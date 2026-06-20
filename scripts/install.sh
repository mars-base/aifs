#!/bin/sh
# aifs one-line installer for Linux / macOS
# Detects and guides installation of all required dependencies:
#   macOS:  Homebrew, Podman, Podman Machine, macFUSE (kernel extension)
#   Linux:  Podman, fusermount3 (fuse3/libfuse3)
set -eu

# ─── Configuration ──────────────────────────────────────────────────

REPO="mars-base/aifs"
BIN="aifs"
INSTALL_DIR="/usr/local/bin"

# ─── Utilities ──────────────────────────────────────────────────────

BOLD=""; DIM=""; GREEN=""; YELLOW=""; RED=""; RESET=""
if [ -t 1 ] && [ -n "${TERM:-}" ] && [ "$TERM" != dumb ]; then
  BOLD="$(printf '\033[1m')"; DIM="$(printf '\033[2m')"
  GREEN="$(printf '\033[32m')"; YELLOW="$(printf '\033[33m')"; RED="$(printf '\033[31m')"
  RESET="$(printf '\033[0m')"
fi

step()  { echo ""; echo "${BOLD}→${RESET} $*"; }
ok()    { echo "  ${GREEN}✓${RESET} $*"; }
warn()  { echo "  ${YELLOW}⚠${RESET} $*"; }
fail()  { echo "  ${RED}✗${RESET} $*"; }
info()  { echo "  ${DIM}$*${RESET}"; }
cmd_hint() { echo "  ${DIM}Run:${RESET} $*"; }

die()   { echo ""; fail "$*"; echo ""; exit 1; }

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
IS_MACOS=false; IS_LINUX=false
case "$OS" in
  darwin) IS_MACOS=true ;;
  linux)  IS_LINUX=true ;;
  *)      die "Unsupported OS: $OS (only macOS and Linux are supported)" ;;
esac

# ─── Dependency probes (no side effects) ────────────────────────────

has_homebrew()      { command -v brew >/dev/null 2>&1; }
has_podman()        { command -v podman >/dev/null 2>&1; }
has_podman_machine() {
  # podman machine list exits non-zero when no machine is configured
  command -v podman >/dev/null 2>&1 && podman machine list >/dev/null 2>&1
}
has_macfuse_kext() {
  # Check if macFUSE kernel extension is loaded
  if command -v kextstat >/dev/null 2>&1; then
    kextstat 2>/dev/null | grep -qi macfuse && return 0
  fi
  # Fallback: check if /dev/macfuse* device nodes exist
  ls /dev/macfuse* >/dev/null 2>&1
}
has_macfuse_bundle() {
  [ -d "/Library/Filesystems/macfuse.fs" ]
}
has_fusermount() {
  command -v fusermount3 >/dev/null 2>&1 || command -v fusermount >/dev/null 2>&1
}

# Detect Linux distribution for package manager hints
detect_linux_distro() {
  if [ -f /etc/os-release ]; then
    . /etc/os-release
    echo "$ID"
  elif command -v apt-get >/dev/null 2>&1; then
    echo "debian"
  elif command -v dnf >/dev/null 2>&1; then
    echo "fedora"
  elif command -v pacman >/dev/null 2>&1; then
    echo "arch"
  else
    echo "unknown"
  fi
}

# auto_install_pkg tries to install a package with the system package manager.
# Returns 0 on success, 1 on failure (unknown distro / install error).
auto_install_pkg() {
  DISTRO="$(detect_linux_distro)"
  case "$DISTRO" in
    ubuntu|debian)
      sudo apt-get update && sudo apt-get install -y "$@"
      ;;
    fedora|rhel|centos|rocky|almalinux)
      sudo dnf install -y "$@"
      ;;
    arch|manjaro)
      sudo pacman -S --noconfirm "$@"
      ;;
    alpine)
      sudo apk add "$@"
      ;;
    opensuse*|sles)
      sudo zypper install -y "$@"
      ;;
    *)
      return 1
      ;;
  esac
}

# ─── Phase 1: Binary download ──────────────────────────────────────

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) die "Unsupported architecture: $ARCH" ;;
esac

step "Downloading aifs binary (${OS}-${ARCH})..."
TAG="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')"
if [ -z "$TAG" ]; then
  die "Failed to determine latest release tag"
fi

URL="https://github.com/${REPO}/releases/latest/download/${BIN}-${OS}-${ARCH}"
info "${URL}"

if [ "$(id -u)" -eq 0 ]; then
  curl -fsSL "$URL" -o "${INSTALL_DIR}/${BIN}"
  chmod +x "${INSTALL_DIR}/${BIN}"
elif [ -w "$INSTALL_DIR" ]; then
  curl -fsSL "$URL" -o "${INSTALL_DIR}/${BIN}"
  chmod +x "${INSTALL_DIR}/${BIN}"
else
  sudo curl -fsSL "$URL" -o "${INSTALL_DIR}/${BIN}"
  sudo chmod +x "${INSTALL_DIR}/${BIN}"
fi
ok "aifs ${TAG} installed to ${INSTALL_DIR}/${BIN}"

# ─── Phase 2: Dependency checks & guided install ────────────────────

if $IS_MACOS; then
  # ── macOS dependencies ────────────────────────────────────────

  step "Checking macOS dependencies..."

  # --- Homebrew ---
  if has_homebrew; then
    ok "Homebrew found ($(brew --version 2>/dev/null | head -1))"
  else
    fail "Homebrew not found — required to install podman and macFUSE"
    cmd_hint '/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"'
    echo ""
    die "Please install Homebrew first, then re-run this script."
  fi

  # --- Podman ---
  if has_podman; then
    PODMAN_VER="$(podman --version 2>/dev/null || echo 'unknown')"
    ok "Podman found ($PODMAN_VER)"
  else
    warn "Podman not found — installing via Homebrew..."
    brew install podman
    ok "Podman installed"
  fi

  # --- Podman Machine ---
  if has_podman_machine; then
    ok "Podman machine is configured and running"
  else
    warn "Podman machine not initialized — creating VM..."
    info "This will download a VM image and may take a few minutes."
    podman machine init --now
    ok "Podman machine initialized and running"
  fi

  # --- macFUSE ---
  if has_macfuse_bundle; then
    ok "macFUSE filesystem bundle found"
  else
    warn "macFUSE not found — installing via Homebrew cask..."
    brew install --cask macfuse
    ok "macFUSE installed"
  fi

  # --- macFUSE kernel extension ---
  if has_macfuse_kext; then
    ok "macFUSE kernel extension is loaded"
  else
    echo ""
    warn "macFUSE kernel extension is NOT loaded"
    echo ""
    info "Trying to load it now with kextutil (requires admin password)..."
    if sudo kextutil /Library/Filesystems/macfuse.fs 2>/dev/null; then
      ok "macFUSE kext loaded via kextutil"
    else
      echo ""
      warn "Could not load macFUSE kext automatically."
      warn "You may need to:"
      echo "  1. Open ${BOLD}System Settings → Privacy & Security${RESET}"
      echo "  2. Approve the macFUSE extension from the developer"
      echo "  3. ${BOLD}Reboot your Mac${RESET} for the change to take effect"
      echo ""
    fi
  fi

elif $IS_LINUX; then
  # ── Linux dependencies ─────────────────────────────────────────

  step "Checking Linux dependencies..."

  # --- Podman ---
  if has_podman; then
    PODMAN_VER="$(podman --version 2>/dev/null || echo 'unknown')"
    ok "Podman found ($PODMAN_VER)"
  else
    warn "Podman not found — installing..."
    if auto_install_pkg podman; then
      ok "Podman installed"
    else
      warn "Could not auto-install podman (unknown distro)"
      cmd_hint "Install podman via your system package manager"
      cmd_hint "Or: curl -fsSL -o ~/.local/bin/podman https://github.com/89luca89/podman-launcher/releases/latest/download/podman-launcher-amd64 && chmod +x ~/.local/bin/podman"
      die "Please install Podman, then re-run this script."
    fi
  fi

  # --- fusermount3 (FUSE) ---
  if has_fusermount; then
    FUSE_BIN="$(command -v fusermount3 2>/dev/null || command -v fusermount)"
    ok "FUSE umount helper found: ${FUSE_BIN}"
  else
    warn "fusermount3 not found — needed for 'aifs umount', installing..."
    # Distro-specific package names for fuse3
    DISTRO="$(detect_linux_distro)"
    case "$DISTRO" in
      ubuntu|debian)       PKG="fuse3" ;;
      fedora|rhel|centos|rocky|almalinux) PKG="fuse3-libs" ;;
      arch|manjaro)         PKG="fuse3" ;;
      alpine)               PKG="fuse3" ;;
      opensuse*|sles)       PKG="fuse3" ;;
      *)                    PKG="" ;;
    esac
    if [ -n "$PKG" ] && auto_install_pkg "$PKG"; then
      ok "fuse3 installed ($PKG)"
    else
      warn "Could not auto-install fuse3"
      [ -n "$PKG" ] && cmd_hint "sudo apt-get install -y fuse3 (or equivalent for your distro)"
      warn "'aifs umount' will not work without fusermount3/fusermount"
    fi
  fi
fi

# ─── Phase 3: Pull container images ────────────────────────────────

if command -v podman >/dev/null 2>&1; then
  step "Pulling aifs container images..."
  # These are the default image tags from aifs config.
  # Custom image tags will be pulled on demand by 'aifs start'.
  # Failures here are non-fatal — images will be pulled on first use.
  podman pull ghcr.io/mars-base/aifs/aifs-pg:18-2.58.0 >/dev/null 2>&1 \
    && ok "aifs-pg:18-2.58.0" \
    || warn "aifs-pg pull failed, will retry on first use"
  podman pull ghcr.io/mars-base/aifs/aifs-backup:2.58.0 >/dev/null 2>&1 \
    && ok "aifs-backup:2.58.0" \
    || warn "aifs-backup pull failed, will retry on first use"
  podman pull docker.io/library/alpine:3.20 >/dev/null 2>&1 \
    && ok "alpine:3.20 (helper)" \
    || warn "alpine helper image pull failed, will retry on first use"
fi

# ─── Phase 4: Done ─────────────────────────────────────────────────

echo ""
echo "${BOLD}${GREEN}✓ aifs installation complete${RESET}"
echo ""
echo "${BOLD}Quick start:${RESET}"
echo "  aifs version"
echo "  aifs config init --add <instance-name>"
echo "  aifs start -i <instance-name>"
echo "  aifs format -i <instance-name> --volume <volume-name>"
echo "  mkdir -p ~/mnt && aifs mount -i <instance-name> ~/mnt"
echo ""

if $IS_MACOS && ! has_macfuse_kext; then
  warn "macFUSE kext is not loaded yet."
  warn "After approving in System Settings and rebooting, verify with:"
  info "  ls /dev/macfuse*"
  echo ""
fi

if $IS_MACOS; then
  info "macOS notes:"
  info "  - Podman runs inside a VM (podman machine). Use 'podman machine' to manage it."
  info "  - For files outside your home directory, add volume mounts via podman machine."
  echo ""
fi
