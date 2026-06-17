#!/bin/bash
# test-pitr.sh — End-to-end PITR test for aifs.
#
# Usage:
#   ./scripts/test-pitr.sh [instance_name]
#
# Environment:
#   AIFS_BIN    path to aifs binary (default: ./aifs)
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

INSTANCE="${1:-proj01}"
CONTAINER="aifs-pg-${INSTANCE}"
DB="${INSTANCE}_db"
AIFS_BIN="${AIFS_BIN:-./aifs}"
PRE_ROWS=10
WRITE_SECONDS=40
POST_SECONDS=30

echo "=== aifs PITR end-to-end test ==="
echo "Instance: ${INSTANCE}"
echo "Binary:   ${AIFS_BIN}"
echo ""

cd "$(dirname "$0")/.."

if [[ ! -x "$AIFS_BIN" ]]; then
    echo "Error: $AIFS_BIN binary not found. Run: go build -o $AIFS_BIN ./cmd/aifs/" >&2
    exit 1
fi

cleanup_instance() {
    echo "→ Cleaning up instance ${INSTANCE} (if it exists)..."
    "$AIFS_BIN" destroy -i "${INSTANCE}" --clean-data --force >/dev/null 2>&1 || true
}

cleanup_instance

echo "→ Creating instance ${INSTANCE}..."
"$AIFS_BIN" create -i "${INSTANCE}"

echo "→ Starting instance ${INSTANCE}..."
"$AIFS_BIN" start -i "${INSTANCE}"

echo "→ Creating restore_test table..."
podman exec "${CONTAINER}" psql -U aifs -d "${DB}" -c "DROP TABLE IF EXISTS restore_test;" >/dev/null
podman exec "${CONTAINER}" psql -U aifs -d "${DB}" -c "CREATE TABLE restore_test (id serial primary key, t timestamp default now(), note text);" >/dev/null

echo "→ Inserting ${PRE_ROWS} pre-backup rows..."
for i in $(seq 1 "${PRE_ROWS}"); do
    podman exec "${CONTAINER}" psql -U aifs -d "${DB}" -c "INSERT INTO restore_test(note) VALUES ('pre_${i}');" >/dev/null
done

echo "→ Taking full backup..."
"$AIFS_BIN" snapshot create -i "${INSTANCE}" --type full

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
"$AIFS_BIN" restore -i "${INSTANCE}" --time "${TARGET_TIME_UTC}" --force

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
