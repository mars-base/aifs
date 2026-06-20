#!/bin/bash
# test-pitr.sh — End-to-end PITR test for aifs.
#
# Usage:
#   ./scripts/test-pitr.sh [instance_name]
#
# Environment:
#   AIFS_BIN    path to aifs binary (default: ./build/aifs)
#
# The script uses an isolated work directory and config file so it does not
# interfere with an existing aifs environment.
#
# The script:
#   1. Creates (or recreates) a PG instance
#   2. Writes some initial rows
#   3. Takes a full pgBackRest backup
#   4. Continues writing rows in the background
#   5. Records a target restore time while rows are being inserted
#   6. Stops the writer and takes a final row count
#   7. Restores the instance to the recorded target time
#   8. Verifies the row count matches the count at the target time

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

INSTANCE="${1:-proj01}"
AIFS_BIN="${AIFS_BIN:-./build/aifs}"

# Isolate backup container from any existing aifs environment.
SUFFIX="pitr-$$"
BACKUP_CONTAINER="aifs-backup-${SUFFIX}"

WORK_DIR="$(make_work_dir aifs-pitr)"
CONFIG="${WORK_DIR}/config.yaml"

PRE_ROWS=10
WRITE_SECONDS=40
POST_SECONDS=30

CONTAINER="aifs-pg-${INSTANCE}"
DB="${INSTANCE}_db"

echo "=== aifs PITR end-to-end test ==="
echo "Instance:       ${INSTANCE}"
echo "Binary:         ${AIFS_BIN}"
echo "Work dir:       ${WORK_DIR}"
echo "Backup container: ${BACKUP_CONTAINER}"
echo ""

cd "$(dirname "$0")/.."

if [[ ! -x "$AIFS_BIN" ]]; then
    echo "Error: $AIFS_BIN binary not found. Run: go build -o $AIFS_BIN ./cmd/aifs/" >&2
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
    # Remove backup container first (not managed by aifs destroy) to avoid
    # leaving it behind when destroy hangs.
    podman rm -f "$BACKUP_CONTAINER" 2>/dev/null || true
    "$AIFS_BIN" -c "$CONFIG" destroy -i "${INSTANCE}" --clean-data --force >/dev/null 2>&1 || true
    podman rm -f "$CONTAINER" 2>/dev/null || true
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

echo "→ Generating isolated config..."
"$AIFS_BIN" config init -o "$CONFIG" --add "$INSTANCE" --base-dir "$WORK_DIR"

# Use unique backup container name so we do not touch an existing aifs setup.
sedi "s/^\\( *container_name:\\) aifs-backup$/\\1 ${BACKUP_CONTAINER}/" "$CONFIG"

# Assign a free host port so this test does not collide with an existing PG instance.
HOST_PORT=$(find_free_port)
sedi "s/^\\( *host_port:\\) .*/\\1 ${HOST_PORT}/" "$CONFIG"

echo "→ Creating and starting instance ${INSTANCE}..."
"$AIFS_BIN" -c "$CONFIG" start -i "${INSTANCE}"

echo "→ Creating restore_test table..."
podman exec "${CONTAINER}" psql -U aifs -d "${DB}" -c "DROP TABLE IF EXISTS restore_test;" >/dev/null
podman exec "${CONTAINER}" psql -U aifs -d "${DB}" -c "CREATE TABLE restore_test (id serial primary key, t timestamp default now(), note text);" >/dev/null

echo "→ Inserting ${PRE_ROWS} pre-backup rows..."
for i in $(seq 1 "${PRE_ROWS}"); do
    podman exec "${CONTAINER}" psql -U aifs -d "${DB}" -c "INSERT INTO restore_test(note) VALUES ('pre_${i}');" >/dev/null
done

echo "→ Taking full backup..."
"$AIFS_BIN" -c "$CONFIG" snapshot create -i "${INSTANCE}" --type full

echo "→ Starting background writer (1 row/sec)..."
local_writer_script=$(cat <<'EOF'
for i in $(seq 1 300); do
    psql -U aifs -d "${DB}" -c "INSERT INTO restore_test(note) VALUES ('post_'||$i);" >/dev/null 2>&1 || true
    sleep 1
done
EOF
)
podman exec -d "${CONTAINER}" bash -c "DB=${DB}; ${local_writer_script}"

echo "→ Waiting ${WRITE_SECONDS}s to reach target restore time..."
sleep "${WRITE_SECONDS}"
TARGET_TIME_UTC=$(date -u '+%Y-%m-%d %H:%M:%S+00')
echo "  Target restore time (UTC): ${TARGET_TIME_UTC}"

echo "→ Counting rows before target time..."
EXPECTED_ROWS=$(podman exec "${CONTAINER}" psql -U aifs -d "${DB}" -t -c "SELECT count(*) FROM restore_test WHERE t < '${TARGET_TIME_UTC}';" | xargs)
echo "  Expected rows after restore: ${EXPECTED_ROWS}"

echo "→ Waiting ${POST_SECONDS}s before stopping writer..."
sleep "${POST_SECONDS}"

echo "→ Stopping background writer..."
podman exec "${CONTAINER}" pkill -f 'INSERT INTO restore_test' >/dev/null 2>&1 || true

echo "→ Final row count before restore:"
FINAL_ROWS=$(podman exec "${CONTAINER}" psql -U aifs -d "${DB}" -t -c "SELECT count(*) FROM restore_test;" | xargs)
echo "  ${FINAL_ROWS}"

echo "→ Restoring to ${TARGET_TIME_UTC}..."
"$AIFS_BIN" -c "$CONFIG" restore -i "${INSTANCE}" --time "${TARGET_TIME_UTC}" --force

echo "→ Waiting for PostgreSQL to be ready..."
sleep 5

echo "→ Verifying restored row count..."
RESTORED_ROWS=$(podman exec "${CONTAINER}" psql -U aifs -d "${DB}" -t -c "SELECT count(*) FROM restore_test;" | xargs)
RESTORED_MAX_T=$(podman exec "${CONTAINER}" psql -U aifs -d "${DB}" -t -c "SELECT max(t) FROM restore_test;" | xargs)

echo ""
echo "=== Results ==="
echo "  Instance:           ${INSTANCE}"
echo "  Target time (UTC):  ${TARGET_TIME_UTC}"
echo "  Expected rows:      ${EXPECTED_ROWS}"
echo "  Final rows:         ${FINAL_ROWS}"
echo "  Restored rows:      ${RESTORED_ROWS}"
echo "  Restored max(t):    ${RESTORED_MAX_T}"

if [[ "${RESTORED_ROWS}" == "${EXPECTED_ROWS}" ]]; then
    echo ""
    echo "✓ PITR test PASSED"
    exit 0
else
    echo ""
    echo "✗ PITR test FAILED: restored row count ${RESTORED_ROWS} != expected ${EXPECTED_ROWS}"
    exit 1
fi
