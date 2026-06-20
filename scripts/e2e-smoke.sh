#!/bin/bash
# scripts/e2e-smoke.sh — End-to-end smoke test for aifs lifecycle.
#
# Usage:
#   ./scripts/e2e-smoke.sh [instance_name]
#
# Environment:
#   AIFS_BIN    path to aifs binary (default: ./build/aifs)
#   FORCE_CLEAN set to 1 to skip the confirmation prompt
#
# The script uses an isolated work directory and config file. It exercises:
#   config init / validate / show / create / list / destroy / start / status /
#   backup status / snapshot create / list / stop / restart / final cleanup

set -euo pipefail

# ─── Platform helpers ──────────────────────────────────────────────
IS_MACOS=false
[[ "$(uname -s)" == "Darwin" ]] && IS_MACOS=true

# make_work_dir creates a temp directory accessible from the podman VM
# on both Linux (/tmp) and macOS ($HOME/tmp, since /tmp is not shared).
make_work_dir() {
    local prefix="${1:-aifs-test}"
    if $IS_MACOS; then
        mkdir -p "$HOME/tmp"
        mktemp -d "$HOME/tmp/${prefix}-XXXXXX"
    else
        mktemp -d "/tmp/${prefix}-XXXXXX"
    fi
}

# sedi is a cross-platform in-place sed.
# Detects sed flavour at runtime: GNU sed uses -i, BSD sed requires -i ''.
sedi() {
    if sed --version 2>/dev/null | grep -q GNU; then
        sed -i "$@"
    else
        sed -i '' "$@"
    fi
}

INSTANCE="${1:-smoke}"
DB="${INSTANCE}_db"
CONTAINER="aifs-pg-${INSTANCE}"
AIFS_BIN="${AIFS_BIN:-./build/aifs}"
FORCE_CLEAN="${FORCE_CLEAN:-0}"

# Use unique backup container name so this test does not collide
# with an existing aifs environment.
SUFFIX="smoke-$$"
BACKUP_CONTAINER="aifs-backup-${SUFFIX}"

WORK_DIR="$(make_work_dir aifs-smoke)"
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
podman rm -f "$CONTAINER" "$BACKUP_CONTAINER" 2>/dev/null || true

cleanup() {
    set +e
    echo ""
    echo "→ Cleaning up..."
    if [[ -d "$MOUNT_POINT" ]]; then
        "$AIFS_BIN" -c "$CONFIG" umount "$MOUNT_POINT" 2>/dev/null || true
    fi
    # Remove backup container first (not managed by aifs destroy) to avoid
    # leaving it behind when destroy hangs.
    podman rm -f "$BACKUP_CONTAINER" aifs-pg-smoke01 2>/dev/null || true
    "$AIFS_BIN" -c "$CONFIG" destroy -i "$INSTANCE" --clean-data --force 2>/dev/null || true
    podman rm -f "$CONTAINER" 2>/dev/null || true
    if command -v podman >/dev/null 2>&1; then
        podman unshare rm -rf "$WORK_DIR" 2>/dev/null || rm -rf "$WORK_DIR" 2>/dev/null || true
    else
        rm -rf "$WORK_DIR"
    fi
}
trap cleanup EXIT

if [[ "$FORCE_CLEAN" != "1" ]]; then
    echo "⚠️  This script will create an isolated aifs environment under ${WORK_DIR}."
    echo "    It will be automatically cleaned up when the script exits."
    read -rp "Continue? [y/N]: " ans
    if [[ "$ans" != [yY]* ]]; then
        echo "Cancelled"
        exit 0
    fi
fi

echo ""
echo "=== aifs e2e smoke test ==="
echo "Instance:       ${INSTANCE}"
echo "Work dir:       ${WORK_DIR}"
echo "Backup container: ${BACKUP_CONTAINER}"
echo ""

echo "=== 1. config init ==="
"$AIFS_BIN" config init -o "$CONFIG" --add "$INSTANCE" --base-dir "$WORK_DIR"

# Isolate backup container from any existing aifs environment.
sedi "s/^\\( *container_name:\\) aifs-backup$/\\1 ${BACKUP_CONTAINER}/" "$CONFIG"

# Assign a free host port so this test does not collide with an existing PG instance.
HOST_PORT=$(find_free_port)
sedi "s/^\\( *host_port:\\) .*/\\1 ${HOST_PORT}/" "$CONFIG"

echo ""
echo "=== 2. config validate ==="
"$AIFS_BIN" -c "$CONFIG" config validate

echo ""
echo "=== 3. config show ==="
"$AIFS_BIN" -c "$CONFIG" config show

echo ""
echo "=== 4. create extra instance (smoke01) ==="
"$AIFS_BIN" -c "$CONFIG" create -i smoke01

echo ""
echo "=== 5. list instances ==="
"$AIFS_BIN" -c "$CONFIG" list

echo ""
echo "=== 6. destroy extra instance ==="
"$AIFS_BIN" -c "$CONFIG" destroy -i smoke01 --force

echo ""
echo "=== 7. start ${INSTANCE} instance ==="
"$AIFS_BIN" -c "$CONFIG" start -i "$INSTANCE"

echo ""
echo "=== 8. status ==="
"$AIFS_BIN" -c "$CONFIG" status -i "$INSTANCE"

echo ""
echo "=== 9. format filesystem ==="
"$AIFS_BIN" -c "$CONFIG" format -i "$INSTANCE" --volume "$INSTANCE"

mkdir -p "$MOUNT_POINT"

echo ""
echo "=== 10. mount filesystem ==="
"$AIFS_BIN" -c "$CONFIG" mount -i "$INSTANCE" "$MOUNT_POINT" -d
sleep 2

echo ""
echo "=== 11. filesystem smoke (write/read/mkdir/symlink) ==="
echo "hello aifs" > "$MOUNT_POINT/hello.txt"
mkdir "$MOUNT_POINT/subdir"
ln -s hello.txt "$MOUNT_POINT/hello-link"
[[ "$(cat "$MOUNT_POINT/hello.txt")" == "hello aifs" ]]
[[ -d "$MOUNT_POINT/subdir" ]]
[[ -L "$MOUNT_POINT/hello-link" ]]
echo "  ✓ filesystem operations passed"

echo ""
echo "=== 12. umount filesystem ==="
"$AIFS_BIN" -c "$CONFIG" umount "$MOUNT_POINT"

echo ""
echo "=== 13. backup status ==="
"$AIFS_BIN" -c "$CONFIG" backup status

echo ""
echo "=== 14. create test table and insert data ==="
podman exec "$CONTAINER" psql -U aifs -d "$DB" -c "DROP TABLE IF EXISTS smoke_test;" >/dev/null
podman exec "$CONTAINER" psql -U aifs -d "$DB" -c "CREATE TABLE smoke_test(id serial primary key, note text);" >/dev/null
podman exec "$CONTAINER" psql -U aifs -d "$DB" -c "INSERT INTO smoke_test(note) VALUES ('before-snapshot');" >/dev/null
echo "  ✓ inserted 1 row"

echo ""
echo "=== 15. snapshot create (full, tail-logs) ==="
"$AIFS_BIN" -c "$CONFIG" snapshot create -i "$INSTANCE" --tail-logs

echo ""
echo "=== 16. snapshot list ==="
"$AIFS_BIN" -c "$CONFIG" snapshot list -i "$INSTANCE"

echo ""
echo "=== 17. stop ${INSTANCE} ==="
"$AIFS_BIN" -c "$CONFIG" stop -i "$INSTANCE"

echo ""
echo "=== 18. restart ${INSTANCE} ==="
"$AIFS_BIN" -c "$CONFIG" start -i "$INSTANCE"

echo ""
echo "=== 19. status after restart ==="
"$AIFS_BIN" -c "$CONFIG" status -i "$INSTANCE"

echo ""
echo "=== 20. snapshot after restart (incr, tail-logs) ==="
"$AIFS_BIN" -c "$CONFIG" snapshot create -i "$INSTANCE" --type incr --tail-logs

echo ""
echo "=== 21. destroy ${INSTANCE} (with clean-data) ==="
"$AIFS_BIN" -c "$CONFIG" destroy -i "$INSTANCE" --clean-data --force || {
    echo "  ⚠ destroy --clean-data returned an error, will force-cleanup at the end"
}

echo ""
echo "=== 22. stop backup container ==="
"$AIFS_BIN" -c "$CONFIG" backup stop

echo ""
echo "✓ aifs e2e smoke test completed successfully"
