#!/usr/bin/env sh
set -eu

APP_NAME="steadip"

INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
CONFIG_DIR="${CONFIG_DIR:-$HOME/.config/steadip}"
STATE_DIR="${STATE_DIR:-$HOME/.local/state/steadip}"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin/steadip}"
LAUNCH_AGENT_DIR="$HOME/Library/LaunchAgents"
LAUNCH_AGENT_FILE="$LAUNCH_AGENT_DIR/com.steadip.client.plist"

FRP_VERSION="${FRP_VERSION:-0.61.1}"

STEADIP_DOMAIN="steadip.com"
STEADIP_API="https://steadip.com/api"
STEADIP_DASHBOARD="https://steadip.com"

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required command: $1" >&2
    exit 1
  }
}

detect_os() {
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$os" in
    darwin) echo "darwin" ;;
    *) echo "Unsupported OS for this installer: $os" >&2; exit 1 ;;
  esac
}

detect_arch() {
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *) echo "Unsupported architecture: $arch" >&2; exit 1 ;;
  esac
}

download() {
  url="$1"
  output="$2"
  curl -fsSL "$url" -o "$output"
}

install_frpc() {
  os="$(detect_os)"
  arch="$(detect_arch)"

  archive="frp_${FRP_VERSION}_${os}_${arch}.tar.gz"
  url="https://github.com/fatedier/frp/releases/download/v${FRP_VERSION}/${archive}"

  mkdir -p "$BIN_DIR"

  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT INT TERM

  echo "Downloading frpc v${FRP_VERSION} for ${os}/${arch}..."
  download "$url" "$tmpdir/$archive"

  tar -xzf "$tmpdir/$archive" -C "$tmpdir"

  extracted_dir="$(find "$tmpdir" -type d -name "frp_${FRP_VERSION}_${os}_${arch}" | head -n 1)"

  if [ -z "$extracted_dir" ] || [ ! -f "$extracted_dir/frpc" ]; then
    echo "Could not find frpc in downloaded archive." >&2
    exit 1
  fi

  cp "$extracted_dir/frpc" "$BIN_DIR/frpc"
  chmod 755 "$BIN_DIR/frpc"

  echo "Installed frpc to $BIN_DIR/frpc"
}

install_steadip_cli() {
  mkdir -p "$CONFIG_DIR" "$STATE_DIR" "$BIN_DIR"

  wrapper="$BIN_DIR/steadip"

  cat > "$wrapper" <<'STEADIP_CLI'
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

LAUNCH_AGENT_DIR="$HOME/Library/LaunchAgents"
LAUNCH_AGENT_FILE="$LAUNCH_AGENT_DIR/com.steadip.client.plist"

STEADIP_DOMAIN="steadip.com"
STEADIP_API="https://steadip.com/api"
STEADIP_DASHBOARD="https://steadip.com"

mkdir -p "$CONFIG_DIR" "$STATE_DIR"

usage() {
  cat <<USAGE
SteadIP CLI for macOS

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
  enable     Enable and start macOS LaunchAgent auto-start daemon
  disable    Stop and disable macOS LaunchAgent auto-start daemon
  status     Show tunnel and daemon status
  logs       Show frpc logs
  config     Print local frpc config with secrets hidden
  logout     Stop tunnels and remove local login token
  uninstall  Remove SteadIP client files

Dashboard:
  Configure tunnels at https://steadip.com

Free:
  HTTP/HTTPS tunnels with random SteadIP subdomains.

Verified:
  custom SteadIP subdomains, custom domains, SSH/TCP.

USAGE
}

require_php_for_json() {
  if ! command -v php >/dev/null 2>&1; then
    echo "Missing php command." >&2
    echo "This MVP SteadIP CLI uses php-cli to parse JSON responses." >&2
    echo "Install it with Homebrew, for example:" >&2
    echo "  brew install php" >&2
    exit 1
  fi
}

http_post_json() {
  url="$1"
  data="$2"
  auth="${3:-}"

  if [ -n "$auth" ]; then
    curl -fsSL -X POST "$url" \
      -H "Content-Type: application/json" \
      -H "Authorization: Bearer $auth" \
      --data "$data"
  else
    curl -fsSL -X POST "$url" \
      -H "Content-Type: application/json" \
      --data "$data"
  fi
}

http_get_json() {
  url="$1"
  auth="${2:-}"

  if [ -n "$auth" ]; then
    curl -fsSL "$url" \
      -H "Accept: application/json" \
      -H "Authorization: Bearer $auth"
  else
    curl -fsSL "$url" \
      -H "Accept: application/json"
  fi
}

json_get_string() {
  key="$1"

  php -r '
    $key = $argv[1];
    $json = stream_get_contents(STDIN);
    $data = json_decode($json, true);
    if (!is_array($data)) { exit(1); }
    $parts = explode(".", $key);
    $value = $data;
    foreach ($parts as $part) {
      if (!is_array($value) || !array_key_exists($part, $value)) { exit(1); }
      $value = $value[$part];
    }
    if ($value === null) { exit(1); }
    if (is_bool($value)) { echo $value ? "true" : "false"; exit(0); }
    if (is_scalar($value)) { echo $value; exit(0); }
    echo json_encode($value);
  ' "$key"
}

json_write_frpc_config() {
  input_file="$1"
  output_file="$2"

  php -r '
    $inputFile = $argv[1];
    $outputFile = $argv[2];
    $json = file_get_contents($inputFile);
    $data = json_decode($json, true);
    if (!is_array($data)) {
      fwrite(STDERR, "Invalid config JSON returned by SteadIP API.\n");
      exit(1);
    }
    $frp = $data["frp"] ?? "";
    file_put_contents($outputFile, $frp);
  ' "$input_file" "$output_file"
}

json_print_tunnels() {
  input_file="$1"

  php -r '
    $inputFile = $argv[1];
    $json = file_get_contents($inputFile);
    $data = json_decode($json, true);
    if (!is_array($data)) { exit(1); }
    $tunnels = $data["tunnels"] ?? [];
    if (!is_array($tunnels) || count($tunnels) === 0) {
      echo "No active tunnels configured.\n";
      exit(0);
    }
    foreach ($tunnels as $tunnel) {
      if (!is_array($tunnel)) { continue; }
      $type = strtoupper((string) ($tunnel["type"] ?? ""));
      $localHost = (string) ($tunnel["local_host"] ?? "127.0.0.1");
      $localPort = (string) ($tunnel["local_port"] ?? "");
      if (!empty($tunnel["public_url"])) {
        echo "{$type}: {$tunnel["public_url"]} -> {$localHost}:{$localPort}\n";
        continue;
      }
      if (!empty($tunnel["public_endpoint"])) {
        echo "{$type}: {$tunnel["public_endpoint"]} -> {$localHost}:{$localPort}\n";
        continue;
      }
      if (!empty($tunnel["remote_port"])) {
        echo "{$type}: gateway.steadip.com:{$tunnel["remote_port"]} -> {$localHost}:{$localPort}\n";
        continue;
      }
      if (!empty($tunnel["subdomain"])) {
        echo "{$type}: https://{$tunnel["subdomain"]}.steadip.com -> {$localHost}:{$localPort}\n";
        continue;
      }
    }
  ' "$input_file"
}

get_token() {
  if [ -f "$TOKEN_FILE" ]; then cat "$TOKEN_FILE"; else echo ""; fi
}

save_token() {
  token="$1"
  umask 077
  mkdir -p "$CONFIG_DIR"
  printf "%s" "$token" > "$TOKEN_FILE"
  chmod 600 "$TOKEN_FILE" 2>/dev/null || true
}

require_login() {
  token="$(get_token)"
  if [ -z "$token" ]; then
    echo "You are not logged in." >&2
    echo "Run: steadip login" >&2
    exit 1
  fi
  echo "$token"
}

is_manual_running() {
  [ -f "$PID_FILE" ] || return 1
  pid="$(cat "$PID_FILE" 2>/dev/null || true)"
  [ -n "$pid" ] || return 1
  kill -0 "$pid" >/dev/null 2>&1
}

launch_label() {
  echo "com.steadip.client"
}

is_daemon_active() {
  launchctl print "gui/$(id -u)/$(launch_label)" >/dev/null 2>&1
}

open_browser() {
  url="$1"
  open "$url" >/dev/null 2>&1 || echo "$url"
}

device_name() {
  hostname 2>/dev/null || echo unknown
}

cmd_login() {
  require_php_for_json
  device="$(device_name)"
  payload="{\"client_name\":\"steadip-cli\",\"client_version\":\"0.2.0\",\"device_name\":\"$device\"}"

  echo "Requesting SteadIP device login..."
  response="$(http_post_json "$STEADIP_API/device/code" "$payload")"

  device_code="$(printf "%s" "$response" | json_get_string device_code)"
  user_code="$(printf "%s" "$response" | json_get_string user_code)"
  verification_uri="$(printf "%s" "$response" | json_get_string verification_uri)"
  verification_uri_complete="$(printf "%s" "$response" | json_get_string verification_uri_complete || true)"
  interval="$(printf "%s" "$response" | json_get_string interval || echo 5)"
  expires_in="$(printf "%s" "$response" | json_get_string expires_in || echo 600)"

  echo
  echo "SteadIP CLI login"
  echo
  echo "Open this page:"
  echo "  $verification_uri"
  echo
  echo "Enter code:"
  echo "  $device_code"
  echo

  if [ -n "$verification_uri_complete" ]; then
    open_browser "$verification_uri_complete"
  fi

  echo "Waiting for authorization..."
  echo
  start_time="$(date +%s)"

  while :; do
    now="$(date +%s)"
    elapsed=$((now - start_time))
    if [ "$elapsed" -ge "$expires_in" ]; then
      echo "Login expired. Run 'steadip login' again." >&2
      exit 1
    fi

    sleep "$interval"
    token_payload="{\"device_code\":\"$device_code\",\"user_code\":\"$user_code\"}"

    tmp_error="$(mktemp)"
    set +e
    token_response="$(http_post_json "$STEADIP_API/device/token" "$token_payload" 2>"$tmp_error")"
    code="$?"
    set -e

    if [ "$code" -ne 0 ]; then
      err="$(cat "$tmp_error" 2>/dev/null || true)"
      rm -f "$tmp_error"
      error="$(printf "%s" "$err" | json_get_string error 2>/dev/null || true)"
      if [ "$error" = "authorization_pending" ] || [ -z "$error" ]; then printf "."; continue; fi
      if [ "$error" = "slow_down" ]; then interval=$((interval + 5)); printf "."; continue; fi
      if [ "$error" = "tunnels_limit_reached" ]; then echo; echo "Maximum number of tunnels reached." >&2; exit 1; fi
      if [ "$error" = "expired_token" ]; then echo; echo "Login expired." >&2; exit 1; fi
      if [ "$error" = "access_denied" ]; then echo; echo "Login was denied." >&2; exit 1; fi
      if [ "$error" = "no_device_code" ]; then echo; echo "Device code was lost in transport." >&2; exit 1; fi
      printf "."
      continue
    fi

    rm -f "$tmp_error"
    access_token="$(printf "%s" "$token_response" | json_get_string access_token)"
    email="$(printf "%s" "$token_response" | json_get_string user_email || echo "")"
    verified="$(printf "%s" "$token_response" | json_get_string user_verified || echo "false")"

    save_token "$access_token"

    echo
    echo
    echo "Logged in successfully."
    [ -n "$email" ] && echo "Account: $email"
    if [ "$verified" = "true" ]; then echo "Plan: Verified"; else echo "Plan: Free"; fi
    echo
    echo "Configure tunnels in your dashboard:"
    echo "  $STEADIP_DASHBOARD"
    echo
    echo "Then run:"
    echo "  steadip up"
    echo
    exit 0
  done
}

cmd_relogin() {
  require_php_for_json

  printf "Enter device code from SteadIP webapp: "
  read device_code
  device_code="$(printf "%s" "$device_code" | tr -d '[:space:]')"

  if [ -z "$device_code" ]; then
    echo "Missing device code." >&2
    exit 1
  fi

  device="$(device_name)"

  payload="{\"device_code\":\"$device_code\",\"relogin\":true,\"client_name\":\"steadip-cli\",\"client_version\":\"0.2.2\",\"device_name\":\"$device\"}"

  echo "Authorizing this device with SteadIP..."

  result="$(http_post_json_with_status "$STEADIP_API/device/token" "$payload")"
  http_code="$(printf "%s" "$result" | sed -n '1p')"
  token_response="$(printf "%s" "$result" | sed '1d')"

  if [ "$http_code" != "200" ]; then
    error="$(printf "%s" "$token_response" | json_get_string error 2>/dev/null || true)"

    if [ "$error" = "tunnels_limit_reached" ]; then
      echo "Maximum number of tunnels reached. Delete an existing tunnel from your SteadIP dashboard, then try again." >&2
      exit 1
    fi

    if [ "$error" = "expired_token" ]; then
      echo "Device code expired. Generate a new one from the SteadIP dashboard." >&2
      exit 1
    fi

    if [ "$error" = "access_denied" ]; then
      echo "Device code was denied." >&2
      exit 1
    fi

    if [ "$error" = "invalid_device_code" ]; then
      echo "Invalid device code." >&2
      exit 1
    fi

    if [ "$error" = "no_device_code" ]; then
      echo "Missing device code." >&2
      exit 1
    fi

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

cmd_sync() {
  require_php_for_json
  token="$(require_login)"
  echo "Fetching SteadIP tunnel config..."
  response="$(http_get_json "$STEADIP_API/device/config" "$token")"

  umask 077
  printf "%s" "$response" > "$META_FILE"
  chmod 600 "$META_FILE" 2>/dev/null || true

  set +e
  json_write_frpc_config "$META_FILE" "$CONFIG_FILE"
  code="$?"
  set -e

  if [ "$code" -eq 2 ]; then exit 2; fi
  if [ "$code" -ne 0 ]; then echo "Could not write frpc config." >&2; exit "$code"; fi

  chmod 600 "$CONFIG_FILE" 2>/dev/null || true
  echo "Config written:"
  echo "  $CONFIG_FILE"
  echo
  echo "Configured tunnels:"
  json_print_tunnels "$META_FILE"
}

start_manual_frpc() {
  if [ ! -x "$FRPC" ]; then echo "frpc is missing: $FRPC" >&2; exit 1; fi
  if [ ! -f "$CONFIG_FILE" ]; then echo "No frpc config found. Run: steadip sync" >&2; exit 1; fi

  if is_manual_running; then
    old_pid="$(cat "$PID_FILE")"
    kill "$old_pid" >/dev/null 2>&1 || true
    sleep 1
  fi

  echo
  echo "Starting SteadIP tunnels..."
  nohup "$FRPC" -c "$CONFIG_FILE" > "$LOG_FILE" 2>&1 &
  echo "$!" > "$PID_FILE"
  sleep 1

  if is_manual_running; then
    echo "Started."
    echo
    [ -f "$META_FILE" ] && json_print_tunnels "$META_FILE"
    echo
    echo "Logs: $LOG_FILE"
  else
    echo "frpc failed to start. Logs:" >&2
    tail -n 120 "$LOG_FILE" >&2 || true
    exit 1
  fi
}

cmd_up() {
  cmd_sync
  if is_daemon_active; then
    echo
    echo "SteadIP LaunchAgent is running. Restarting with latest config..."
    launchctl kickstart -k "gui/$(id -u)/$(launch_label)" >/dev/null 2>&1 || true
    echo "Restarted."
    return
  fi
  start_manual_frpc
}

cmd_down() {
  stopped="false"
  if is_manual_running; then
    pid="$(cat "$PID_FILE")"
    kill "$pid" >/dev/null 2>&1 || true
    sleep 1
    if kill -0 "$pid" >/dev/null 2>&1; then kill -9 "$pid" >/dev/null 2>&1 || true; fi
    rm -f "$PID_FILE"
    echo "Stopped manually started SteadIP tunnel."
    stopped="true"
  else
    rm -f "$PID_FILE"
  fi

  if is_daemon_active; then
    launchctl bootout "gui/$(id -u)" "$LAUNCH_AGENT_FILE" >/dev/null 2>&1 || true
    echo "Stopped SteadIP LaunchAgent daemon."
    stopped="true"
  fi

  [ "$stopped" = "false" ] && echo "SteadIP tunnel is not running."
}

cmd_daemon() {
  require_php_for_json
  token="$(require_login)"
  echo "Fetching SteadIP tunnel config..."
  response="$(http_get_json "$STEADIP_API/device/config" "$token")"

  umask 077
  printf "%s" "$response" > "$META_FILE"
  chmod 600 "$META_FILE" 2>/dev/null || true
  json_write_frpc_config "$META_FILE" "$CONFIG_FILE"
  chmod 600 "$CONFIG_FILE" 2>/dev/null || true

  echo "Configured tunnels:"
  json_print_tunnels "$META_FILE"
  echo
  echo "Starting frpc in daemon mode..."
  exec "$FRPC" -c "$CONFIG_FILE"
}

cmd_enable() {
  require_login >/dev/null
  mkdir -p "$LAUNCH_AGENT_DIR"

  cat > "$LAUNCH_AGENT_FILE" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.steadip.client</string>
  <key>ProgramArguments</key>
  <array>
    <string>$BIN_DIR/steadip</string>
    <string>daemon</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>$LOG_FILE</string>
  <key>StandardErrorPath</key>
  <string>$LOG_FILE</string>
  <key>WorkingDirectory</key>
  <string>$HOME</string>
</dict>
</plist>
PLIST

  chmod 644 "$LAUNCH_AGENT_FILE"
  launchctl bootout "gui/$(id -u)" "$LAUNCH_AGENT_FILE" >/dev/null 2>&1 || true
  launchctl bootstrap "gui/$(id -u)" "$LAUNCH_AGENT_FILE"
  launchctl enable "gui/$(id -u)/$(launch_label)" >/dev/null 2>&1 || true
  launchctl kickstart -k "gui/$(id -u)/$(launch_label)" >/dev/null 2>&1 || true

  echo "SteadIP auto-start enabled and started."
  echo "LaunchAgent: $LAUNCH_AGENT_FILE"
}

cmd_disable() {
  launchctl bootout "gui/$(id -u)" "$LAUNCH_AGENT_FILE" >/dev/null 2>&1 || true
  rm -f "$LAUNCH_AGENT_FILE"
  echo "SteadIP auto-start disabled."
}

cmd_status() {
  if is_manual_running; then
    echo "Manual tunnel: running"
    echo "Manual PID: $(cat "$PID_FILE")"
  else
    echo "Manual tunnel: stopped"
  fi

  if [ -f "$LAUNCH_AGENT_FILE" ]; then echo "Auto-start: enabled"; else echo "Auto-start: disabled"; fi
  if is_daemon_active; then echo "Daemon: running"; else echo "Daemon: stopped"; fi

  [ -f "$CONFIG_FILE" ] && echo "Config: $CONFIG_FILE"
  if [ -f "$META_FILE" ]; then
    echo
    echo "Configured tunnels:"
    json_print_tunnels "$META_FILE" || true
  fi
  [ -f "$LOG_FILE" ] && echo "Logs: $LOG_FILE"
}

cmd_logs() {
  if [ ! -f "$LOG_FILE" ]; then
    echo "No logs yet."
    exit 0
  fi
  tail -n 120 -f "$LOG_FILE"
}

cmd_config() {
  if [ ! -f "$CONFIG_FILE" ]; then echo "No config found."; exit 0; fi
  sed \
    -e 's/connection_token = ".*/connection_token = "***"/' \
    -e "s/connection_token = '.*/connection_token = '***'/" \
    "$CONFIG_FILE"
}

cmd_logout() {
  cmd_down >/dev/null 2>&1 || true
  rm -f "$TOKEN_FILE"
  echo "Logged out."
}

cmd_uninstall() {
  cmd_down >/dev/null 2>&1 || true
  launchctl bootout "gui/$(id -u)" "$LAUNCH_AGENT_FILE" >/dev/null 2>&1 || true
  rm -f "$LAUNCH_AGENT_FILE"

  echo "This will remove:"
  echo "  $BIN_DIR"
  echo "  $CONFIG_DIR"
  echo "  $STATE_DIR"
  printf "Continue? [y/N] "
  read answer
  case "$answer" in
    y|Y|yes|YES) rm -rf "$BIN_DIR" "$CONFIG_DIR" "$STATE_DIR"; echo "SteadIP removed." ;;
    *) echo "Cancelled." ;;
  esac
}

cmd="${1:-}"
case "$cmd" in
  login) cmd_login ;;
  relogin) cmd_relogin ;;
  sync) cmd_sync ;;
  up) cmd_up ;;
  down) cmd_down ;;
  enable) cmd_enable ;;
  disable) cmd_disable ;;
  status) cmd_status ;;
  logs) cmd_logs ;;
  config) cmd_config ;;
  logout) cmd_logout ;;
  uninstall) cmd_uninstall ;;
  daemon) cmd_daemon ;;
  help|-h|--help|"") usage ;;
  *) echo "Unknown command: $cmd" >&2; echo >&2; usage >&2; exit 1 ;;
esac
STEADIP_CLI

  chmod 755 "$wrapper"
  echo "Installed SteadIP CLI to $wrapper"
}

install_symlink_or_hint() {
  if [ -w "$INSTALL_DIR" ]; then
    ln -sf "$BIN_DIR/steadip" "$INSTALL_DIR/steadip"
    echo "Linked steadip to $INSTALL_DIR/steadip"
    return
  fi

  if command -v sudo >/dev/null 2>&1; then
    echo "Creating system symlink with sudo..."
    sudo ln -sf "$BIN_DIR/steadip" "$INSTALL_DIR/steadip"
    echo "Linked steadip to $INSTALL_DIR/steadip"
    return
  fi

  echo
  echo "Could not write to $INSTALL_DIR and sudo is not available."
  echo "Add this to your shell profile:"
  echo
  echo "  export PATH=\"$BIN_DIR:\$PATH\""
  echo
}

main() {
  need_cmd uname
  need_cmd tar
  need_cmd find
  need_cmd mktemp
  need_cmd curl

  if ! command -v php >/dev/null 2>&1; then
    echo "Warning: php-cli not found."
    echo "The installed SteadIP CLI uses php to parse JSON API responses."
    echo "Install with: brew install php"
    echo
  fi

  mkdir -p "$BIN_DIR" "$CONFIG_DIR" "$STATE_DIR"
  install_frpc
  install_steadip_cli
  install_symlink_or_hint

  echo
  echo "SteadIP installed for macOS."
  echo
  echo "Next steps:"
  echo "  steadip login"
  echo "  steadip up"
  echo
  echo "Auto-start:"
  echo "  steadip enable"
  echo
  echo "Dashboard:"
  echo "  https://steadip.com"
  echo
}

main "$@"
