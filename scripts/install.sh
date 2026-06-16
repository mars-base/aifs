#!/bin/sh
# aifs one-line installer for Linux / macOS
set -e

REPO="mars-base/aifs"
BIN="aifs"
INSTALL_DIR="/usr/local/bin"

# Detect OS and arch
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Fetch latest release tag
TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$TAG" ]; then
  echo "Failed to determine latest release tag"
  exit 1
fi

URL="https://github.com/${REPO}/releases/latest/download/${BIN}-${OS}-${ARCH}"
echo "Downloading aifs ${TAG} (${OS}-${ARCH})..."
echo "  ${URL}"

if [ "$(id -u)" -eq 0 ]; then
  # Running as root
  curl -fsSL "$URL" -o "${INSTALL_DIR}/${BIN}"
  chmod +x "${INSTALL_DIR}/${BIN}"
else
  # User install
  if [ -w "$INSTALL_DIR" ]; then
    curl -fsSL "$URL" -o "${INSTALL_DIR}/${BIN}"
    chmod +x "${INSTALL_DIR}/${BIN}"
  else
    sudo curl -fsSL "$URL" -o "${INSTALL_DIR}/${BIN}"
    sudo chmod +x "${INSTALL_DIR}/${BIN}"
  fi
fi

echo ""
echo "✓ aifs ${TAG} installed to ${INSTALL_DIR}/${BIN}"
echo "  Run: aifs version"

# Pull the small helper image used by `aifs destroy --clean-data` to remove
# rootless-podman data directories that contain subordinate-UID files.
if command -v podman >/dev/null 2>&1; then
	echo ""
	echo "→ Pulling helper image (alpine:3.20) for destroy --clean-data..."
	podman pull docker.io/library/alpine:3.20 >/dev/null 2>&1 || echo "  ⚠️  optional helper image pull failed, will retry on first use"
fi
