package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	qrcode "github.com/skip2/go-qrcode"
)

const (
	version         = "0.1.1"
	frpVersion      = "0.61.1"
	apiBase         = "https://steadip.com/api"
	dashboardURL    = "https://steadip.com/dashboard"
	windowsTaskName = "SteadIP Tunnel Client"
)

type Paths struct {
	BinDir, ConfigDir, StateDir         string
	Frpc, Token, Config, Meta, PID, Log string
	ServiceFile, LaunchAgent            string
}

func paths() Paths {
	home, _ := os.UserHomeDir()

	if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(home, "AppData", "Roaming")
		}
		bin := filepath.Join(local, "SteadIP", "bin")
		cfg := filepath.Join(appdata, "SteadIP")
		state := filepath.Join(local, "SteadIP", "state")
		return Paths{
			BinDir: bin, ConfigDir: cfg, StateDir: state,
			Frpc: filepath.Join(bin, "frpc.exe"), Token: filepath.Join(cfg, "token"), Config: filepath.Join(cfg, "frpc.toml"), Meta: filepath.Join(cfg, "tunnels.json"), PID: filepath.Join(state, "frpc.pid"), Log: filepath.Join(state, "frpc.log"),
		}
	}

	appDir := filepath.Join(home, ".local", "share", "steadip")
	bin := filepath.Join(appDir, "bin")
	cfg := filepath.Join(home, ".config", "steadip")
	state := filepath.Join(home, ".local", "state", "steadip")

	return Paths{
		BinDir: bin, ConfigDir: cfg, StateDir: state,
		Frpc: filepath.Join(bin, "frpc"), Token: filepath.Join(cfg, "token"), Config: filepath.Join(cfg, "frpc.toml"), Meta: filepath.Join(cfg, "tunnels.json"), PID: filepath.Join(state, "frpc.pid"), Log: filepath.Join(state, "frpc.log"),
		ServiceFile: filepath.Join(home, ".config", "systemd", "user", "steadip.service"),
		LaunchAgent: filepath.Join(home, "Library", "LaunchAgents", "com.steadip.client.plist"),
	}
}

func ensureDirs(p Paths) error {
	for _, d := range []string{p.BinDir, p.ConfigDir, p.StateDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

var (
	cyan       = lipgloss.Color("#5FFFD7")
	green      = lipgloss.Color("#8CFF8C")
	red        = lipgloss.Color("#FF6B6B")
	yellow     = lipgloss.Color("#FFD166")
	muted      = lipgloss.Color("#7D8DA1")
	bg         = lipgloss.Color("#070B14")
	panel      = lipgloss.Color("#0D1322")
	border     = lipgloss.Color("#1F2A44")
	white      = lipgloss.Color("#F4F8FF")
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(cyan)
	subtle     = lipgloss.NewStyle().Foreground(muted)
	appStyle   = lipgloss.NewStyle().Padding(1, 2).Background(bg)
	card       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Background(panel).Padding(1, 2)
	activeCard = card.Copy().BorderForeground(cyan)
	okStyle    = lipgloss.NewStyle().Bold(true).Foreground(green)
	errStyle   = lipgloss.NewStyle().Bold(true).Foreground(red)
	warnStyle  = lipgloss.NewStyle().Bold(true).Foreground(yellow)
	codeStyle  = lipgloss.NewStyle().Foreground(white).Background(lipgloss.Color("#050812")).Padding(0, 1)
)

type DeviceCodeResp struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
}

type TokenResp struct {
	AccessToken  string `json:"access_token"`
	UserEmail    string `json:"user_email"`
	UserVerified bool   `json:"user_verified"`
	Error        string `json:"error"`
	Message      string `json:"message"`
}

type ConfigResp struct {
	FRP     string          `json:"frp"`
	Tunnels json.RawMessage `json:"tunnels,omitempty"`
}

type APIError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func postJSON(ctx context.Context, path, token string, payload any, out any) (int, []byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+path, bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, out)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, raw, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, raw, nil
}

func getJSON(ctx context.Context, path, token string, out any) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+path, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, out)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, raw, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, raw, nil
}

func apiErr(raw []byte, fallback string) string {
	var e APIError
	_ = json.Unmarshal(raw, &e)
	if e.Message != "" {
		return e.Message
	}
	switch e.Error {
	case "authorization_pending", "slow_down":
		return e.Error
	case "tunnels_limit_reached":
		return "Maximum number of tunnels reached. Delete an existing tunnel in the dashboard, then try again."
	case "expired_token":
		return "Login expired. Run steadip login again."
	case "access_denied":
		return "Login was denied."
	case "no_device_code":
		return "Device code was lost in transport."
	case "":
		return fallback
	default:
		return e.Error
	}
}

func token(p Paths) (string, error) {
	b, err := os.ReadFile(p.Token)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func saveToken(p Paths, t string) error {
	_ = os.MkdirAll(p.ConfigDir, 0o700)
	return os.WriteFile(p.Token, []byte(t), 0o600)
}

func requireToken(p Paths) (string, error) {
	t, err := token(p)
	if err != nil || t == "" {
		return "", errors.New("not logged in; run steadip login")
	}
	return t, nil
}

func clearConfig(p Paths) {
	_ = os.Remove(p.Config)
	_ = os.Remove(p.Meta)
}

func syncConfig(ctx context.Context, p Paths) error {
	t, err := requireToken(p)
	if err != nil {
		return err
	}
	var cfg ConfigResp
	_, raw, err := getJSON(ctx, "/device/config", t, &cfg)
	if err != nil {
		return errors.New(apiErr(raw, err.Error()))
	}
	if strings.TrimSpace(cfg.FRP) == "" {
		return errors.New("no frp config returned by SteadIP API")
	}
	if err := os.WriteFile(p.Meta, raw, 0o600); err != nil {
		return err
	}
	return os.WriteFile(p.Config, []byte(cfg.FRP), 0o600)
}

func startDeviceLogin(ctx context.Context, p Paths) (DeviceCodeResp, error) {
	var r DeviceCodeResp
	_, raw, err := postJSON(ctx, "/device/code", "", map[string]any{
		"client_name":    "steadip-go-cli",
		"client_version": version,
		"device_name":    host(),
	}, &r)
	if err != nil {
		return r, errors.New(apiErr(raw, err.Error()))
	}
	if r.Interval <= 0 {
		r.Interval = 5
	}
	if r.ExpiresIn <= 0 {
		r.ExpiresIn = 600
	}
	if r.VerificationURI == "" && r.VerificationURIComplete != "" {
		r.VerificationURI = r.VerificationURIComplete
	}
	if r.DeviceCode == "" {
		return r, errors.New("SteadIP API did not return device_code")
	}
	return r, nil
}

func pollDeviceLogin(ctx context.Context, d DeviceCodeResp) (TokenResp, error) {
	interval := time.Duration(d.Interval) * time.Second
	deadline := time.Now().Add(time.Duration(d.ExpiresIn) * time.Second)
	var lastErr string

	for time.Now().Before(deadline) {
		time.Sleep(interval)
		var tr TokenResp
		_, raw, err := postJSON(ctx, "/device/token", "", map[string]any{
			"device_code": d.DeviceCode,
			"user_code":   d.UserCode,
		}, &tr)
		if err == nil && tr.AccessToken != "" {
			return tr, nil
		}
		msg := apiErr(raw, "")
		switch msg {
		case "authorization_pending", "":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		default:
			lastErr = msg
			return TokenResp{}, errors.New(msg)
		}
	}
	if lastErr != "" {
		return TokenResp{}, errors.New(lastErr)
	}
	return TokenResp{}, errors.New("login expired")
}

func saveLoginResult(p Paths, tr TokenResp) error {
	if tr.AccessToken == "" {
		return errors.New("no access token returned")
	}
	return saveToken(p, tr.AccessToken)
}

func qrURLForDevice(d DeviceCodeResp) string {
	url := d.VerificationURIComplete
	if url == "" {
		url = d.VerificationURI
	}
	if url == "" {
		url = dashboardURL
	}
	return url
}

func renderQRCode(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}
	qr, err := qrcode.New(url, qrcode.Medium)
	if err != nil {
		return ""
	}
	return qr.ToSmallString(false)
}

func cliLogin(p Paths) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d, err := startDeviceLogin(ctx, p)
	if err != nil {
		fmt.Fprintln(os.Stderr, errStyle.Render("Error:"), err)
		return 1
	}

	url := qrURLForDevice(d)

	fmt.Println(titleStyle.Render("SteadIP Login"))
	fmt.Println()
	fmt.Println("Open this URL:")
	fmt.Println(codeStyle.Render(url))
	fmt.Println()
	fmt.Println("Enter this code:")
	fmt.Println(warnStyle.Render(d.DeviceCode))
	fmt.Println()
	if qr := renderQRCode(url); qr != "" {
		fmt.Println(qr)
	}
	fmt.Println(subtle.Render("Waiting for authorization..."))

	openURL(url)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				fmt.Print(".")
			}
		}
	}()

	tr, err := pollDeviceLogin(ctx, d)
	cancel()
	<-done
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, errStyle.Render("Error:"), err)
		return 1
	}
	if err := saveLoginResult(p, tr); err != nil {
		fmt.Fprintln(os.Stderr, errStyle.Render("Error:"), err)
		return 1
	}
	plan := "Free"
	if tr.UserVerified {
		plan = "Verified"
	}
	fmt.Println(okStyle.Render("Logged in successfully."))
	if tr.UserEmail != "" {
		fmt.Println("Account:", tr.UserEmail)
	}
	fmt.Println("Plan:", plan)
	fmt.Println()
	fmt.Println("Next:")
	fmt.Println("  steadip up")
	return 0
}

func cliRelogin(p Paths) int {
	fmt.Print("Enter device code from SteadIP dashboard: ")
	r := bufio.NewReader(os.Stdin)
	code, _ := r.ReadString('\n')
	code = strings.TrimSpace(code)
	if code == "" {
		fmt.Fprintln(os.Stderr, errStyle.Render("Error:"), "device code is required")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var tr TokenResp
	_, raw, err := postJSON(ctx, "/device/token", "", map[string]any{
		"device_code":    code,
		"relogin":        true,
		"client_name":    "steadip-go-cli",
		"client_version": version,
		"device_name":    host(),
	}, &tr)
	if err != nil {
		fmt.Fprintln(os.Stderr, errStyle.Render("Error:"), apiErr(raw, err.Error()))
		return 1
	}
	if err := saveLoginResult(p, tr); err != nil {
		fmt.Fprintln(os.Stderr, errStyle.Render("Error:"), err)
		return 1
	}
	clearConfig(p)
	fmt.Println(okStyle.Render("Relogin successful."))
	fmt.Println("Next:")
	fmt.Println("  steadip up")
	return 0
}

func frpOS() (string, error) {
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		return runtime.GOOS, nil
	}
	return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}

func frpArch() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64", nil
	case "arm64":
		return "arm64", nil
	case "arm":
		return "arm", nil
	}
	return "", fmt.Errorf("unsupported arch: %s", runtime.GOARCH)
}

func installFrpc(ctx context.Context, p Paths) error {
	if _, err := os.Stat(p.Frpc); err == nil {
		return nil
	}
	osn, err := frpOS()
	if err != nil {
		return err
	}
	arch, err := frpArch()
	if err != nil {
		return err
	}
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	name := fmt.Sprintf("frp_%s_%s_%s%s", frpVersion, osn, arch, ext)
	url := fmt.Sprintf("https://github.com/fatedier/frp/releases/download/v%s/%s", frpVersion, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("failed to download frpc: HTTP %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "steadip-frp-*"+ext)
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		return err
	}
	_ = tmp.Close()
	if runtime.GOOS == "windows" {
		return extractZip(tmp.Name(), p.Frpc)
	}
	return extractTarGz(tmp.Name(), p.Frpc)
}

func extractTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(h.Name) == "frpc" && h.Typeflag == tar.TypeReg {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		}
	}
	return errors.New("frpc not found in archive")
}

func extractZip(src, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if filepath.Base(f.Name) == "frpc.exe" {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, rc)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		}
	}
	return errors.New("frpc.exe not found in archive")
}

func pidRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	switch runtime.GOOS {
	case "windows":
		return exec.Command("powershell.exe", "-NoProfile", "-Command", fmt.Sprintf("Get-Process -Id %d -ErrorAction SilentlyContinue", pid)).Run() == nil
	case "linux":
		_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
		return err == nil
	case "darwin":
		return exec.Command("ps", "-p", strconv.Itoa(pid)).Run() == nil
	default:
		proc, err := os.FindProcess(pid)
		return err == nil && proc != nil
	}
}

func readPID(p Paths) int {
	b, err := os.ReadFile(p.PID)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func manualRunning(p Paths) bool { return pidRunning(readPID(p)) }

func stopManual(p Paths) {
	pid := readPID(p)
	if pid <= 0 {
		_ = os.Remove(p.PID)
		return
	}
	if runtime.GOOS == "windows" {
		_ = exec.Command("powershell.exe", "-NoProfile", "-Command", fmt.Sprintf("Stop-Process -Id %d -Force -ErrorAction SilentlyContinue", pid)).Run()
	} else if proc, err := os.FindProcess(pid); err == nil && proc != nil {
		_ = proc.Kill()
	}
	_ = os.Remove(p.PID)
}

func startManual(p Paths) error {
	if _, err := os.Stat(p.Frpc); err != nil {
		return fmt.Errorf("frpc is missing: %s", p.Frpc)
	}
	if _, err := os.Stat(p.Config); err != nil {
		return errors.New("no frpc config found; run steadip sync")
	}
	stopManual(p)
	logFile, err := os.OpenFile(p.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(p.Frpc, "-c", p.Config)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	if err := os.WriteFile(p.PID, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		_ = logFile.Close()
		return err
	}
	go func() { _ = cmd.Wait(); _ = logFile.Close() }()
	time.Sleep(time.Second)
	if !manualRunning(p) {
		return fmt.Errorf("frpc failed to start; check logs: %s", p.Log)
	}
	return nil
}

func autoEnabled(p Paths) bool {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("systemctl", "--user", "is-enabled", "--quiet", "steadip.service").Run() == nil
	case "darwin":
		_, err := os.Stat(p.LaunchAgent)
		return err == nil
	case "windows":
		out, _ := exec.Command("powershell.exe", "-NoProfile", "-Command", fmt.Sprintf(`$t=Get-ScheduledTask -TaskName "%s" -ErrorAction SilentlyContinue; if ($t) { "yes" }`, windowsTaskName)).Output()
		return strings.TrimSpace(string(out)) == "yes"
	}
	return false
}

func daemonActive(p Paths) bool {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("systemctl", "--user", "is-active", "--quiet", "steadip.service").Run() == nil
	case "darwin":
		return exec.Command("launchctl", "list", "com.steadip.client").Run() == nil
	case "windows":
		out, _ := exec.Command("powershell.exe", "-NoProfile", "-Command", fmt.Sprintf(`$t=Get-ScheduledTask -TaskName "%s" -ErrorAction SilentlyContinue; if ($t -and $t.State -eq "Running") { "yes" }`, windowsTaskName)).Output()
		return strings.TrimSpace(string(out)) == "yes"
	}
	return false
}

func stopDaemon(p Paths) {
	switch runtime.GOOS {
	case "linux":
		_ = exec.Command("systemctl", "--user", "stop", "steadip.service").Run()
	case "darwin":
		_ = exec.Command("launchctl", "unload", p.LaunchAgent).Run()
	case "windows":
		_ = exec.Command("powershell.exe", "-NoProfile", "-Command", fmt.Sprintf(`Stop-ScheduledTask -TaskName "%s" -ErrorAction SilentlyContinue`, windowsTaskName)).Run()
	}
}

func restartDaemon(p Paths) error {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("systemctl", "--user", "restart", "steadip.service").Run()
	case "darwin":
		_ = exec.Command("launchctl", "unload", p.LaunchAgent).Run()
		return exec.Command("launchctl", "load", p.LaunchAgent).Run()
	case "windows":
		stopDaemon(p)
		return exec.Command("powershell.exe", "-NoProfile", "-Command", fmt.Sprintf(`Start-ScheduledTask -TaskName "%s"`, windowsTaskName)).Run()
	}
	return nil
}

func enableAuto(p Paths) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "linux":
		_ = os.MkdirAll(filepath.Dir(p.ServiceFile), 0o755)
		service := fmt.Sprintf(`[Unit]
Description=SteadIP Tunnel Client
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s daemon
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`, exe)
		if err := os.WriteFile(p.ServiceFile, []byte(service), 0o644); err != nil {
			return err
		}
		if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
			return err
		}
		return exec.Command("systemctl", "--user", "enable", "--now", "steadip.service").Run()
	case "darwin":
		_ = os.MkdirAll(filepath.Dir(p.LaunchAgent), 0o755)
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.steadip.client</string>
<key>ProgramArguments</key><array><string>%s</string><string>daemon</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
<key>StandardOutPath</key><string>%s</string>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, exe, filepath.Join(p.StateDir, "launchd.log"), filepath.Join(p.StateDir, "launchd.err.log"))
		if err := os.WriteFile(p.LaunchAgent, []byte(plist), 0o644); err != nil {
			return err
		}
		_ = exec.Command("launchctl", "unload", p.LaunchAgent).Run()
		return exec.Command("launchctl", "load", p.LaunchAgent).Run()
	case "windows":
		ps := fmt.Sprintf(`$Action = New-ScheduledTaskAction -Execute "%s" -Argument "daemon"; $Trigger = New-ScheduledTaskTrigger -AtLogOn; Register-ScheduledTask -TaskName "%s" -Action $Action -Trigger $Trigger -Force | Out-Null; Start-ScheduledTask -TaskName "%s"`, exe, windowsTaskName, windowsTaskName)
		return exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", ps).Run()
	}
	return errors.New("unsupported OS")
}

func disableAuto(p Paths) {
	switch runtime.GOOS {
	case "linux":
		_ = exec.Command("systemctl", "--user", "disable", "--now", "steadip.service").Run()
		_ = os.Remove(p.ServiceFile)
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	case "darwin":
		_ = exec.Command("launchctl", "unload", p.LaunchAgent).Run()
		_ = os.Remove(p.LaunchAgent)
	case "windows":
		_ = exec.Command("powershell.exe", "-NoProfile", "-Command", fmt.Sprintf(`Stop-ScheduledTask -TaskName "%s" -ErrorAction SilentlyContinue; Unregister-ScheduledTask -TaskName "%s" -Confirm:$false -ErrorAction SilentlyContinue`, windowsTaskName, windowsTaskName)).Run()
	}
}

type screen int

const (
	home screen = iota
	working
	loginScreen
	reloginScreen
	resultScreen
	statusScreen
	logsScreen
)

type model struct {
	p                    Paths
	spin                 spinner.Model
	screen               screen
	cursor               int
	width, height        int
	message, result, err string
	login                *DeviceCodeResp
	reloginInput         textinput.Model
}

type doneMsg struct {
	title, body string
	err         error
}

type loginCodeMsg struct {
	resp DeviceCodeResp
	err  error
}

type loginDoneMsg struct {
	resp TokenResp
	err  error
}

var menu = []struct{ key, label, desc string }{
	{"login", "Login", "Browser/device-code login"},
	{"relogin", "Relogin", "Use webapp device code"},
	{"sync", "Sync", "Fetch dashboard config"},
	{"up", "Up", "Start tunnels"},
	{"down", "Down", "Stop tunnels"},
	{"enable", "Enable", "Auto-start daemon"},
	{"disable", "Disable", "Remove auto-start"},
	{"status", "Status", "Current tunnel status"},
	{"logs", "Logs", "Recent frpc logs"},
	{"config", "Config", "Show frpc config"},
	{"logout", "Logout", "Remove local token"},
}

func newModel(p Paths) model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(cyan)

	ti := textinput.New()
	ti.Placeholder = "Paste device code from SteadIP dashboard"
	ti.CharLimit = 128
	ti.Width = 48
	ti.Prompt = ""
	ti.Focus()

	return model{p: p, spin: s, screen: home, width: 100, height: 32, reloginInput: ti}
}

func (m model) Init() tea.Cmd { return m.spin.Tick }

func loginStart(p Paths) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		r, err := startDeviceLogin(ctx, p)
		if err != nil {
			return loginCodeMsg{err: err}
		}
		return loginCodeMsg{resp: r}
	}
}

func pollLogin(p Paths, d DeviceCodeResp) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		tr, err := pollDeviceLogin(ctx, d)
		if err != nil {
			return loginDoneMsg{err: err}
		}
		return loginDoneMsg{resp: tr}
	}
}

func reloginWithCodeCmd(p Paths, code string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var tr TokenResp
		_, raw, err := postJSON(ctx, "/device/token", "", map[string]any{
			"device_code":    code,
			"relogin":        true,
			"client_name":    "steadip-go-cli",
			"client_version": version,
			"device_name":    host(),
		}, &tr)
		if err != nil {
			return doneMsg{title: "Relogin", err: errors.New(apiErr(raw, err.Error()))}
		}
		if err := saveLoginResult(p, tr); err != nil {
			return doneMsg{title: "Relogin", err: err}
		}
		clearConfig(p)
		plan := "Free"
		if tr.UserVerified {
			plan = "Verified"
		}
		email := tr.UserEmail
		if email == "" {
			email = "unknown"
		}
		return doneMsg{title: "Relogin", body: fmt.Sprintf("Relogin successful.\n\nAccount: %s\nPlan: %s\n\nRun steadip up to start this tunnel config.", email, plan)}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
		inputWidth := v.Width - 18
		if inputWidth > 64 {
			inputWidth = 64
		}
		if inputWidth < 24 {
			inputWidth = 24
		}
		m.reloginInput.Width = inputWidth
		return m, nil

	case tea.KeyMsg:
		if m.screen == reloginScreen {
			switch v.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.screen = home
				m.err = ""
				m.reloginInput.SetValue("")
				return m, nil
			case "enter":
				code := strings.TrimSpace(m.reloginInput.Value())
				if code == "" {
					m.err = "Device code is required."
					return m, nil
				}
				m.screen = working
				m.message = "Authorizing device..."
				m.err = ""
				return m, reloginWithCodeCmd(m.p, code)
			}
			var cmd tea.Cmd
			m.reloginInput, cmd = m.reloginInput.Update(v)
			return m, cmd
		}

		switch v.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc":
			m.screen = home
			m.err = ""
			m.result = ""
			return m, nil
		case "up", "k":
			if m.screen == home && m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.screen == home && m.cursor < len(menu)-1 {
				m.cursor++
			}
		case "enter":
			if m.screen == home {
				return m.run(menu[m.cursor].key)
			}
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(v)
		return m, cmd

	case doneMsg:
		m.screen = resultScreen
		m.message = v.title
		if v.err != nil {
			m.err = v.err.Error()
			m.result = ""
		} else {
			m.err = ""
			m.result = v.body
		}
		return m, nil

	case loginCodeMsg:
		if v.err != nil {
			m.screen = resultScreen
			m.err = v.err.Error()
			m.message = "Login failed"
			return m, nil
		}
		m.login = &v.resp
		m.screen = loginScreen
		url := v.resp.VerificationURIComplete
		if url == "" {
			url = v.resp.VerificationURI
		}
		openURL(url)
		return m, tea.Batch(m.spin.Tick, pollLogin(m.p, v.resp))

	case loginDoneMsg:
		m.screen = resultScreen
		m.message = "Login"
		if v.err != nil {
			m.err = v.err.Error()
			return m, nil
		}
		if err := saveLoginResult(m.p, v.resp); err != nil {
			m.err = err.Error()
			return m, nil
		}
		plan := "Free"
		if v.resp.UserVerified {
			plan = "Verified"
		}
		email := v.resp.UserEmail
		if email == "" {
			email = "unknown"
		}
		m.result = fmt.Sprintf("Logged in successfully.\n\nAccount: %s\nPlan: %s\n\nConfigure tunnels in dashboard:\n%s\n\nThen run steadip up.", email, plan, dashboardURL)
		return m, nil
	}
	return m, nil
}

func (m model) run(key string) (tea.Model, tea.Cmd) {
	ctx := context.Background()
	work := func(title string, fn func() (string, error)) (tea.Model, tea.Cmd) {
		m.screen = working
		m.message = title
		return m, func() tea.Msg { b, e := fn(); return doneMsg{title: title, body: b, err: e} }
	}
	switch key {
	case "login":
		m.screen = working
		m.message = "Requesting device login..."
		return m, loginStart(m.p)
	case "relogin":
		m.screen = reloginScreen
		m.err = ""
		m.result = ""
		m.message = "Relogin"
		m.reloginInput.SetValue("")
		m.reloginInput.Focus()
		return m, textinput.Blink
	case "sync":
		return work("Sync", func() (string, error) { return "Config written:\n" + m.p.Config, syncConfig(ctx, m.p) })
	case "up":
		return work("Up", func() (string, error) {
			if err := installFrpc(ctx, m.p); err != nil {
				return "", err
			}
			if err := syncConfig(ctx, m.p); err != nil {
				return "", err
			}
			if daemonActive(m.p) {
				return "Daemon restarted with latest config.", restartDaemon(m.p)
			}
			return "Tunnels started.\n\nLogs:\n" + m.p.Log, startManual(m.p)
		})
	case "down":
		return work("Down", func() (string, error) {
			stopManual(m.p)
			if daemonActive(m.p) {
				stopDaemon(m.p)
			}
			return "Tunnels stopped.", nil
		})
	case "enable":
		return work("Enable", func() (string, error) {
			if err := installFrpc(ctx, m.p); err != nil {
				return "", err
			}
			return "Auto-start enabled and started.", enableAuto(m.p)
		})
	case "disable":
		return work("Disable", func() (string, error) { disableAuto(m.p); return "Auto-start disabled.", nil })
	case "status":
		m.screen = statusScreen
		m.result = statusText(m.p)
		return m, nil
	case "logs":
		m.screen = logsScreen
		m.result = lastLines(m.p.Log, 80)
		if m.result == "" {
			m.result = "No logs yet."
		}
		return m, nil
	case "config":
		return work("Config", func() (string, error) {
			b, e := os.ReadFile(m.p.Config)
			if e != nil {
				return "", e
			}
			return scrub(string(b)), nil
		})
	case "logout":
		return work("Logout", func() (string, error) {
			stopManual(m.p)
			if daemonActive(m.p) {
				stopDaemon(m.p)
			}
			_ = os.Remove(m.p.Token)
			clearConfig(m.p)
			return "Logged out and local config cleared.", nil
		})
	}
	return m, nil
}

func (m model) View() string {
	w, h := m.width, m.height
	if w <= 0 {
		w = 100
	}
	if h <= 0 {
		h = 32
	}

	header := titleStyle.Render("SteadIP") + " " + subtle.Render("Free HTTP/HTTPS tunnels for local apps, webhooks, and homelabs")
	contentW := maxInt(40, w-4)
	contentH := maxInt(12, h-2)

	var body string
	switch m.screen {
	case home:
		body = header + "\n\n" + m.viewHome() + "\n\n" + subtle.Render("↑/↓ navigate • enter select • esc back • q quit")
	case working:
		box := activeCard.Width(minInt(86, maxInt(40, w-10))).Render(m.spin.View() + "  " + m.message)
		body = header + "\n\n" + lipgloss.Place(contentW, contentH-4, lipgloss.Center, lipgloss.Center, box, lipgloss.WithWhitespaceBackground(bg))
	case loginScreen:
		body = header + "\n\n" + m.viewLogin()
	case reloginScreen:
		body = header + "\n\n" + m.viewReloginModal()
	case resultScreen:
		resultBody := m.result
		if m.err != "" {
			resultBody = errStyle.Render("Error") + "\n\n" + m.err
		} else {
			resultBody = okStyle.Render(m.message) + "\n\n" + resultBody
		}
		box := activeCard.Width(minInt(96, maxInt(40, w-10))).Render(resultBody)
		body = header + "\n\n" + lipgloss.Place(contentW, contentH-6, lipgloss.Center, lipgloss.Center, box, lipgloss.WithWhitespaceBackground(bg)) + "\n\n" + subtle.Render("esc back • q quit")
	case statusScreen:
		box := activeCard.Width(minInt(92, maxInt(40, w-10))).Render(titleStyle.Render("Status") + "\n\n" + m.result)
		body = header + "\n\n" + lipgloss.Place(contentW, contentH-6, lipgloss.Center, lipgloss.Center, box, lipgloss.WithWhitespaceBackground(bg)) + "\n\n" + subtle.Render("esc back • q quit")
	case logsScreen:
		box := activeCard.Width(maxInt(60, w-10)).Height(maxInt(14, h-10)).Render(titleStyle.Render("Logs") + "\n\n" + m.result)
		body = header + "\n\n" + box + "\n\n" + subtle.Render("esc back • q quit")
	}

	return lipgloss.NewStyle().Width(w).Height(h).Background(bg).Render(appStyle.Width(w).Height(h).Background(bg).Render(body))
}

func (m model) viewHome() string {
	available := m.width - 8
	if available < 82 {
		available = 82
	}
	leftWidth := int(float64(available) * 0.62)
	rightWidth := available - leftWidth - 4
	if rightWidth < 30 {
		rightWidth = 30
	}
	cardWidth := (leftWidth - 4) / 2
	if cardWidth < 28 {
		cardWidth = 28
	}

	var rows []string
	for i, it := range menu {
		style := card.Width(cardWidth)
		mark := "  "
		if i == m.cursor {
			style = activeCard.Width(cardWidth)
			mark = "› "
		}
		rows = append(rows, style.Render(titleStyle.Render(mark+it.label)+"\n"+subtle.Render("  "+it.desc)))
	}

	var lines []string
	for i := 0; i < len(rows); i += 2 {
		if i+1 < len(rows) {
			lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, rows[i], "  ", rows[i+1]))
		} else {
			lines = append(lines, rows[i])
		}
	}

	sideH := maxInt(16, len(lines)*5-2)
	side := card.Width(rightWidth).Height(sideH).Render(titleStyle.Render("Local Status") + "\n\n" + statusMini(m.p) + "\n\n" + subtle.Render("Dashboard:\n"+dashboardURL))
	return lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.JoinVertical(lipgloss.Left, lines...), "  ", side)
}

func (m model) viewLogin() string {
	if m.login == nil {
		return ""
	}
	d := m.login
	boxWidth := minInt(96, maxInt(48, m.width-10))
	url := qrURLForDevice(*d)
	code := d.DeviceCode

	body := titleStyle.Render("Approve login") + "\n\n" +
		subtle.Render("Scan the QR code, or open the URL and enter the code.") + "\n\n" +
		"Open this URL:\n" + codeStyle.Render(url) + "\n\n" +
		"Device code:\n" + warnStyle.Render(code)

	if m.width >= 90 && m.height >= 32 {
		if qr := renderQRCode(url); qr != "" {
			body += "\n\n" + qr
		}
	} else {
		body += "\n\n" + warnStyle.Render("Terminal too small for QR code. Use the URL/code above.")
	}

	body += "\n" + m.spin.View() + " Waiting for authorization..."
	placed := lipgloss.Place(maxInt(40, m.width-4), maxInt(12, m.height-8), lipgloss.Center, lipgloss.Center, activeCard.Width(boxWidth).Render(body), lipgloss.WithWhitespaceBackground(bg))
	return lipgloss.NewStyle().Width(maxInt(40, m.width-4)).Height(maxInt(12, m.height-8)).Background(bg).Render(placed)
}

func (m model) viewReloginModal() string {
	boxWidth := m.width - 12
	if boxWidth > 78 {
		boxWidth = 78
	}
	if boxWidth < 44 {
		boxWidth = 44
	}
	inputBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cyan).
		Background(lipgloss.Color("#050812")).
		Foreground(white).
		Padding(0, 1).
		Width(boxWidth - 8).
		Render(m.reloginInput.View())
	body := titleStyle.Render("Relogin with device code") + "\n\n" +
		subtle.Render("Paste the device code generated from the SteadIP dashboard.") + "\n\n" +
		inputBox
	if m.err != "" {
		body += "\n\n" + errStyle.Render(m.err)
	}
	body += "\n\n" + subtle.Render("enter submit • esc cancel")
	modal := activeCard.Width(boxWidth).Padding(1, 2).Background(panel).Render(body)
	placed := lipgloss.Place(maxInt(40, m.width-4), maxInt(12, m.height-8), lipgloss.Center, lipgloss.Center, modal, lipgloss.WithWhitespaceBackground(bg))
	return lipgloss.NewStyle().Width(maxInt(40, m.width-4)).Height(maxInt(12, m.height-8)).Background(bg).Render(placed)
}

func statusMini(p Paths) string {
	logged := "no"
	if t, err := token(p); err == nil && t != "" {
		logged = "yes"
	}
	man := "stopped"
	if manualRunning(p) {
		man = "running"
	}
	auto := "disabled"
	if autoEnabled(p) {
		auto = "enabled"
	}
	daemon := "stopped"
	if daemonActive(p) {
		daemon = "running"
	}
	return fmt.Sprintf("Logged in: %s\nManual tunnel: %s\nAuto-start: %s\nDaemon: %s", logged, man, auto, daemon)
}

func statusText(p Paths) string {
	return statusMini(p) + "\n\nDashboard: " + dashboardURL + "\nConfig:    " + p.Config + "\nLogs:      " + p.Log + "\nfrpc:      " + p.Frpc
}

func openURL(url string) {
	if url == "" {
		return
	}
	switch runtime.GOOS {
	case "linux":
		_ = exec.Command("xdg-open", url).Start()
	case "darwin":
		_ = exec.Command("open", url).Start()
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
}

func host() string {
	h, _ := os.Hostname()
	if h == "" {
		return "unknown"
	}
	return h
}

func lastLines(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func scrub(s string) string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "connection_token") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				line = strings.TrimSpace(parts[0]) + " = \"***\""
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func nonInteractive(args []string, p Paths) int {
	ctx := context.Background()
	fail := func(e error) int { fmt.Fprintln(os.Stderr, errStyle.Render("Error:"), e); return 1 }
	ok := func(s string) int { fmt.Println(okStyle.Render(s)); return 0 }

	switch args[0] {
	case "login":
		return cliLogin(p)
	case "relogin":
		return cliRelogin(p)
	case "sync":
		if err := syncConfig(ctx, p); err != nil {
			return fail(err)
		}
		return ok("Config written: " + p.Config)
	case "up":
		if err := installFrpc(ctx, p); err != nil {
			return fail(err)
		}
		if err := syncConfig(ctx, p); err != nil {
			return fail(err)
		}
		if daemonActive(p) {
			if err := restartDaemon(p); err != nil {
				return fail(err)
			}
			return ok("Daemon restarted with latest config")
		}
		if err := startManual(p); err != nil {
			return fail(err)
		}
		return ok("Tunnels started")
	case "down":
		stopManual(p)
		if daemonActive(p) {
			stopDaemon(p)
		}
		return ok("Tunnels stopped")
	case "enable":
		if err := installFrpc(ctx, p); err != nil {
			return fail(err)
		}
		if err := enableAuto(p); err != nil {
			return fail(err)
		}
		return ok("Auto-start enabled")
	case "disable":
		disableAuto(p)
		return ok("Auto-start disabled")
	case "status":
		fmt.Println(statusText(p))
		return 0
	case "logs":
		fmt.Print(lastLines(p.Log, 120))
		return 0
	case "config":
		b, err := os.ReadFile(p.Config)
		if err != nil {
			return fail(err)
		}
		fmt.Println(scrub(string(b)))
		return 0
	case "logout":
		stopManual(p)
		if daemonActive(p) {
			stopDaemon(p)
		}
		_ = os.Remove(p.Token)
		clearConfig(p)
		return ok("Logged out")
	case "daemon":
		if err := installFrpc(ctx, p); err != nil {
			return fail(err)
		}
		if err := syncConfig(ctx, p); err != nil {
			return fail(err)
		}
		cmd := exec.Command(p.Frpc, "-c", p.Config)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fail(err)
		}
		return 0
	case "help", "-h", "--help":
		printHelp()
		return 0
	default:
		fmt.Fprintln(os.Stderr, "Unknown command:", args[0])
		printHelp()
		return 1
	}
}

func printHelp() {
	fmt.Println(`SteadIP CLI

Usage:
  steadip                 Open interactive TUI
  steadip login           Command-line device-code login
  steadip relogin         Command-line relogin with dashboard code
  steadip sync            Fetch dashboard tunnel config
  steadip up              Sync and start tunnels
  steadip down            Stop tunnels
  steadip enable          Enable auto-start
  steadip disable         Disable auto-start
  steadip status          Show current tunnel status
  steadip logs            Show recent frpc logs
  steadip config          Show frpc config with secrets hidden
  steadip logout          Stop tunnels and remove local token
`)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func main() {
	p := paths()
	if err := ensureDirs(p); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) > 1 {
		os.Exit(nonInteractive(os.Args[1:], p))
	}
	if _, err := tea.NewProgram(newModel(p), tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
