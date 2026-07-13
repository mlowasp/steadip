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
	frpVersion      = "0.70.0"
	apiBase         = "https://steadip.com/api"
	dashboardURL    = "https://steadip.com"
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
	cyan     = lipgloss.Color("#00F5FF") // electric cyan
	neonPink = lipgloss.Color("#FF2D78") // neon magenta/pink
	green    = lipgloss.Color("#39FF14") // neon green
	red      = lipgloss.Color("#FF2D55") // hot red
	yellow   = lipgloss.Color("#FFE600") // acid yellow
	purple   = lipgloss.Color("#BD00FF") // neon purple
	muted    = lipgloss.Color("#7E7FA8") // muted blue-purple
	bg       = lipgloss.Color("#06000F") // near-black
	panel    = lipgloss.Color("#0D0020") // deep purple panel
	border   = lipgloss.Color("#3A0060") // dim purple border
	white    = lipgloss.Color("#E0E8FF") // cold white
	dimText  = lipgloss.Color("#2A1B42") // very dim purple

	titleStyle       = lipgloss.NewStyle().Bold(true).Foreground(neonPink)
	subtle           = lipgloss.NewStyle().Foreground(muted)
	appStyle         = lipgloss.NewStyle().Padding(1, 2).Background(bg)
	okStyle          = lipgloss.NewStyle().Bold(true).Foreground(green)
	errStyle         = lipgloss.NewStyle().Bold(true).Foreground(red)
	warnStyle        = lipgloss.NewStyle().Bold(true).Foreground(yellow)
	codeStyle        = lipgloss.NewStyle().Foreground(cyan).Background(lipgloss.Color("#0A001A")).Padding(0, 1)
	panelStyle       = lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(border).Background(panel).Padding(1, 2)
	activePanelStyle = panelStyle.Copy().BorderForeground(neonPink)
	monBorderSt      = lipgloss.NewStyle().Foreground(border)
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
	p                         Paths
	spin                      spinner.Model
	screen                    screen
	cursor                    int
	width, height             int
	message, result, err      string
	login                     *DeviceCodeResp
	reloginInput              textinput.Model
	launchMonitor             bool
	logsLines                 []string
	logsScrollV, logsScrollH  int
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

var menu = []struct{ key, label, desc, shortcut, section string }{
	{"login",   "Login",   "Authenticate via browser",       "l", "Auth"},
	{"relogin", "Relogin", "Reuse a dashboard device code",  "r", ""},
	{"sync",    "Sync",    "Pull config from dashboard",     "s", "Tunnel"},
	{"up",      "Up",      "Install, sync & start",          "u", ""},
	{"down",    "Down",    "Stop running tunnels",            "d", ""},
	{"enable",  "Enable",  "Enable auto-start daemon",       "e", "System"},
	{"disable", "Disable", "Remove auto-start",              "a", ""},
	{"status",  "Status",  "View connection status",         "t", "Info"},
	{"logs",    "Logs",    "Tail recent frpc logs",           "g", ""},
	{"monitor", "Monitor", "Live tunnel monitor TUI",        "m", ""},
	{"config",  "Config",  "Inspect frpc config",            "c", ""},
	{"logout",  "Logout",  "Stop tunnels & sign out",        "o", "Account"},
}

func boolStr(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

func menuIndexByShortcut(sc string) int {
	for i, it := range menu {
		if it.shortcut == sc {
			return i
		}
	}
	return -1
}

func newModel(p Paths) model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(neonPink)

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

		if m.screen == logsScreen {
			switch v.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "esc":
				m.screen = home
				return m, nil
			case "up", "k":
				m.logsScrollV++
			case "down", "j":
				if m.logsScrollV > 0 {
					m.logsScrollV--
				}
			case "left":
				if m.logsScrollH > 0 {
					m.logsScrollH--
				}
			case "right":
				m.logsScrollH++
			}
			return m, nil
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
		default:
			if m.screen == home {
				if idx := menuIndexByShortcut(v.String()); idx >= 0 {
					m.cursor = idx
					return m.run(menu[idx].key)
				}
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
		m.logsScrollV = 0
		m.logsScrollH = 0
		raw := lastLines(m.p.Log, 2000)
		if raw == "" {
			m.logsLines = nil
		} else {
			m.logsLines = strings.Split(strings.TrimRight(raw, "\n"), "\n")
		}
		return m, nil
	case "monitor":
		m.launchMonitor = true
		return m, tea.Quit
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

	logo := lipgloss.NewStyle().Bold(true).Foreground(neonPink).Render("// STEADIP //")
	sep := lipgloss.NewStyle().Foreground(purple).Render("  »  ")
	tagline := subtle.Render("encrypted tunnels for the grid  ·  free for homelabs & webhooks")
	divW := w - 4
	if divW < 0 {
		divW = 0
	}
	if divW > 200 {
		divW = 200
	}
	divider := lipgloss.NewStyle().Foreground(border).Render(strings.Repeat("━", divW))
	header := logo + sep + tagline + "\n" + divider

	contentW := maxInt(40, w-4)
	contentH := maxInt(12, h-6)

	var body string
	switch m.screen {
	case home:
		footer := subtle.Render("↑↓/jk » navigate  //  enter » select  //  q » quit")
		body = header + "\n\n" + m.viewHome() + "\n\n" + footer
	case working:
		content := m.spin.View() + "  " + lipgloss.NewStyle().Foreground(white).Render(m.message)
		box := activePanelStyle.Width(minInt(74, maxInt(30, w-14))).Render(content)
		body = header + "\n\n" + lipgloss.Place(contentW, contentH-4, lipgloss.Center, lipgloss.Center, box, lipgloss.WithWhitespaceBackground(bg))
	case loginScreen:
		body = header + "\n\n" + m.viewLogin()
	case reloginScreen:
		body = header + "\n\n" + m.viewReloginModal()
	case resultScreen:
		var resultBody string
		if m.err != "" {
			resultBody = errStyle.Render("✕  "+m.message) + "\n\n" + lipgloss.NewStyle().Foreground(white).Render(m.err)
		} else {
			resultBody = okStyle.Render("✓  "+m.message) + "\n\n" + lipgloss.NewStyle().Foreground(white).Render(m.result)
		}
		box := activePanelStyle.Width(minInt(86, maxInt(34, w-12))).Render(resultBody)
		body = header + "\n\n" +
			lipgloss.Place(contentW, contentH-6, lipgloss.Center, lipgloss.Center, box, lipgloss.WithWhitespaceBackground(bg)) +
			"\n\n" + subtle.Render("esc » back  //  q » quit")
	case statusScreen:
		box := activePanelStyle.Width(minInt(82, maxInt(34, w-12))).Render(
			titleStyle.Render("Status") + "\n\n" + m.result,
		)
		body = header + "\n\n" +
			lipgloss.Place(contentW, contentH-6, lipgloss.Center, lipgloss.Center, box, lipgloss.WithWhitespaceBackground(bg)) +
			"\n\n" + subtle.Render("esc » back  //  q » quit")
	case logsScreen:
		panelW := maxInt(54, w-8)
		panelH := maxInt(12, h-6)
		// ThickBorder(1+1=2) + Padding(top/bot 1+1=2, left/right 2+2=4)
		innerW := panelW - 6
		innerH := panelH - 4
		// title(1) + blank(1) + hscrollbar(1) = 3 rows overhead
		usable := innerH - 3
		if usable < 1 {
			usable = 1
		}
		// 1 col reserved for vertical scrollbar
		viewW := innerW - 1
		if viewW < 4 {
			viewW = 4
		}

		lines := m.logsLines
		total := len(lines)

		if total == 0 {
			box := activePanelStyle.Width(panelW).Height(panelH).Render(
				titleStyle.Render("// Logs") + "\n\n" + subtle.Render("No logs yet."),
			)
			body = header + "\n\n" + box + "\n\n" + subtle.Render("esc » back  //  q » quit")
			break
		}

		// vertical scroll: 0 = bottom (newest), higher = older
		maxScrollV := total - usable
		if maxScrollV < 0 {
			maxScrollV = 0
		}
		scrollV := m.logsScrollV
		if scrollV > maxScrollV {
			scrollV = maxScrollV
		}
		start := total - usable - scrollV
		if start < 0 {
			start = 0
		}
		end := start + usable
		if end > total {
			end = total
		}
		visible := lines[start:end]

		// compute max line rune width across all lines for hscrollbar
		maxLineW := viewW
		for _, l := range lines {
			if rw := len([]rune(l)); rw > maxLineW {
				maxLineW = rw
			}
		}
		maxScrollH := maxLineW - viewW
		if maxScrollH < 0 {
			maxScrollH = 0
		}
		scrollH := m.logsScrollH
		if scrollH > maxScrollH {
			scrollH = maxScrollH
		}

		vsb := makeScrollbar(total, usable, scrollV)

		var rows []string
		for i := 0; i < usable; i++ {
			var line string
			if i < len(visible) {
				line = hClip(visible[i], scrollH, viewW)
			}
			pad := viewW - lipgloss.Width(line)
			if pad < 0 {
				pad = 0
			}
			rows = append(rows, lipgloss.NewStyle().Foreground(white).Render(line)+strings.Repeat(" ", pad)+vsb[i])
		}
		// horizontal scrollbar row fills the full innerW
		rows = append(rows, makeHScrollbar(maxLineW, viewW, scrollH)+" ")

		content := titleStyle.Render("// Logs") + "\n\n" + strings.Join(rows, "\n")
		box := activePanelStyle.Width(panelW).Height(panelH).Render(content)
		body = header + "\n\n" + box + "\n\n" + subtle.Render("↑↓ » scroll  //  ←→ » h-scroll  //  esc » back  //  q » quit")
	}

	return lipgloss.NewStyle().Width(w).Height(h).Background(bg).Render(
		appStyle.Width(w).Height(h).Background(bg).Render(body),
	)
}

func (m model) viewHome() string {
	available := m.width - 8
	if available < 84 {
		available = 84
	}
	statusW := 32
	if available > 120 {
		statusW = 38
	}
	menuW := available - statusW - 4

	sectionSt := lipgloss.NewStyle().Foreground(purple).Bold(true)
	cursorSt := lipgloss.NewStyle().Foreground(neonPink).Bold(true)
	scSt := lipgloss.NewStyle().Foreground(dimText)
	scSelSt := lipgloss.NewStyle().Foreground(yellow).Bold(true)
	labelSt := lipgloss.NewStyle().Foreground(white)
	labelSelSt := lipgloss.NewStyle().Foreground(cyan).Bold(true)

	var menuLines []string
	for i, it := range menu {
		if it.section != "" {
			if i > 0 {
				menuLines = append(menuLines, "")
			}
			menuLines = append(menuLines, sectionSt.Render("  // "+strings.ToUpper(it.section)))
		}

		sc := "[" + it.shortcut + "]"
		label := fmt.Sprintf("%-9s", it.label)

		if i == m.cursor {
			line := " " + cursorSt.Render("▶") + " " + scSelSt.Render(sc) + " " + labelSelSt.Render(label) + "  " + subtle.Render(it.desc)
			menuLines = append(menuLines, line)
		} else {
			line := "   " + scSt.Render(sc) + " " + labelSt.Render(label) + "  " + lipgloss.NewStyle().Foreground(dimText).Render(it.desc)
			menuLines = append(menuLines, line)
		}
	}

	leftPanel := panelStyle.Width(menuW - 6).Render(strings.Join(menuLines, "\n"))

	divLine := lipgloss.NewStyle().Foreground(border).Render(strings.Repeat("━", statusW-10))
	statusContent := titleStyle.Render("// STATUS") + "\n\n" + statusMini(m.p) +
		"\n\n" + divLine +
		"\n" + subtle.Render("Dashboard") +
		"\n" + lipgloss.NewStyle().Foreground(cyan).Render(dashboardURL)
	rightPanel := panelStyle.Width(statusW - 6).Render(statusContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, "  ", rightPanel)
}

func (m model) viewLogin() string {
	if m.login == nil {
		return ""
	}
	d := m.login
	boxWidth := minInt(88, maxInt(48, m.width-12))
	url := qrURLForDevice(*d)

	fieldLabel := lipgloss.NewStyle().Foreground(muted).Render
	body := titleStyle.Render("// AUTHENTICATE") + "\n\n" +
		subtle.Render("Scan the QR code or open the URL, then enter the device code.") + "\n\n" +
		fieldLabel("URL") + "\n" + codeStyle.Render(url) + "\n\n" +
		fieldLabel("Device Code") + "\n" + warnStyle.Render(d.DeviceCode)

	if m.width >= 90 && m.height >= 32 {
		if qr := renderQRCode(url); qr != "" {
			body += "\n\n" + qr
		}
	}

	body += "\n\n" + m.spin.View() + "  " + subtle.Render("Waiting for authorization...")

	placed := lipgloss.Place(
		maxInt(40, m.width-4), maxInt(12, m.height-8),
		lipgloss.Center, lipgloss.Center,
		activePanelStyle.Width(boxWidth-6).Render(body),
		lipgloss.WithWhitespaceBackground(bg),
	)
	return lipgloss.NewStyle().Width(maxInt(40, m.width-4)).Height(maxInt(12, m.height-8)).Background(bg).Render(placed)
}

func (m model) viewReloginModal() string {
	boxWidth := m.width - 12
	if boxWidth > 76 {
		boxWidth = 76
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
		Width(boxWidth - 14).
		Render(m.reloginInput.View())
	body := titleStyle.Render("// RELOGIN") + "\n\n" +
		subtle.Render("Paste the device code from the SteadIP dashboard:") + "\n\n" +
		inputBox
	if m.err != "" {
		body += "\n\n" + errStyle.Render(m.err)
	}
	body += "\n\n" + subtle.Render("enter » submit  //  esc » cancel")
	modal := activePanelStyle.Width(boxWidth - 6).Render(body)
	placed := lipgloss.Place(
		maxInt(40, m.width-4), maxInt(12, m.height-8),
		lipgloss.Center, lipgloss.Center,
		modal,
		lipgloss.WithWhitespaceBackground(bg),
	)
	return lipgloss.NewStyle().Width(maxInt(40, m.width-4)).Height(maxInt(12, m.height-8)).Background(bg).Render(placed)
}

func statusMini(p Paths) string {
	dot := func(active bool) string {
		if active {
			return lipgloss.NewStyle().Foreground(green).Render("●")
		}
		return lipgloss.NewStyle().Foreground(red).Render("●")
	}

	loggedIn := false
	if t, err := token(p); err == nil && t != "" {
		loggedIn = true
	}
	manual := manualRunning(p)
	auto := autoEnabled(p)
	daemon := daemonActive(p)

	labelSt := lipgloss.NewStyle().Foreground(muted)
	valSt := lipgloss.NewStyle().Foreground(white)

	rows := []struct{ active bool; label, val string }{
		{loggedIn, "Logged in  ", boolStr(loggedIn, "yes", "no")},
		{manual,   "Tunnel     ", boolStr(manual, "running", "stopped")},
		{auto,     "Auto-start ", boolStr(auto, "enabled", "disabled")},
		{daemon,   "Daemon     ", boolStr(daemon, "running", "stopped")},
	}

	var lines []string
	for _, r := range rows {
		lines = append(lines, fmt.Sprintf("%s  %s  %s",
			dot(r.active),
			labelSt.Render(r.label),
			valSt.Render(r.val),
		))
	}
	return strings.Join(lines, "\n")
}

func statusText(p Paths) string {
	labelSt := lipgloss.NewStyle().Foreground(muted)
	return statusMini(p) + "\n\n" +
		labelSt.Render("Dashboard  ") + dashboardURL + "\n" +
		labelSt.Render("Config     ") + p.Config + "\n" +
		labelSt.Render("Logs       ") + p.Log + "\n" +
		labelSt.Render("frpc       ") + p.Frpc
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

// ─── Monitor ─────────────────────────────────────────────────────────────────

type MonitorTunnel struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Domain   string  `json:"subdomain"`
	LastPing string  `json:"last_ping_timestamp"`
	UserID   string  `json:"user_id"`
	Latency    int     // avg of access log latencies, computed in fetch
	Throughput float64 // avg bytes/sec across access log entries, computed in fetch
}

type TrafficData struct {
	Daily   float64 `json:"daily"`
	Monthly float64 `json:"monthly"`
}

type LogEntry struct {
	Data         string `json:"data"`
	CreationTime string `json:"creation_time"`
	Latency      string `json:"latency"`
}

type LogsData struct {
	Access []LogEntry `json:"access"`
	Error  []LogEntry `json:"error"`
}

type monitorSnap struct {
	tunnels    []MonitorTunnel
	daily      float64
	monthly    float64
	accessLogs []LogEntry
	frpcLogs   []LogEntry
	email      string
	region     string
}

type monitorModel struct {
	p              Paths
	tok            string
	width          int
	height         int
	snap           monitorSnap
	loading        bool
	err            string
	spin           spinner.Model
	lastAt         time.Time
	frpcConn       bool
	focusedPane      int // 0 = tunnels, 1 = access logs, 2 = error logs
	tunnelCursor     int // -1 = all tunnels, 0..N-1 = specific tunnel index
	accessScrollUp   int
	accessScrollLeft int
	frpcScrollUp     int
	frpcScrollLeft   int
}

type monitorRefreshMsg struct {
	snap monitorSnap
	err  error
}

type monitorTickMsg struct{}

func newMonitorModel(p Paths, tok string) monitorModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(cyan)
	return monitorModel{
		p:            p,
		tok:          tok,
		spin:         s,
		loading:      true,
		width:        100,
		height:       40,
		frpcConn:     manualRunning(p) || daemonActive(p),
		tunnelCursor: -1,
	}
}

func (m monitorModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, doMonitorFetch(m.p, m.tok, ""))
}

func (m monitorModel) activeTunnelID() string {
	if m.tunnelCursor >= 0 && m.tunnelCursor < len(m.snap.tunnels) {
		return m.snap.tunnels[m.tunnelCursor].ID
	}
	return ""
}

// parseAccessLogThroughput extracts bytes=X and request_time=Y from a log line.
// request_time is expected in seconds (float).
func parseAccessLogThroughput(data string) (bytes float64, reqTime float64, ok bool) {
	var gotBytes, gotTime bool
	for _, field := range strings.Fields(data) {
		if strings.HasPrefix(field, "bytes=") {
			if v, err := strconv.ParseFloat(strings.TrimPrefix(field, "bytes="), 64); err == nil && v >= 0 {
				bytes = v
				gotBytes = true
			}
		} else if strings.HasPrefix(field, "request_time=") {
			if v, err := strconv.ParseFloat(strings.TrimPrefix(field, "request_time="), 64); err == nil && v > 0 {
				reqTime = v
				gotTime = true
			}
		}
	}
	return bytes, reqTime, gotBytes && gotTime
}

func fmtThroughput(bps float64) string {
	switch {
	case bps <= 0:
		return "-"
	case bps >= 1e9:
		return fmt.Sprintf("%.1f GB/s", bps/1e9)
	case bps >= 1e6:
		return fmt.Sprintf("%.1f MB/s", bps/1e6)
	case bps >= 1e3:
		return fmt.Sprintf("%.1f KB/s", bps/1e3)
	default:
		return fmt.Sprintf("%.0f B/s", bps)
	}
}

func doMonitorFetch(p Paths, tok string, tunnelID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		var cfgExt struct {
			FRP     string          `json:"frp"`
			Tunnels json.RawMessage `json:"tunnels,omitempty"`
			Email   string          `json:"email,omitempty"`
			Region  string          `json:"region,omitempty"`
		}
		_, raw, err := getJSON(ctx, "/device/config", tok, &cfgExt)
		if err != nil {
			return monitorRefreshMsg{err: errors.New(apiErr(raw, err.Error()))}
		}

		var tunnels []MonitorTunnel
		if cfgExt.Tunnels != nil {
			_ = json.Unmarshal(cfgExt.Tunnels, &tunnels)
		}

		var totalDaily, totalMonthly float64
		var allAccess, allError []LogEntry

		if len(tunnels) > 0 {
			var tr TrafficData
			if _, _, e := getJSON(ctx, "/device/traffic?user_id="+tunnels[0].UserID, tok, &tr); e == nil {
				totalDaily = tr.Daily
				totalMonthly = tr.Monthly
			}
		}

		for i, t := range tunnels {
			if tunnelID != "" && t.ID != tunnelID {
				continue
			}
			var logs LogsData
			if _, _, e := getJSON(ctx, "/device/logs?tunnel_id="+t.ID, tok, &logs); e == nil {
				allAccess = append(allAccess, logs.Access...)
				allError = append(allError, logs.Error...)
				var latSum, latCount int
				var tpTotalBytes, tpTotalTime float64
				for _, entry := range logs.Access {
					if v, err := strconv.Atoi(strings.TrimSpace(entry.Latency)); err == nil && v > 0 {
						latSum += v
						latCount++
					}
					if b, rt, ok := parseAccessLogThroughput(entry.Data); ok {
						tpTotalBytes += b
						tpTotalTime += rt
					}
				}
				if latCount > 0 {
					tunnels[i].Latency = latSum / latCount
				}
				if tpTotalTime > 0 {
					tunnels[i].Throughput = tpTotalBytes / tpTotalTime
				}
			}
		}

		return monitorRefreshMsg{snap: monitorSnap{
			tunnels:    tunnels,
			daily:      totalDaily,
			monthly:    totalMonthly,
			accessLogs: allAccess,
			frpcLogs:   allError,
			email:      cfgExt.Email,
			region:     cfgExt.Region,
		}}
	}
}

func (m monitorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case tea.KeyMsg:
		switch v.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "r":
			m.loading = true
			return m, doMonitorFetch(m.p, m.tok, m.activeTunnelID())
		case "tab":
			m.focusedPane = (m.focusedPane + 1) % 3
		case "up", "k":
			switch m.focusedPane {
			case 0:
				if m.tunnelCursor > -1 {
					m.tunnelCursor--
					m.accessScrollUp, m.accessScrollLeft = 0, 0
					m.frpcScrollUp, m.frpcScrollLeft = 0, 0
					m.loading = true
					return m, doMonitorFetch(m.p, m.tok, m.activeTunnelID())
				}
			case 1:
				maxUp := len(m.snap.accessLogs) - 1
				if maxUp < 0 {
					maxUp = 0
				}
				if m.accessScrollUp < maxUp {
					m.accessScrollUp++
				}
			case 2:
				maxUp := len(m.snap.frpcLogs) - 1
				if maxUp < 0 {
					maxUp = 0
				}
				if m.frpcScrollUp < maxUp {
					m.frpcScrollUp++
				}
			}
		case "down", "j":
			switch m.focusedPane {
			case 0:
				if m.tunnelCursor < len(m.snap.tunnels)-1 {
					m.tunnelCursor++
					m.accessScrollUp, m.accessScrollLeft = 0, 0
					m.frpcScrollUp, m.frpcScrollLeft = 0, 0
					m.loading = true
					return m, doMonitorFetch(m.p, m.tok, m.activeTunnelID())
				}
			case 1:
				if m.accessScrollUp > 0 {
					m.accessScrollUp--
				}
			case 2:
				if m.frpcScrollUp > 0 {
					m.frpcScrollUp--
				}
			}
		case "left":
			if m.focusedPane == 1 && m.accessScrollLeft > 0 {
				m.accessScrollLeft--
			} else if m.focusedPane == 2 && m.frpcScrollLeft > 0 {
				m.frpcScrollLeft--
			}
		case "right":
			if m.focusedPane == 1 {
				m.accessScrollLeft++
			} else if m.focusedPane == 2 {
				m.frpcScrollLeft++
			}
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(v)
		return m, cmd
	case monitorTickMsg:
		m.loading = true
		return m, doMonitorFetch(m.p, m.tok, m.activeTunnelID())
	case monitorRefreshMsg:
		m.loading = false
		if v.err != nil {
			m.err = v.err.Error()
		} else {
			m.snap = v.snap
			m.err = ""
			m.lastAt = time.Now()
			m.frpcConn = manualRunning(m.p) || daemonActive(m.p)
		}
		return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return monitorTickMsg{} })
	}
	return m, nil
}

// mPad right-pads s to n visible columns (ANSI-aware).
func mPad(s string, n int) string {
	vis := lipgloss.Width(s)
	if vis >= n {
		return s
	}
	return s + strings.Repeat(" ", n-vis)
}

// mTrunc truncates s to n runes, adding ellipsis if needed.
func mTrunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func mTop(w int, label string) string {
	n := w - 5 - lipgloss.Width(label)
	if n < 1 {
		n = 1
	}
	styledLabel := lipgloss.NewStyle().Foreground(neonPink).Bold(true).Render(label)
	return monBorderSt.Render("┌─ ") + styledLabel + monBorderSt.Render(" "+strings.Repeat("─", n)+"┐")
}

func mSep(w int, label string) string {
	n := w - 5 - lipgloss.Width(label)
	if n < 1 {
		n = 1
	}
	styledLabel := lipgloss.NewStyle().Foreground(muted).Bold(true).Render(label)
	return monBorderSt.Render("├─ ") + styledLabel + monBorderSt.Render(" "+strings.Repeat("─", n)+"┤")
}

func mRow(w int, content string) string {
	vis := lipgloss.Width(content)
	pad := w - 4 - vis
	if pad < 0 {
		pad = 0
	}
	return monBorderSt.Render("│") + " " + content + strings.Repeat(" ", pad) + " " + monBorderSt.Render("│")
}

// mSepPane is like mSep but highlights the label in cyan when focused.
func mSepPane(w int, label string, focused bool) string {
	n := w - 5 - lipgloss.Width(label)
	if n < 1 {
		n = 1
	}
	renderedLabel := lipgloss.NewStyle().Foreground(dimText).Render(label)
	if focused {
		renderedLabel = lipgloss.NewStyle().Foreground(cyan).Bold(true).Render(label)
	}
	return monBorderSt.Render("├─ ") + renderedLabel + monBorderSt.Render(" "+strings.Repeat("─", n)+"┤")
}

func mBot(w int) string {
	return monBorderSt.Render("└" + strings.Repeat("─", w-2) + "┘")
}

// mRowSB is like mRow but reserves the last interior column for a scrollbar char.
func mRowSB(w int, content, sbChar string) string {
	vis := lipgloss.Width(content)
	pad := w - 4 - vis
	if pad < 0 {
		pad = 0
	}
	return monBorderSt.Render("│") + " " + content + strings.Repeat(" ", pad) + sbChar + monBorderSt.Render("│")
}

// makeScrollbar returns `visible` single-char strings representing a scrollbar.
// scrollUp = 0 means showing the bottom (newest); higher = scrolled toward oldest.
func makeScrollbar(total, visible, scrollUp int) []string {
	track := lipgloss.NewStyle().Foreground(dimText).Render("░")
	thumb := lipgloss.NewStyle().Foreground(muted).Render("█")
	bar := make([]string, visible)
	for i := range bar {
		bar[i] = " "
	}
	if total <= visible {
		return bar
	}
	maxUp := total - visible
	thumbH := visible * visible / total
	if thumbH < 1 {
		thumbH = 1
	}
	trackH := visible - thumbH
	thumbTop := trackH
	if maxUp > 0 {
		thumbTop = trackH - scrollUp*trackH/maxUp
	}
	if thumbTop < 0 {
		thumbTop = 0
	}
	if thumbTop > trackH {
		thumbTop = trackH
	}
	for i := range bar {
		if i >= thumbTop && i < thumbTop+thumbH {
			bar[i] = thumb
		} else {
			bar[i] = track
		}
	}
	return bar
}

func fmtLogTime(s string) string {
	if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
		return time.Unix(n, 0).Format("15:04:05")
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05.999999999Z07:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("15:04:05")
		}
	}
	return s
}

func fmtLogEntry(e LogEntry, maxWidth int) string {
	ts := fmtLogTime(e.CreationTime)
	data := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, e.Data)
	return mTrunc(ts+"  "+data, maxWidth)
}

// fmtLogEntryRaw returns the full untruncated log entry string.
func fmtLogEntryRaw(e LogEntry) string {
	ts := fmtLogTime(e.CreationTime)
	data := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, e.Data)
	return ts + "  " + data
}

// hClip clips a plain (no ANSI) string to visibleW runes starting at scrollLeft.
func hClip(s string, scrollLeft, visibleW int) string {
	r := []rune(s)
	if scrollLeft >= len(r) {
		return ""
	}
	end := scrollLeft + visibleW
	if end > len(r) {
		end = len(r)
	}
	return string(r[scrollLeft:end])
}

// makeHScrollbar returns a horizontal scrollbar string of exactly visibleW rendered chars.
func makeHScrollbar(totalW, visibleW, scrollLeft int) string {
	if totalW <= visibleW {
		return strings.Repeat(" ", visibleW)
	}
	maxLeft := totalW - visibleW
	if scrollLeft > maxLeft {
		scrollLeft = maxLeft
	}
	thumbW := visibleW * visibleW / totalW
	if thumbW < 1 {
		thumbW = 1
	}
	trackW := visibleW - thumbW
	thumbStart := 0
	if maxLeft > 0 {
		thumbStart = scrollLeft * trackW / maxLeft
	}
	if thumbStart > trackW {
		thumbStart = trackW
	}
	track := lipgloss.NewStyle().Foreground(dimText).Render("░")
	thumb := lipgloss.NewStyle().Foreground(muted).Render("█")
	var sb strings.Builder
	for i := 0; i < visibleW; i++ {
		if i >= thumbStart && i < thumbStart+thumbW {
			sb.WriteString(thumb)
		} else {
			sb.WriteString(track)
		}
	}
	return sb.String()
}

func fmtBytes(b float64) string {
	switch {
	case b >= 1e12:
		return fmt.Sprintf("%.1f TB", b/1e12)
	case b >= 1e9:
		return fmt.Sprintf("%.1f GB", b/1e9)
	case b >= 1e6:
		return fmt.Sprintf("%.1f MB", b/1e6)
	case b >= 1e3:
		return fmt.Sprintf("%.1f KB", b/1e3)
	default:
		return fmt.Sprintf("%.0f B", b)
	}
}

func (m monitorModel) View() string {
	w := m.width
	if w < 60 {
		w = 60
	}
	if w > 220 {
		w = 220
	}
	contentW := w - 4

	whiteB := lipgloss.NewStyle().Foreground(white).Bold(true)

	var out []string

	// Title bar
	out = append(out, mTop(w, "// STEADIP MONITOR //"))

	// Info / header row
	email := m.snap.email
	if email == "" {
		email = "–"
	}
	region := m.snap.region
	if region == "" {
		region = "–"
	}
	connTxt := lipgloss.NewStyle().Foreground(red).Bold(true).Render("◆ disconnected")
	if m.frpcConn {
		connTxt = lipgloss.NewStyle().Foreground(green).Bold(true).Render("◆ connected")
	}
	loadTxt := ""
	if m.loading {
		loadTxt = "  " + m.spin.View()
	}
	infoRow := subtle.Render("User: ") + lipgloss.NewStyle().Foreground(white).Render(email) +
		"   " + subtle.Render("Region: ") + lipgloss.NewStyle().Foreground(white).Render(region) +
		"   " + subtle.Render("frpc: ") + connTxt + loadTxt
	out = append(out, mRow(w, infoRow))

	// Tunnels section
	out = append(out, mSepPane(w, "TUNNELS", m.focusedPane == 0))

	const (
		nameW       = 18
		typeW       = 6
		statusW     = 10
		latW        = 8
		throughputW = 10
		cursorW     = 2
	)
	// 5 spaces separate the 6 columns, plus cursor prefix column
	domainW := contentW - cursorW - nameW - typeW - statusW - latW - throughputW - 5
	if domainW < 10 {
		domainW = 10
	}

	colHdr := strings.Repeat(" ", cursorW) +
		mPad(subtle.Render("NAME"), nameW) + " " +
		mPad(subtle.Render("TYPE"), typeW) + " " +
		mPad(subtle.Render("DOMAIN"), domainW) + " " +
		mPad(subtle.Render("STATUS"), statusW) + " " +
		mPad(subtle.Render("LATENCY"), latW) + " " +
		subtle.Render("THROUGHPUT")
	out = append(out, mRow(w, colHdr))

	// "ALL TUNNELS" virtual row
	{
		cur := "  "
		label := subtle.Render("ALL TUNNELS")
		if m.tunnelCursor == -1 {
			cur = lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("▶ ")
			label = lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("ALL TUNNELS")
		}
		out = append(out, mRow(w, cur+label))
	}

	if len(m.snap.tunnels) == 0 {
		out = append(out, mRow(w, strings.Repeat(" ", cursorW)+subtle.Render("no tunnels found")))
	}
	for i, t := range m.snap.tunnels {
		ts, _ := strconv.ParseInt(strings.TrimSpace(t.LastPing), 10, 64)
		isUp := ts > 0 && time.Now().Unix()-ts <= 30
		st := "OFFLINE"
		if isUp {
			st = "ONLINE"
		}
		stColor := lipgloss.Color("#F87171")
		if isUp {
			stColor = lipgloss.Color("#4ADE80")
		}
		dotSt := lipgloss.NewStyle().Foreground(stColor).Render("●")
		stSt := lipgloss.NewStyle().Foreground(stColor).Bold(true).Render(st)

		lat := "-"
		if t.Latency > 0 {
			lat = fmt.Sprintf("%dms", t.Latency)
		}

		cur := "  "
		if m.tunnelCursor == i {
			cur = lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("▶ ")
		}

		row := cur +
			mPad(mTrunc(t.Name, nameW-1), nameW) + " " +
			mPad(mTrunc(t.Type, typeW-1), typeW) + " " +
			mPad(mTrunc(t.Domain+".steadip.com", domainW-1), domainW) + " " +
			mPad(dotSt+" "+stSt, statusW) + " " +
			mPad(lat, latW) + " " +
			fmtThroughput(t.Throughput)
		out = append(out, mRow(w, row))
	}

	// Traffic section
	out = append(out, mSep(w, "TRAFFIC"))
	trafficRow := subtle.Render("Today: ") + whiteB.Render(fmtBytes(m.snap.daily)) +
		"      " +
		subtle.Render("Month: ") + whiteB.Render(fmtBytes(m.snap.monthly))
	out = append(out, mRow(w, trafficRow))

	// Use the actual line count so far to compute remaining space exactly.
	// Still to emit: ACCESS_SEP(1) + access rows + access hscroll(1) + FRPC_SEP(1) + frpc rows + frpc hscroll(1) + BOTTOM(1) + FOOTER(1) = 6 + rows
	h := m.height
	if h < 10 {
		h = 10
	}
	remaining := h - len(out) - 6
	if remaining < 2 {
		remaining = 2
	}
	maxAccessLines := remaining / 2
	maxFrpcLines := remaining - maxAccessLines

	// Access logs – scroll-aware with vertical + horizontal scrollbars
	aAll := m.snap.accessLogs
	aTotal := len(aAll)
	aScrollUp := m.accessScrollUp
	aMaxUp := aTotal - maxAccessLines
	if aMaxUp < 0 {
		aMaxUp = 0
	}
	if aScrollUp > aMaxUp {
		aScrollUp = aMaxUp
	}
	aStart := aTotal - maxAccessLines - aScrollUp
	if aStart < 0 {
		aStart = 0
	}
	aEnd := aTotal - aScrollUp
	if aEnd < 0 {
		aEnd = 0
	}
	aVisible := aAll[aStart:aEnd]
	aSB := makeScrollbar(aTotal, maxAccessLines, aScrollUp)

	// Compute max natural content width for horizontal scrollbar.
	aViewW := contentW - 2 // 1 for vertical SB, 1 spare
	aMaxW := aViewW
	for _, e := range aAll {
		if rw := len([]rune(fmtLogEntryRaw(e))); rw > aMaxW {
			aMaxW = rw
		}
	}
	aScrollLeft := m.accessScrollLeft
	if aScrollLeft > aMaxW-aViewW {
		aScrollLeft = aMaxW - aViewW
	}
	if aScrollLeft < 0 {
		aScrollLeft = 0
	}

	aLabel := "ACCESS LOGS"
	if aScrollUp > 0 {
		aLabel += fmt.Sprintf(" [↑%d]", aScrollUp)
	}
	out = append(out, mSepPane(w, aLabel, m.focusedPane == 1))
	for i := 0; i < maxAccessLines; i++ {
		sb := aSB[i]
		switch {
		case len(aVisible) == 0 && i == 0:
			out = append(out, mRowSB(w, subtle.Render("  no access logs"), sb))
		case i < len(aVisible):
			out = append(out, mRowSB(w, hClip(fmtLogEntryRaw(aVisible[i]), aScrollLeft, aViewW), sb))
		default:
			out = append(out, mRowSB(w, "", sb))
		}
	}
	out = append(out, mRow(w, makeHScrollbar(aMaxW, contentW-2, aScrollLeft)))

	// Error logs – scroll-aware with vertical + horizontal scrollbars
	fAll := m.snap.frpcLogs
	fTotal := len(fAll)
	fScrollUp := m.frpcScrollUp
	fMaxUp := fTotal - maxFrpcLines
	if fMaxUp < 0 {
		fMaxUp = 0
	}
	if fScrollUp > fMaxUp {
		fScrollUp = fMaxUp
	}
	fStart := fTotal - maxFrpcLines - fScrollUp
	if fStart < 0 {
		fStart = 0
	}
	fEnd := fTotal - fScrollUp
	if fEnd < 0 {
		fEnd = 0
	}
	fVisible := fAll[fStart:fEnd]
	fSB := makeScrollbar(fTotal, maxFrpcLines, fScrollUp)

	// Compute max natural content width for horizontal scrollbar.
	fViewW := contentW - 2
	fMaxW := fViewW
	for _, e := range fAll {
		if rw := len([]rune(fmtLogEntryRaw(e))); rw > fMaxW {
			fMaxW = rw
		}
	}
	fScrollLeft := m.frpcScrollLeft
	if fScrollLeft > fMaxW-fViewW {
		fScrollLeft = fMaxW - fViewW
	}
	if fScrollLeft < 0 {
		fScrollLeft = 0
	}

	fLabel := "ERROR LOGS"
	if fScrollUp > 0 {
		fLabel += fmt.Sprintf(" [↑%d]", fScrollUp)
	}
	out = append(out, mSepPane(w, fLabel, m.focusedPane == 2))
	for i := 0; i < maxFrpcLines; i++ {
		sb := fSB[i]
		switch {
		case len(fVisible) == 0 && i == 0:
			out = append(out, mRowSB(w, subtle.Render("  no error logs"), sb))
		case i < len(fVisible):
			out = append(out, mRowSB(w, subtle.Render(hClip(fmtLogEntryRaw(fVisible[i]), fScrollLeft, fViewW)), sb))
		default:
			out = append(out, mRowSB(w, "", sb))
		}
	}
	out = append(out, mRow(w, makeHScrollbar(fMaxW, contentW-2, fScrollLeft)))

	// Bottom border
	out = append(out, mBot(w))

	// Footer
	refreshInfo := ""
	if !m.lastAt.IsZero() {
		refreshInfo = "  refreshed " + m.lastAt.Format("15:04:05")
	}
	if m.err != "" {
		refreshInfo = "  " + errStyle.Render(m.err)
	}
	paneNames := [3]string{"TUNNELS", "ACCESS LOGS", "ERROR LOGS"}
	scrollHint := "↑↓ · scroll    ←→ · h-scroll"
	if m.focusedPane == 0 {
		scrollHint = "↑↓ · select tunnel"
	}
	out = append(out, subtle.Render("q » quit  //  r » refresh  //  tab » switch pane  //  "+scrollHint+"  ["+paneNames[m.focusedPane]+"]")+subtle.Render(refreshInfo))

	return lipgloss.NewStyle().Background(bg).Width(w).Render(strings.Join(out, "\n"))
}

func cliMonitor(p Paths) int {
	tok, err := requireToken(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, errStyle.Render("Error:"), err)
		return 1
	}
	prog := tea.NewProgram(newMonitorModel(p, tok), tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
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
	case "monitor":
		return cliMonitor(p)
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
  steadip monitor         Live tunnel monitor TUI
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
	final, err := tea.NewProgram(newModel(p), tea.WithAltScreen()).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if tm, ok := final.(model); ok && tm.launchMonitor {
		os.Exit(cliMonitor(p))
	}
}
