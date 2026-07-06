#!/usr/bin/env sh
set -eu

APP_NAME="steadip"
REPO_OWNER="mlowasp"
REPO_NAME="steadip"
BRANCH="${STEADIP_BRANCH:-main}"
BASE_URL="${STEADIP_BASE_URL:-https://raw.githubusercontent.com/${REPO_OWNER}/${REPO_NAME}/${BRANCH}/cli/steadip-go-cli/dist}"

INSTALL_DIR="${STEADIP_INSTALL_DIR:-$HOME/.local/bin}"
BIN_PATH="$INSTALL_DIR/$APP_NAME"

info() { printf "\033[1;36m==>\033[0m %s\n" "$*"; }
ok() { printf "\033[1;32m✓\033[0m %s\n" "$*"; }
warn() { printf "\033[1;33m!\033[0m %s\n" "$*" >&2; }
fail() { printf "\033[1;31mError:\033[0m %s\n" "$*" >&2; exit 1; }

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "Missing required command: $1"
}

detect_arch() {
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) printf "amd64" ;;
    aarch64|arm64) printf "arm64" ;;
    *) fail "Unsupported CPU architecture: $arch" ;;
  esac
}

download() {
  url="$1"
  dest="$2"

  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$dest"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$url" -O "$dest"
  else
    fail "curl or wget is required"
  fi
}

main() {
  need_cmd uname
  need_cmd chmod
  need_cmd mkdir

  os="$(uname -s)"
  case "$os" in
    Linux) platform="linux" ;;
    *) fail "This installer is for Linux only. Detected: $os" ;;
  esac

  arch="$(detect_arch)"
  binary="steadip-${platform}-${arch}"
  url="${BASE_URL}/${binary}"

  info "Installing SteadIP TUI CLI"
  info "Platform: ${platform}/${arch}"
  info "Source: $url"
  info "Install dir: $INSTALL_DIR"

  mkdir -p "$INSTALL_DIR"

  tmp="$(mktemp)"
  trap 'rm -f "$tmp"' EXIT INT TERM

  download "$url" "$tmp"
  chmod +x "$tmp"
  mv "$tmp" "$BIN_PATH"
  chmod +x "$BIN_PATH"

  ok "Installed: $BIN_PATH"

  if ! printf "%s" ":$PATH:" | grep -q ":$INSTALL_DIR:"; then
    warn "$INSTALL_DIR is not in your PATH."
    shell_name="$(basename "${SHELL:-sh}")"

    case "$shell_name" in
      zsh)
        profile="$HOME/.zshrc"
        ;;
      bash)
        profile="$HOME/.bashrc"
        ;;
      fish)
        profile="$HOME/.config/fish/config.fish"
        ;;
      *)
        profile="$HOME/.profile"
        ;;
    esac

    if [ "$shell_name" = "fish" ]; then
      mkdir -p "$(dirname "$profile")"
      if ! grep -q "$INSTALL_DIR" "$profile" 2>/dev/null; then
        printf '\nfish_add_path %s\n' "$INSTALL_DIR" >> "$profile"
      fi
    else
      if ! grep -q "$INSTALL_DIR" "$profile" 2>/dev/null; then
        printf '\nexport PATH="$HOME/.local/bin:$PATH"\n' >> "$profile"
      fi
    fi

    warn "Added PATH update to $profile"
    warn "Open a new terminal, or run: export PATH=\"$INSTALL_DIR:\$PATH\""
  fi

  if "$BIN_PATH" --help >/dev/null 2>&1; then
    ok "SteadIP CLI is ready."
  else
    warn "Installed binary, but --help returned a non-zero status."
  fi

  printf "\nNext steps:\n"
  printf "  %s login\n" "$APP_NAME"
  printf "  %s up\n" "$APP_NAME"
  printf "  %s status\n\n" "$APP_NAME"
}

main "$@"
