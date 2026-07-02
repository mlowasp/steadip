#!/usr/bin/env sh
set -eu
APP_NAME="steadip"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
CONFIG_DIR="${CONFIG_DIR:-$HOME/.config/steadip}"
STATE_DIR="${STATE_DIR:-$HOME/.local/state/steadip}"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin/steadip}"
FRP_VERSION="${FRP_VERSION:-0.61.1}"
need_cmd(){ command -v "$1" >/dev/null 2>&1 || { echo "Missing required command: $1" >&2; exit 1; }; }
detect_os(){ os="$(uname -s | tr '[:upper:]' '[:lower:]')"; case "$os" in linux) echo "linux" ;; *) echo "Unsupported OS for this installer: $os" >&2; echo "Use install-macos.sh on macOS or install-windows.ps1 on Windows." >&2; exit 1;; esac; }
detect_arch(){ arch="$(uname -m)"; case "$arch" in x86_64|amd64) echo "amd64";; aarch64|arm64) echo "arm64";; armv7l|armv7) echo "arm";; *) echo "Unsupported architecture: $arch" >&2; exit 1;; esac; }
download(){ url="$1"; output="$2"; if command -v curl >/dev/null 2>&1; then curl -fsSL "$url" -o "$output"; elif command -v wget >/dev/null 2>&1; then wget -q "$url" -O "$output"; else echo "Missing curl or wget." >&2; exit 1; fi; }
install_frpc(){ os="$(detect_os)"; arch="$(detect_arch)"; archive="frp_${FRP_VERSION}_${os}_${arch}.tar.gz"; url="https://github.com/fatedier/frp/releases/download/v${FRP_VERSION}/${archive}"; mkdir -p "$BIN_DIR"; tmpdir="$(mktemp -d)"; trap 'rm -rf "$tmpdir"' EXIT INT TERM; echo "Downloading frpc v${FRP_VERSION} for ${os}/${arch}..."; download "$url" "$tmpdir/$archive"; tar -xzf "$tmpdir/$archive" -C "$tmpdir"; extracted_dir="$(find "$tmpdir" -type d -name "frp_${FRP_VERSION}_${os}_${arch}" | head -n 1)"; [ -n "$extracted_dir" ] && [ -f "$extracted_dir/frpc" ] || { echo "Could not find frpc in downloaded archive." >&2; exit 1; }; cp "$extracted_dir/frpc" "$BIN_DIR/frpc"; chmod 755 "$BIN_DIR/frpc"; echo "Installed frpc to $BIN_DIR/frpc"; }
install_steadip_cli(){ mkdir -p "$CONFIG_DIR" "$STATE_DIR" "$BIN_DIR"; wrapper="$BIN_DIR/steadip"; cat > "$wrapper" <<'STEADIP_CLI'
#!/usr/bin/env sh
set -eu
APP_NAME="steadip"
CONFIG_DIR="${STEADIP_CONFIG_DIR:-$HOME/.config/steadip}"
STATE_DIR="${STEADIP_STATE_DIR:-$HOME/.local/state/steadip}"
BIN_DIR="${STEADIP_BIN_DIR:-$HOME/.local/bin/steadip}"
FRPC="$BIN_DIR/frpc"
TOKEN_FILE="$CONFIG_DIR/token"
CONFIG_FILE="$CONFIG_DIR/frpc.toml"
META_FILE="$CONFIG_DIR/tunnels.json"
PID_FILE="$STATE_DIR/frpc.pid"
LOG_FILE="$STATE_DIR/frpc.log"
STEADIP_API="https://steadip.com/api"
STEADIP_DASHBOARD="https://steadip.com"
SYSTEMD_USER_DIR="$HOME/.config/systemd/user"
SYSTEMD_SERVICE_FILE="$SYSTEMD_USER_DIR/steadip.service"
mkdir -p "$CONFIG_DIR" "$STATE_DIR"
usage(){ cat <<USAGE
SteadIP CLI

Usage:
  steadip login
  steadip relogin
  steadip sync
  steadip up
  steadip down
  steadip enable
  steadip disable
  steadip status
  steadip logs
  steadip config
  steadip logout
  steadip uninstall

Commands:
  login      Sign in using browser/device-code login
  relogin    Sign in using a device code generated in the webapp
  sync       Fetch dashboard tunnel config and write frpc.toml
  up         Sync and start tunnels now
  down       Stop running tunnels now, without changing auto-start setting
  enable     Enable and start auto-start daemon
  disable    Stop and disable auto-start daemon
  status     Show tunnel and daemon status
  logs       Show frpc logs
  config     Print local frpc config with secrets hidden
  logout     Stop tunnels and remove local login token
  uninstall  Remove SteadIP client files

Dashboard:
  Configure tunnels at https://steadip.com
USAGE
}
require_php_for_json(){ command -v php >/dev/null 2>&1 || { echo "Missing php-cli. Install php-cli before using steadip." >&2; exit 1; }; }
http_post_json(){
  url="$1"; data="$2"; auth="${3:-}"
  if command -v curl >/dev/null 2>&1; then
    if [ -n "$auth" ]; then curl -fsSL -X POST "$url" -H 'Content-Type: application/json' -H "Authorization: Bearer $auth" --data "$data"; else curl -fsSL -X POST "$url" -H 'Content-Type: application/json' --data "$data"; fi
  elif command -v wget >/dev/null 2>&1; then
    tmp="$(mktemp)"; printf "%s" "$data" > "$tmp"
    if [ -n "$auth" ]; then wget -qO- --header='Content-Type: application/json' --header="Authorization: Bearer $auth" --post-file="$tmp" "$url"; else wget -qO- --header='Content-Type: application/json' --post-file="$tmp" "$url"; fi
    rm -f "$tmp"
  else echo "Missing curl or wget." >&2; exit 1; fi
}
http_post_json_with_status(){
  url="$1"; data="$2"; auth="${3:-}"; tmp_body="$(mktemp)"
  command -v curl >/dev/null 2>&1 || { echo "Missing curl." >&2; rm -f "$tmp_body"; exit 1; }
  if [ -n "$auth" ]; then
    http_code="$(curl -sS -o "$tmp_body" -w '%{http_code}' -X POST "$url" -H 'Content-Type: application/json' -H "Authorization: Bearer $auth" --data "$data")"
  else
    http_code="$(curl -sS -o "$tmp_body" -w '%{http_code}' -X POST "$url" -H 'Content-Type: application/json' --data "$data")"
  fi
  body="$(cat "$tmp_body")"; rm -f "$tmp_body"; printf "%s\n%s" "$http_code" "$body"
}
http_get_json(){
  url="$1"; auth="${2:-}"
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$url" -H 'Accept: application/json' ${auth:+-H "Authorization: Bearer $auth"};
  elif command -v wget >/dev/null 2>&1; then wget -qO- --header='Accept: application/json' ${auth:+--header="Authorization: Bearer $auth"} "$url";
  else echo "Missing curl or wget." >&2; exit 1; fi
}
json_get_string(){ key="$1"; php -r '$key=$argv[1];$json=stream_get_contents(STDIN);$data=json_decode($json,true);if(!is_array($data))exit(1);$v=$data;foreach(explode(".",$key) as $p){if(!is_array($v)||!array_key_exists($p,$v))exit(1);$v=$v[$p];}if($v===null)exit(1);if(is_bool($v)){echo $v?"true":"false";exit;}if(is_scalar($v)){echo $v;exit;}echo json_encode($v);' "$key"; }
json_write_frpc_config(){
  input_file="$1"; output_file="$2"
  php -r '$d=json_decode(file_get_contents($argv[1]),true); if(!is_array($d)){fwrite(STDERR,"Invalid config JSON returned by SteadIP API.\n"); exit(1);} $frp=$d["frp"]??""; if(!is_string($frp)||trim($frp)===""){fwrite(STDERR,"No frp config returned by SteadIP API.\n"); exit(2);} file_put_contents($argv[2],$frp);' "$input_file" "$output_file"
}
get_token(){ [ -f "$TOKEN_FILE" ] && cat "$TOKEN_FILE" || echo ""; }
save_token(){ umask 077; mkdir -p "$CONFIG_DIR"; printf "%s" "$1" > "$TOKEN_FILE"; chmod 600 "$TOKEN_FILE" 2>/dev/null || true; }
require_login(){ token="$(get_token)"; [ -n "$token" ] || { echo "You are not logged in. Run: steadip login" >&2; exit 1; }; echo "$token"; }
is_manual_running(){ [ -f "$PID_FILE" ] || return 1; pid="$(cat "$PID_FILE" 2>/dev/null || true)"; [ -n "$pid" ] || return 1; kill -0 "$pid" >/dev/null 2>&1; }
open_browser(){ url="$1"; if command -v xdg-open >/dev/null 2>&1; then xdg-open "$url" >/dev/null 2>&1 || true; elif command -v open >/dev/null 2>&1; then open "$url" >/dev/null 2>&1 || true; else echo "$url"; fi; }
device_name(){ hostname 2>/dev/null || echo unknown; }
cmd_login(){
  require_php_for_json; device="$(device_name)"; payload="{\"client_name\":\"steadip-cli\",\"client_version\":\"0.2.1\",\"device_name\":\"$device\"}"
  echo "Requesting SteadIP device login..."; response="$(http_post_json "$STEADIP_API/device/code" "$payload")"
  device_code="$(printf "%s" "$response" | json_get_string device_code)"; user_code="$(printf "%s" "$response" | json_get_string user_code)"; verification_uri="$(printf "%s" "$response" | json_get_string verification_uri)"; verification_uri_complete="$(printf "%s" "$response" | json_get_string verification_uri_complete || true)"; interval="$(printf "%s" "$response" | json_get_string interval || echo 5)"; expires_in="$(printf "%s" "$response" | json_get_string expires_in || echo 600)"
  echo; echo "SteadIP CLI login"; echo; echo "Open this page:"; echo "  $verification_uri"; echo; echo "Enter code:"; echo "  $device_code"; echo
  [ -n "$verification_uri_complete" ] && open_browser "$verification_uri_complete"
  echo "Waiting for authorization..."; echo; start_time="$(date +%s)"
  while :; do
    now="$(date +%s)"; elapsed=$((now-start_time)); [ "$elapsed" -lt "$expires_in" ] || { echo "Login expired. Run 'steadip login' again." >&2; exit 1; }
    sleep "$interval"; token_payload="{\"device_code\":\"$device_code\",\"user_code\":\"$user_code\"}"
    result="$(http_post_json_with_status "$STEADIP_API/device/token" "$token_payload")"; http_code="$(printf "%s" "$result" | sed -n '1p')"; token_response="$(printf "%s" "$result" | sed '1d')"
    if [ "$http_code" != "200" ]; then
      error="$(printf "%s" "$token_response" | json_get_string error 2>/dev/null || true)"
      [ "$error" = "authorization_pending" ] && { printf "."; continue; }
      [ "$error" = "slow_down" ] && { interval=$((interval+5)); printf "."; continue; }
      [ "$error" = "tunnels_limit_reached" ] && { echo; echo "Maximum number of tunnels reached. Delete an existing tunnel from your SteadIP dashboard, then try again." >&2; exit 1; }
      [ "$error" = "expired_token" ] && { echo; echo "Login expired." >&2; exit 1; }
      [ "$error" = "access_denied" ] && { echo; echo "Login was denied." >&2; exit 1; }
      [ "$error" = "no_device_code" ] && { echo; echo "Device code was lost in transport." >&2; exit 1; }
      echo; echo "Login failed: ${error:-HTTP $http_code}" >&2; exit 1
    fi
    access_token="$(printf "%s" "$token_response" | json_get_string access_token)"; email="$(printf "%s" "$token_response" | json_get_string user_email || echo "")"; verified="$(printf "%s" "$token_response" | json_get_string user_verified || echo false)"
    save_token "$access_token"; echo; echo; echo "Logged in successfully."; [ -n "$email" ] && echo "Account: $email"; [ "$verified" = true ] && echo "Plan: Verified" || echo "Plan: Free"; echo; echo "Configure tunnels in your dashboard:"; echo "  $STEADIP_DASHBOARD"; echo; echo "Then run:"; echo "  steadip up"; echo; exit 0
  done
}
cmd_relogin(){
  require_php_for_json
  printf "Enter device code from SteadIP webapp: "
  read device_code
  device_code="$(printf "%s" "$device_code" | tr -d '[:space:]')"
  [ -n "$device_code" ] || { echo "Missing device code." >&2; exit 1; }
  device="$(device_name)"
  payload="{\"device_code\":\"$device_code\",\"relogin\":true,\"client_name\":\"steadip-cli\",\"client_version\":\"0.2.2\",\"device_name\":\"$device\"}"
  echo "Authorizing this device with SteadIP..."
  result="$(http_post_json_with_status "$STEADIP_API/device/token" "$payload")"
  http_code="$(printf "%s" "$result" | sed -n '1p')"
  token_response="$(printf "%s" "$result" | sed '1d')"
  if [ "$http_code" != "200" ]; then
    error="$(printf "%s" "$token_response" | json_get_string error 2>/dev/null || true)"
    [ "$error" = "tunnels_limit_reached" ] && { echo "Maximum number of tunnels reached. Delete an existing tunnel from your SteadIP dashboard, then try again." >&2; exit 1; }
    [ "$error" = "expired_token" ] && { echo "Device code expired. Generate a new one from the SteadIP dashboard." >&2; exit 1; }
    [ "$error" = "access_denied" ] && { echo "Device code was denied." >&2; exit 1; }
    [ "$error" = "invalid_device_code" ] && { echo "Invalid device code." >&2; exit 1; }
    [ "$error" = "no_device_code" ] && { echo "Missing device code." >&2; exit 1; }
    echo "Relogin failed: ${error:-HTTP $http_code}" >&2
    exit 1
  fi
  access_token="$(printf "%s" "$token_response" | json_get_string access_token)"
  email="$(printf "%s" "$token_response" | json_get_string user_email || echo "")"
  verified="$(printf "%s" "$token_response" | json_get_string user_verified || echo false)"
  save_token "$access_token"
  rm -f "$CONFIG_FILE" "$META_FILE" 2>/dev/null || true
  echo
  echo "Relogin successful."
  [ -n "$email" ] && echo "Account: $email"
  [ "$verified" = true ] && echo "Plan: Verified" || echo "Plan: Free"
  echo
  echo "Run: steadip up"
}
cmd_sync(){ require_php_for_json; token="$(require_login)"; echo "Fetching SteadIP tunnel config..."; response="$(http_get_json "$STEADIP_API/device/config" "$token")"; umask 077; printf "%s" "$response" > "$META_FILE"; chmod 600 "$META_FILE" 2>/dev/null || true; set +e; json_write_frpc_config "$META_FILE" "$CONFIG_FILE"; code="$?"; set -e; [ "$code" -eq 2 ] && exit 2; [ "$code" -eq 0 ] || { echo "Could not write frpc config." >&2; exit "$code"; }; chmod 600 "$CONFIG_FILE" 2>/dev/null || true; echo "Config written:"; echo "  $CONFIG_FILE"; }
start_manual_frpc(){ [ -x "$FRPC" ] || { echo "frpc is missing: $FRPC" >&2; exit 1; }; [ -f "$CONFIG_FILE" ] || { echo "No frpc config found. Run: steadip sync" >&2; exit 1; }; if is_manual_running; then old_pid="$(cat "$PID_FILE")"; kill "$old_pid" >/dev/null 2>&1 || true; sleep 1; fi; echo; echo "Starting SteadIP tunnels..."; nohup "$FRPC" -c "$CONFIG_FILE" > "$LOG_FILE" 2>&1 & echo "$!" > "$PID_FILE"; sleep 1; if is_manual_running; then echo "Started."; echo; echo "Logs: $LOG_FILE"; else echo "frpc failed to start. Logs:" >&2; tail -n 120 "$LOG_FILE" >&2 || true; exit 1; fi; }
cmd_up(){ cmd_sync; if is_daemon_active; then echo; echo "SteadIP daemon is running. Restarting with latest config..."; restart_daemon; echo "Restarted."; return; fi; start_manual_frpc; }
cmd_down(){ stopped=false; if is_manual_running; then pid="$(cat "$PID_FILE")"; kill "$pid" >/dev/null 2>&1 || true; sleep 1; kill -0 "$pid" >/dev/null 2>&1 && kill -9 "$pid" >/dev/null 2>&1 || true; rm -f "$PID_FILE"; echo "Stopped manually started SteadIP tunnel."; stopped=true; else rm -f "$PID_FILE"; fi; if is_daemon_active; then stop_daemon; echo "Stopped SteadIP daemon."; stopped=true; fi; [ "$stopped" = false ] && echo "SteadIP tunnel is not running."; }
cmd_daemon(){ require_php_for_json; token="$(require_login)"; echo "Fetching SteadIP tunnel config..."; response="$(http_get_json "$STEADIP_API/device/config" "$token")"; umask 077; printf "%s" "$response" > "$META_FILE"; chmod 600 "$META_FILE" 2>/dev/null || true; json_write_frpc_config "$META_FILE" "$CONFIG_FILE"; chmod 600 "$CONFIG_FILE" 2>/dev/null || true; echo "Starting frpc in daemon mode..."; exec "$FRPC" -c "$CONFIG_FILE"; }
is_daemon_active(){ command -v systemctl >/dev/null 2>&1 && systemctl --user is-active steadip.service >/dev/null 2>&1; }
restart_daemon(){ systemctl --user restart steadip.service; }
stop_daemon(){ systemctl --user stop steadip.service >/dev/null 2>&1 || true; }
disable_autostart(){ command -v systemctl >/dev/null 2>&1 && systemctl --user disable --now steadip.service >/dev/null 2>&1 || true; rm -f "$SYSTEMD_SERVICE_FILE"; command -v systemctl >/dev/null 2>&1 && systemctl --user daemon-reload >/dev/null 2>&1 || true; }
cmd_enable(){ command -v systemctl >/dev/null 2>&1 || { echo "systemd/systemctl not found. Auto-start is only supported on systemd Linux." >&2; exit 1; }; systemctl --user status >/dev/null 2>&1 || { echo "systemd user service is not available in this shell/session." >&2; exit 1; }; require_login >/dev/null; mkdir -p "$SYSTEMD_USER_DIR"; cat > "$SYSTEMD_SERVICE_FILE" <<SERVICE
[Unit]
Description=SteadIP Tunnel Client
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$BIN_DIR/steadip daemon
Restart=always
RestartSec=5
Environment=STEADIP_DAEMON=1

[Install]
WantedBy=default.target
SERVICE
systemctl --user daemon-reload; systemctl --user enable --now steadip.service; echo "SteadIP auto-start enabled and started."; echo "Service file: $SYSTEMD_SERVICE_FILE"; }
cmd_disable(){ disable_autostart; echo "SteadIP auto-start disabled."; }
status_daemon(){ if command -v systemctl >/dev/null 2>&1 && systemctl --user status >/dev/null 2>&1; then systemctl --user is-enabled steadip.service >/dev/null 2>&1 && echo "Auto-start: enabled" || echo "Auto-start: disabled"; systemctl --user is-active steadip.service >/dev/null 2>&1 && echo "Daemon: running" || echo "Daemon: stopped"; else echo "Auto-start: unavailable"; echo "Daemon: unavailable"; fi; }
logs_daemon(){ if command -v systemctl >/dev/null 2>&1 && systemctl --user is-active steadip.service >/dev/null 2>&1; then journalctl --user -u steadip.service -f; exit 0; fi; return 1; }
cmd_status(){ if is_manual_running; then echo "Manual tunnel: running"; echo "Manual PID: $(cat "$PID_FILE")"; else echo "Manual tunnel: stopped"; fi; status_daemon; [ -f "$CONFIG_FILE" ] && echo "Config: $CONFIG_FILE"; [ -f "$LOG_FILE" ] && echo "Manual logs: $LOG_FILE"; }
cmd_logs(){ logs_daemon || true; [ -f "$LOG_FILE" ] || { echo "No manual logs yet."; exit 0; }; tail -n 120 -f "$LOG_FILE"; }
cmd_config(){ [ -f "$CONFIG_FILE" ] || { echo "No config found."; exit 0; }; sed -e 's/connection_token = ".*/connection_token = "***"/' -e "s/connection_token = '.*/connection_token = '***'/" "$CONFIG_FILE"; }
cmd_logout(){ cmd_down >/dev/null 2>&1 || true; rm -f "$TOKEN_FILE"; echo "Logged out."; }
cmd_uninstall(){ cmd_down >/dev/null 2>&1 || true; disable_autostart >/dev/null 2>&1 || true; echo "This will remove:"; echo "  $BIN_DIR"; echo "  $CONFIG_DIR"; echo "  $STATE_DIR"; printf "Continue? [y/N] "; read answer; case "$answer" in y|Y|yes|YES) rm -rf "$BIN_DIR" "$CONFIG_DIR" "$STATE_DIR"; echo "SteadIP removed.";; *) echo "Cancelled.";; esac; }
cmd="${1:-}"; case "$cmd" in login) cmd_login;; relogin) cmd_relogin;; sync) cmd_sync;; up) cmd_up;; down) cmd_down;; enable) cmd_enable;; disable) cmd_disable;; status) cmd_status;; logs) cmd_logs;; config) cmd_config;; logout) cmd_logout;; uninstall) cmd_uninstall;; daemon) cmd_daemon;; help|-h|--help|"") usage;; *) echo "Unknown command: $cmd" >&2; echo >&2; usage >&2; exit 1;; esac

STEADIP_CLI
chmod 755 "$wrapper"; echo "Installed SteadIP CLI to $wrapper"; }
install_symlink_or_hint(){ if [ -w "$INSTALL_DIR" ]; then ln -sf "$BIN_DIR/steadip" "$INSTALL_DIR/steadip"; echo "Linked steadip to $INSTALL_DIR/steadip"; return; fi; if command -v sudo >/dev/null 2>&1; then echo "Creating system symlink with sudo..."; sudo ln -sf "$BIN_DIR/steadip" "$INSTALL_DIR/steadip"; echo "Linked steadip to $INSTALL_DIR/steadip"; return; fi; echo; echo "Could not write to $INSTALL_DIR and sudo is not available."; echo "Add this to your shell profile:"; echo; echo "  export PATH="$BIN_DIR:\$PATH""; echo; }
main(){ need_cmd uname; need_cmd tar; need_cmd find; need_cmd mktemp; command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 || { echo "Missing curl or wget." >&2; exit 1; }; if ! command -v php >/dev/null 2>&1; then echo "Warning: php-cli not found. Install php-cli before running 'steadip login'."; echo; fi; mkdir -p "$BIN_DIR" "$CONFIG_DIR" "$STATE_DIR"; install_frpc; install_steadip_cli; install_symlink_or_hint; echo; echo "SteadIP installed."; echo; echo "Next steps:"; echo "  steadip login"; echo "  steadip up"; echo; echo "Auto-start:"; echo "  steadip enable"; echo; echo "Dashboard:"; echo "  https://steadip.com"; echo; }
main "$@"
