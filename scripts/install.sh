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
  # podman machine list exits non-zero when no machine is configured.
  # On macOS podman runs inside a VM, so we also require the machine to be
  # (or have been) started; a configured-but-stopped machine still lists OK.
  command -v podman >/dev/null 2>&1 && podman machine list >/dev/null 2>&1
}
podman_machine_running() {
  # `podman machine list` prints "Currently running" for the active VM.
  command -v podman >/dev/null 2>&1 || return 1
  podman machine list 2>/dev/null | grep -qi "currently running"
}
is_apple_silicon() {
  [ "$(uname -m)" = "arm64" ]
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

# Detect the Linux distribution family for distro-specific package names.
# Echoes a normalized family token that install_pkgs / package-name maps can
# case on. Reads /etc/os-release ($ID and $ID_LIKE) so that derivatives
# (rocky, almalinux, opensuse-tumbleweed, ...) resolve to their base family.
# Falls back to probing the package manager binary when os-release is absent.
detect_linux_distro() {
  if [ -f /etc/os-release ]; then
    . /etc/os-release
    # First try the concrete distro $ID.
    case "${ID:-}" in
      ubuntu|linuxmint|pop|kali|raspbian|debian) echo "debian"; return 0 ;;
      fedora)                                    echo "fedora"; return 0 ;;
      rhel|centos|rocky|almalinux|ol|cloudlinux) echo "rhel";   return 0 ;;
      arch|manjaro|garuda|endeavouros|cachyos)   echo "arch";   return 0 ;;
      alpine)                                    echo "alpine"; return 0 ;;
      opensuse*|sles|sle-micro|suse)             echo "suse";   return 0 ;;
    esac
    # Derivatives: walk $ID_LIKE for an ancestor family.
    for _like in ${ID_LIKE:-}; do
      case "$_like" in
        debian|ubuntu)      echo "debian"; return 0 ;;
        rhel|fedora|centos) echo "rhel";   return 0 ;;
        arch)               echo "arch";   return 0 ;;
        suse|opensuse)      echo "suse";   return 0 ;;
        alpine)             echo "alpine"; return 0 ;;
      esac
    done
  fi
  # Last resort: detect by available package manager binary.
  for _pm in apt-get dnf yum zypper pacman apk; do
    if command -v "$_pm" >/dev/null 2>&1; then
      case "$_pm" in
        apt-get) echo "debian" ;;
        dnf|yum) echo "rhel" ;;
        zypper)  echo "suse" ;;
        pacman)  echo "arch" ;;
        apk)     echo "alpine" ;;
      esac
      return 0
    fi
  done
  echo "unknown"
}

# Detect the system package manager binary to use for installs.
# Echoes the binary name (apt-get|dnf|yum|zypper|pacman|apk) or empty.
# Probes by binary availability rather than distro name so that older RHEL 7
# (yum only) and newer RHEL 9+ (dnf) are both handled, regardless of $ID.
detect_pkg_manager() {
  DISTRO="$(detect_linux_distro)"
  case "$DISTRO" in
    debian)
      command -v apt-get >/dev/null 2>&1 && { echo apt-get; return 0; } ;;
    fedora|rhel)
      command -v dnf >/dev/null 2>&1 && { echo dnf; return 0; }
      command -v yum >/dev/null 2>&1 && { echo yum; return 0; } ;;
    arch)
      command -v pacman >/dev/null 2>&1 && { echo pacman; return 0; } ;;
    alpine)
      command -v apk >/dev/null 2>&1 && { echo apk; return 0; } ;;
    suse)
      command -v zypper >/dev/null 2>&1 && { echo zypper; return 0; } ;;
  esac
  # Fallback: probe any known manager regardless of detected distro.
  for _pm in apt-get dnf yum zypper pacman apk; do
    command -v "$_pm" >/dev/null 2>&1 && { echo "$_pm"; return 0; }
  done
  echo ""
}

# Run a command with root privileges if needed. If we are already root, run
# directly; otherwise prefix sudo. Avoids "sudo: not found" / password prompts
# when the installer is run as root (e.g. inside a container).
as_root() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
  else
    sudo "$@"
  fi
}

# install_pkgs installs one or more packages via the detected package manager.
# Returns 0 on success, 1 if no package manager was found or install failed.
install_pkgs() {
  PM="$(detect_pkg_manager)"
  [ -z "$PM" ] && return 1
  case "$PM" in
    apt-get) as_root apt-get update && as_root apt-get install -y "$@" ;;
    dnf)     as_root dnf install -y "$@" ;;
    yum)     as_root yum install -y "$@" ;;
    zypper)  as_root zypper install -y "$@" ;;
    pacman)  as_root pacman -S --noconfirm --needed "$@" ;;
    apk)     as_root apk add --no-cache "$@" ;;
    *)       return 1 ;;
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

# ── GUI binary ──────────────────────────────────────────────────
# GUI is not built for macOS Intel (darwin-amd64) — skip download on that arch.
GUI_ARCH="${OS}-${ARCH}"
GUI_SKIP=false
if [ "$GUI_ARCH" = "darwin-amd64" ]; then
  GUI_SKIP=true
  info "aifs-gui is not available for macOS Intel (arm64 only); skipping"
fi

if ! $GUI_SKIP; then
  step "Downloading aifs-gui binary (${GUI_ARCH})..."
  GUI_URL="https://github.com/${REPO}/releases/latest/download/aifs-gui-${GUI_ARCH}"
  info "${GUI_URL}"
  GUI_BIN="aifs-gui"

  if [ "$(id -u)" -eq 0 ]; then
    curl -fsSL "$GUI_URL" -o "${INSTALL_DIR}/${GUI_BIN}"
    chmod +x "${INSTALL_DIR}/${GUI_BIN}"
  elif [ -w "$INSTALL_DIR" ]; then
    curl -fsSL "$GUI_URL" -o "${INSTALL_DIR}/${GUI_BIN}"
    chmod +x "${INSTALL_DIR}/${GUI_BIN}"
  else
    sudo curl -fsSL "$GUI_URL" -o "${INSTALL_DIR}/${GUI_BIN}"
    sudo chmod +x "${INSTALL_DIR}/${GUI_BIN}"
  fi
  ok "aifs-gui installed to ${INSTALL_DIR}/${GUI_BIN}"
fi

# ─── Phase 2: Dependency checks & guided install ────────────────────

NEEDS_POLICY_JSON=false

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
    if podman_machine_running; then
      ok "Podman machine is configured and running"
    else
      warn "Podman machine is configured but not running — starting..."
      podman machine start
      if podman_machine_running; then
        ok "Podman machine started"
      else
        warn "Podman machine did not report running; run 'podman machine start' manually"
      fi
    fi
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
      if is_apple_silicon; then
        echo ""
        info "Apple Silicon note: macFUSE requires the boot volume to use a"
        info "${BOLD}Reduced Security${RESET} policy. In Recovery mode, run"
        info "'Startup Security Utility → Set Security Policy → Reduced Security',"
        info "enable 'Allow user management of kernel extensions', then reboot."
      fi
      echo ""
    fi
  fi

elif $IS_LINUX; then
  # -- Linux dependencies -------------------------------------------------

  step "Checking Linux dependencies..."

  # Ensure ~/.local/bin exists and is on PATH.
  LOCAL_BIN="$HOME/.local/bin"
  if [ ! -d "$LOCAL_BIN" ]; then
    mkdir -p "$LOCAL_BIN" && info "Created $LOCAL_BIN"
  fi
  case ":$PATH:" in
    *:"$LOCAL_BIN":*) ;;
    *)
      warn "$LOCAL_BIN is not on your PATH"
      info "Add this to your shell profile (~/.bashrc / ~/.zshrc):"
      info "  export PATH="\$HOME/.local/bin:\$PATH""
      ;;
  esac

  # --- Podman (static podman-launcher from GitHub) ---
  PODMAN_URL="https://github.com/89luca89/podman-launcher/releases/latest/download/podman-launcher-amd64"
  PODMAN_BIN="$LOCAL_BIN/podman"

  if has_podman; then
    PODMAN_VER="$(podman --version 2>/dev/null || echo 'unknown')"
    ok "Podman found ($PODMAN_VER)"
  else
    warn "Podman not found -- downloading static podman-launcher from GitHub..."
    info "$PODMAN_URL"
    if curl -fsSL -o "$PODMAN_BIN" "$PODMAN_URL"; then
      chmod +x "$PODMAN_BIN"
      ok "Podman installed to $PODMAN_BIN ($(podman --version 2>/dev/null || echo 'ok'))"
    else
      die "Failed to download podman-launcher. Check network and retry."
    fi
  fi

  # podman-launcher needs newuidmap/newgidmap from the uidmap package.
  if ! command -v newuidmap >/dev/null 2>&1; then
    warn "newuidmap not found -- needed for rootless user namespace mapping, installing..."
    if install_pkgs uidmap; then
      ok "uidmap installed"
    else
      cmd_hint "Install the uidmap package for your distro, then re-run this script"
      die "uidmap (newuidmap/newgidmap) is required for rootless podman"
    fi
  fi

  # --- /etc/containers/policy.json (required by podman to pull images) ---
  POLICY_FILE="/etc/containers/policy.json"
  POLICY_DEFAULT='{"default":[{"type":"insecureAcceptAnything"}]}'
  if [ -f "$POLICY_FILE" ]; then
    ok "$POLICY_FILE exists"
  else
    warn "$POLICY_FILE not found — podman may refuse to pull images"
    # Try to create it. Requires root; fall back gracefully if we can't.
    if mkdir -p /etc/containers 2>/dev/null && printf '%s\n' "$POLICY_DEFAULT" > "$POLICY_FILE" 2>/dev/null; then
      ok "Created $POLICY_FILE"
    elif sudo mkdir -p /etc/containers 2>/dev/null && printf '%s\n' "$POLICY_DEFAULT" | sudo tee "$POLICY_FILE" >/dev/null 2>&1; then
      ok "Created $POLICY_FILE (via sudo)"
    else
      warn "Could not create $POLICY_FILE (no write permission)"
      NEEDS_POLICY_JSON=true
    fi
  fi

  # --- fusermount3 (FUSE) ---
  if has_fusermount; then
    FUSE_BIN="$(command -v fusermount3 2>/dev/null || command -v fusermount)"
    ok "FUSE umount helper found: ${FUSE_BIN}"
  else
    warn "fusermount3 not found — needed for 'aifs umount', installing..."
    # Distro-specific package names for the fuse3 userspace helper.
    # detect_linux_distro normalizes derivatives to a family token.
    DISTRO="$(detect_linux_distro)"
    case "$DISTRO" in
      debian)   PKG="fuse3" ;;
      fedora|rhel) PKG="fuse3" ;;   # fuse3 + fuse3-libs pulled in as deps
      arch)     PKG="fuse3" ;;
      alpine)   PKG="fuse3" ;;
      suse)     PKG="fuse3" ;;
      *)        PKG="fuse3" ;;       # sensible default; install_pkgs will fail cleanly
    esac
    if install_pkgs "$PKG"; then
      ok "fuse3 installed ($PKG)"
    else
      warn "Could not auto-install $PKG"
      cmd_hint "Install the fuse3 package for your distro (e.g. 'fuse3' on Debian/Arch, 'fuse3-libs' on RHEL)"
      warn "'aifs umount' will not work without fusermount3/fusermount"
    fi
  fi

  # --- GUI runtime libraries (WebKit2GTK + GTK3) ---
  # Required to run the aifs-gui binary on Linux.
  # (To build from source you additionally need the -dev/-devel counterparts.)
  DISTRO="$(detect_linux_distro)"
  case "$DISTRO" in
    debian)      GUI_PKGS="libgtk-3-0 libwebkit2gtk-4.0-37" ;;
    fedora|rhel) GUI_PKGS="gtk3 webkit2gtk4.0" ;;
    arch)        GUI_PKGS="gtk3 webkit2gtk" ;;
    suse)        GUI_PKGS="libgtk-3-0 libwebkit2gtk-4_0-37" ;;
    alpine)      GUI_PKGS="" ;;  # webkit2gtk not in Alpine main repos
    *)           GUI_PKGS="libgtk-3-0 libwebkit2gtk-4.0-37" ;;
  esac
  if [ -n "$GUI_PKGS" ]; then
    # Check if webkit2gtk is already available (proxy for both libs being present).
    _webkit_ok=false
    case "$DISTRO" in
      debian) ldconfig -p 2>/dev/null | grep -q libwebkit2gtk && _webkit_ok=true ;;
      *)      command -v pkg-config >/dev/null 2>&1 && pkg-config --exists webkit2gtk-4.0 2>/dev/null && _webkit_ok=true ;;
    esac
    if $_webkit_ok; then
      ok "GUI runtime libraries (GTK3 + WebKit2GTK) found"
    else
      warn "GUI runtime libraries not found — installing..."
      # shellcheck disable=SC2086
      if install_pkgs $GUI_PKGS; then
        ok "GUI runtime libraries installed ($GUI_PKGS)"
      else
        warn "Could not auto-install GUI libraries"
        cmd_hint "Install manually: $GUI_PKGS"
        warn "aifs-gui will not run without GTK3 and WebKit2GTK"
      fi
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
echo "  aifs format -i <instance-name>"
echo "  mkdir -p ~/mnt && aifs mount -i <instance-name> ~/mnt"
echo ""
if ! $GUI_SKIP; then
  echo "${BOLD}GUI:${RESET}"
  echo "  aifs-gui"
  echo ""
fi

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

if $NEEDS_POLICY_JSON; then
  echo ""
  warn "/etc/containers/policy.json is missing and could not be created automatically."
  warn "Podman will refuse to pull images without it. Create it with:"
  echo ""
  echo "  ${BOLD}sudo mkdir -p /etc/containers${RESET}"
  echo "  ${BOLD}echo '{\"default\":[{\"type\":\"insecureAcceptAnything\"}]}' | sudo tee /etc/containers/policy.json${RESET}"
  echo ""
  warn "Then re-run 'aifs start -i <instance-name>' to pull the required images."
  echo ""
fi
