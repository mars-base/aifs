#!/bin/bash
# scripts/e2e-pitr-fs.sh — End-to-end PITR test through the aifs filesystem.
#
# Usage:
#   ./scripts/e2e-pitr-fs.sh [instance_name]
#
# Environment:
#   AIFS_BIN    path to aifs binary (default: ./build/aifs)
#
# The script uses an isolated work directory and config file. It exercises:
#   config init → start → format → mount → write pre-backup files →
#   full snapshot → write post-backup files → record PITR target time →
#   write final files → umount → restore → remount → verify file-level rollback.

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

INSTANCE="${1:-pitrfs}"
AIFS_BIN="${AIFS_BIN:-./build/aifs}"

# Use a unique backup container name so this test does not collide
# with an existing aifs environment.
SUFFIX="pitrfs-$$"
BACKUP_CONTAINER="aifs-backup-${SUFFIX}"

WORK_DIR="$(make_work_dir aifs-pitr-fs)"
CONFIG="${WORK_DIR}/config.yaml"
MOUNT_POINT="${WORK_DIR}/mnt"

CONTAINER="aifs-pg-${INSTANCE}"

fail() {
    echo "✗ $*" >&2
    exit 1
}

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

cleanup() {
    set +e
    echo ""
    echo "→ Cleaning up..."
    if [[ -d "$MOUNT_POINT" ]]; then
        "$AIFS_BIN" -c "$CONFIG" umount "$MOUNT_POINT" 2>/dev/null || true
    fi
    "$AIFS_BIN" -c "$CONFIG" stop -i "$INSTANCE" 2>/dev/null || true
    podman rm -f "$CONTAINER" "$BACKUP_CONTAINER" 2>/dev/null || true
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

echo "=== aifs filesystem PITR end-to-end test ==="
echo "Instance:       ${INSTANCE}"
echo "Work dir:       ${WORK_DIR}"
echo "Backup container: ${BACKUP_CONTAINER}"
echo ""

echo "=== 1. config init ==="
"$AIFS_BIN" config init -o "$CONFIG" --add "$INSTANCE" --base-dir "$WORK_DIR"

# Isolate the backup container from any existing aifs environment.
sedi "s/^\\( *container_name:\\) aifs-backup$/\\1 ${BACKUP_CONTAINER}/" "$CONFIG"

# Assign a free host port so this test does not collide with an existing PG instance.
HOST_PORT=$(find_free_port)
sedi "s/^\( *host_port:\) .*/\1 ${HOST_PORT}/" "$CONFIG"

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
echo "=== 5. write pre-backup files ==="
echo "before backup" > "$MOUNT_POINT/file-before.txt"
mkdir -p "$MOUNT_POINT/dir1"
echo "before backup in dir1" > "$MOUNT_POINT/dir1/before.txt"
[[ "$(cat "$MOUNT_POINT/file-before.txt")" == "before backup" ]] || fail "pre-backup file content mismatch"
[[ -d "$MOUNT_POINT/dir1" ]] || fail "pre-backup directory missing"
echo "  ✓ pre-backup files written"

echo ""
echo "=== 6. take full snapshot ==="
"$AIFS_BIN" -c "$CONFIG" snapshot create -i "$INSTANCE" --type full --tail-logs

echo ""
echo "=== 7. write post-backup files ==="
echo "after backup" > "$MOUNT_POINT/file-after.txt"
echo "after backup in dir1" > "$MOUNT_POINT/dir1/after.txt"
[[ -f "$MOUNT_POINT/file-after.txt" ]] || fail "post-backup file missing"
[[ -f "$MOUNT_POINT/dir1/after.txt" ]] || fail "post-backup dir1 file missing"
echo "  ✓ post-backup files written"

# Give WAL archiving a moment to advance past the post-backup writes.
sleep 2
TARGET_TIME_UTC=$(date -u '+%Y-%m-%d %H:%M:%S+00')
echo ""
echo "=== 8. recorded PITR target time (UTC): ${TARGET_TIME_UTC} ==="

# Continue writing files that should disappear after restore.
sleep 2
echo "final after target" > "$MOUNT_POINT/file-final.txt"
echo "final after target in dir1" > "$MOUNT_POINT/dir1/final.txt"
[[ -f "$MOUNT_POINT/file-final.txt" ]] || fail "final file missing"
echo "  ✓ final files written (should be rolled back)"

# Let the final writes be archived.
sleep 2

echo ""
echo "=== 9. umount before restore ==="
"$AIFS_BIN" -c "$CONFIG" umount "$MOUNT_POINT"

echo ""
echo "=== 10. restore to ${TARGET_TIME_UTC} ==="
"$AIFS_BIN" -c "$CONFIG" restore -i "$INSTANCE" --time "$TARGET_TIME_UTC" --force

echo ""
echo "=== 11. wait for PostgreSQL to be ready ==="
for i in {1..60}; do
    if podman exec "$CONTAINER" pg_isready -U aifs -d "${INSTANCE}_db" >/dev/null 2>&1; then
        echo "  ✓ PostgreSQL ready"
        break
    fi
    sleep 1
done

echo ""
echo "=== 12. remount filesystem ==="
"$AIFS_BIN" -c "$CONFIG" mount -i "$INSTANCE" "$MOUNT_POINT" -d
sleep 2

echo ""
echo "=== 13. verify file-level rollback ==="

# Files written before and right after the backup must still exist.
[[ -f "$MOUNT_POINT/file-before.txt" ]] || fail "file-before.txt missing after restore"
[[ "$(cat "$MOUNT_POINT/file-before.txt")" == "before backup" ]] || fail "file-before.txt content changed"

[[ -f "$MOUNT_POINT/dir1/before.txt" ]] || fail "dir1/before.txt missing after restore"
[[ "$(cat "$MOUNT_POINT/dir1/before.txt")" == "before backup in dir1" ]] || fail "dir1/before.txt content changed"

[[ -f "$MOUNT_POINT/file-after.txt" ]] || fail "file-after.txt missing after restore"
[[ "$(cat "$MOUNT_POINT/file-after.txt")" == "after backup" ]] || fail "file-after.txt content changed"

[[ -f "$MOUNT_POINT/dir1/after.txt" ]] || fail "dir1/after.txt missing after restore"
[[ "$(cat "$MOUNT_POINT/dir1/after.txt")" == "after backup in dir1" ]] || fail "dir1/after.txt content changed"

# Files written after the target time must be gone.
[[ ! -e "$MOUNT_POINT/file-final.txt" ]] || fail "file-final.txt should have been rolled back"
[[ ! -e "$MOUNT_POINT/dir1/final.txt" ]] || fail "dir1/final.txt should have been rolled back"

echo "  ✓ pre-target files preserved, post-target files rolled back"

echo ""
echo "✓ aifs filesystem PITR end-to-end test completed successfully"
