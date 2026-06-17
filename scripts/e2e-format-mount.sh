#!/bin/bash
# scripts/e2e-format-mount.sh — Standalone format/mount smoke test.
#
# Uses an isolated base directory and config file so it does not interfere
# with the user's normal ~/.aifs environment.
#
# Usage:
#   ./scripts/e2e-format-mount.sh [instance_name]
#
# Environment:
#   AIFS_BIN    path to aifs binary (default: ./build/aifs)

set -euo pipefail

INSTANCE="${1:-fmtmnt}"
AIFS_BIN="${AIFS_BIN:-./build/aifs}"

# Use a unique backup container / network name so this test does not collide
# with an existing aifs environment.
SUFFIX="fmtmnt-$$"
BACKUP_CONTAINER="aifs-backup-${SUFFIX}"
NETWORK_NAME="aifs-net-${SUFFIX}"

WORK_DIR="$(mktemp -d /tmp/aifs-fmt-mnt-XXXXXX)"
CONFIG="${WORK_DIR}/config.yaml"
MOUNT_POINT="${WORK_DIR}/mnt"

cd "$(dirname "$0")/.."

if [[ ! -x "$AIFS_BIN" ]]; then
    echo "Error: $AIFS_BIN not found. Build it first:" >&2
    echo "  go build -o build/aifs ./cmd/aifs" >&2
    exit 1
fi

# Pick a free host port to avoid colliding with an existing aifs instance.
find_free_port() {
    python3 -c 'import socket; s=socket.socket(); s.bind(("",0)); print(s.getsockname()[1]); s.close()'
}

# Remove any leftover containers from a previous interrupted run.
podman rm -f "aifs-pg-${INSTANCE}" "$BACKUP_CONTAINER" 2>/dev/null || true

cleanup() {
    set +e
    echo ""
    echo "→ Cleaning up..."
    if [[ -d "$MOUNT_POINT" ]]; then
        "$AIFS_BIN" -c "$CONFIG" umount "$MOUNT_POINT" 2>/dev/null || true
    fi
    "$AIFS_BIN" -c "$CONFIG" stop -i "$INSTANCE" 2>/dev/null || true
    podman rm -f "aifs-pg-${INSTANCE}" "$BACKUP_CONTAINER" 2>/dev/null || true
    podman network rm -f "$NETWORK_NAME" 2>/dev/null || true
    if command -v podman >/dev/null 2>&1; then
        podman unshare rm -rf "$WORK_DIR" 2>/dev/null || rm -rf "$WORK_DIR" 2>/dev/null || true
    else
        rm -rf "$WORK_DIR"
    fi
}
trap cleanup EXIT

FORCE_CLEAN="${FORCE_CLEAN:-0}"
if [[ "$FORCE_CLEAN" != "1" ]]; then
    echo "⚠️  This script will create an isolated aifs environment under ${WORK_DIR}."
    echo "    It will be automatically cleaned up when the script exits."
    read -rp "Continue? [y/N]: " ans
    if [[ "$ans" != [yY]* ]]; then
        echo "Cancelled"
        exit 0
    fi
fi

echo "=== aifs format/mount standalone smoke test ==="
echo "Instance:       ${INSTANCE}"
echo "Work dir:       ${WORK_DIR}"
echo "Backup network: ${NETWORK_NAME}"
echo "Backup container: ${BACKUP_CONTAINER}"
echo ""

echo "=== 1. config init ==="
"$AIFS_BIN" config init -o "$CONFIG" --add "$INSTANCE" --base-dir "$WORK_DIR"

# Isolate the backup container and network from any existing aifs environment.
sed -i "s/^network: aifs-net$/network: ${NETWORK_NAME}/" "$CONFIG"
sed -i "s/^\\( *container_name:\\) aifs-backup$/\\1 ${BACKUP_CONTAINER}/" "$CONFIG"

# Assign a free host port so this test does not collide with an existing PG instance.
HOST_PORT=$(find_free_port)
sed -i "s/^\\( *host_port:\\) .*/\\1 ${HOST_PORT}/" "$CONFIG"

echo ""
echo "=== 2. start instance ==="
"$AIFS_BIN" -c "$CONFIG" start -i "$INSTANCE"

echo ""
echo "=== 3. format filesystem ==="
"$AIFS_BIN" -c "$CONFIG" format -i "$INSTANCE" --volume "$INSTANCE"

echo ""
echo "=== 4. mount filesystem (-d background) ==="
mkdir -p "$MOUNT_POINT"
"$AIFS_BIN" -c "$CONFIG" mount -i "$INSTANCE" "$MOUNT_POINT" -d
sleep 2

echo ""
echo "=== 5. filesystem smoke tests ==="
echo "hello aifs" > "$MOUNT_POINT/hello.txt"
mkdir "$MOUNT_POINT/subdir"
ln -s hello.txt "$MOUNT_POINT/hello-link"

if [[ "$(cat "$MOUNT_POINT/hello.txt")" != "hello aifs" ]]; then
    echo "✗ read back file content mismatch" >&2
    exit 1
fi
if [[ ! -d "$MOUNT_POINT/subdir" ]]; then
    echo "✗ directory creation failed" >&2
    exit 1
fi
if [[ ! -L "$MOUNT_POINT/hello-link" ]]; then
    echo "✗ symlink creation failed" >&2
    exit 1
fi
if [[ "$(readlink "$MOUNT_POINT/hello-link")" != "hello.txt" ]]; then
    echo "✗ symlink target mismatch" >&2
    exit 1
fi
echo "  ✓ write / mkdir / symlink / readlink passed"

echo ""
echo "=== 6. umount filesystem ==="
"$AIFS_BIN" -c "$CONFIG" umount "$MOUNT_POINT"

echo ""
echo "=== 7. remount and verify persistence ==="
"$AIFS_BIN" -c "$CONFIG" mount -i "$INSTANCE" "$MOUNT_POINT" -d
sleep 2

if [[ "$(cat "$MOUNT_POINT/hello.txt")" != "hello aifs" ]]; then
    echo "✗ persisted file content mismatch" >&2
    exit 1
fi
if [[ ! -d "$MOUNT_POINT/subdir" ]]; then
    echo "✗ persisted directory missing" >&2
    exit 1
fi
echo "  ✓ persistence after remount passed"

echo ""
echo "✓ format/mount smoke test completed successfully"
