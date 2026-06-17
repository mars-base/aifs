#!/bin/bash
# scripts/e2e-smoke.sh — End-to-end smoke test for aifs lifecycle.
#
# Usage:
#   ./scripts/e2e-smoke.sh [instance_name]
#
# Environment:
#   AIFS_BIN    path to aifs binary (default: ./build/aifs)
#   FORCE_CLEAN set to 1 to skip the destructive cleanup prompt
#
# The script tears down any existing aifs environment, then exercises:
#   config init / validate / show / create / list / destroy / start / status /
#   backup status / snapshot create / list / stop / restart / final cleanup

set -euo pipefail

INSTANCE="${1:-default}"
DB="${INSTANCE}_db"
CONTAINER="aifs-pg-${INSTANCE}"
AIFS_BIN="${AIFS_BIN:-./build/aifs}"
FORCE_CLEAN="${FORCE_CLEAN:-0}"

cd "$(dirname "$0")/.."

if [[ ! -x "$AIFS_BIN" ]]; then
    echo "Error: $AIFS_BIN not found. Build it first:" >&2
    echo "  go build -o build/aifs ./cmd/aifs" >&2
    exit 1
fi

AIFS_HOME="${HOME}/.aifs"

cleanup_containers() {
    echo "→ Stopping and removing existing aifs containers/networks..."
    podman stop -t 5 aifs-pg-default aifs-pg-proj01 aifs-pg-proj03 aifs-backup 2>/dev/null || true
    podman rm -f aifs-pg-default aifs-pg-proj01 aifs-pg-proj03 aifs-backup 2>/dev/null || true
    podman network rm -f aifs-net 2>/dev/null || true
}

cleanup_home() {
    echo "→ Removing existing aifs home directory (${AIFS_HOME})..."
    if [[ -d "${AIFS_HOME}" ]]; then
        podman unshare rm -rf "${AIFS_HOME}" 2>/dev/null || rm -rf "${AIFS_HOME}"
    fi
}

if [[ "$FORCE_CLEAN" != "1" ]]; then
    echo "⚠️  This script will DESTROY any existing aifs config, containers, and data under ${AIFS_HOME}"
    read -rp "Continue? [y/N]: " ans
    if [[ "$ans" != [yY]* ]]; then
        echo "Cancelled"
        exit 0
    fi
fi

cleanup_containers
cleanup_home

echo ""
echo "=== aifs e2e smoke test ==="
echo "Instance: ${INSTANCE}"
echo ""

echo "=== 1. config init ==="
"$AIFS_BIN" config init --add "$INSTANCE"

echo ""
echo "=== 2. config validate ==="
"$AIFS_BIN" config validate

echo ""
echo "=== 3. config show ==="
"$AIFS_BIN" config show

echo ""
echo "=== 4. create extra instance (smoke01) ==="
"$AIFS_BIN" create -i smoke01

echo ""
echo "=== 5. list instances ==="
"$AIFS_BIN" list

echo ""
echo "=== 6. destroy extra instance ==="
"$AIFS_BIN" destroy -i smoke01 --force

echo ""
echo "=== 7. start ${INSTANCE} instance ==="
"$AIFS_BIN" start -i "$INSTANCE"

echo ""
echo "=== 8. status ==="
"$AIFS_BIN" status -i "$INSTANCE"

echo ""
echo "=== 9. backup status ==="
"$AIFS_BIN" backup status

echo ""
echo "=== 10. create test table and insert data ==="
podman exec "$CONTAINER" psql -U aifs -d "$DB" -c "DROP TABLE IF EXISTS smoke_test;" >/dev/null
podman exec "$CONTAINER" psql -U aifs -d "$DB" -c "CREATE TABLE smoke_test(id serial primary key, note text);" >/dev/null
podman exec "$CONTAINER" psql -U aifs -d "$DB" -c "INSERT INTO smoke_test(note) VALUES ('before-snapshot');" >/dev/null
echo "  ✓ inserted 1 row"

echo ""
echo "=== 11. snapshot create (full, tail-logs) ==="
"$AIFS_BIN" snapshot create -i "$INSTANCE" --tail-logs --comment "e2e smoke full"

echo ""
echo "=== 12. snapshot list ==="
"$AIFS_BIN" snapshot list -i "$INSTANCE"

echo ""
echo "=== 13. stop ${INSTANCE} ==="
"$AIFS_BIN" stop -i "$INSTANCE"

echo ""
echo "=== 14. restart ${INSTANCE} ==="
"$AIFS_BIN" start -i "$INSTANCE"

echo ""
echo "=== 15. status after restart ==="
"$AIFS_BIN" status -i "$INSTANCE"

echo ""
echo "=== 16. snapshot after restart (incr, tail-logs) ==="
"$AIFS_BIN" snapshot create -i "$INSTANCE" --type incr --tail-logs --comment "e2e smoke incr"

echo ""
echo "=== 17. destroy ${INSTANCE} (with clean-data) ==="
"$AIFS_BIN" destroy -i "$INSTANCE" --clean-data --force || {
    echo "  ⚠ destroy --clean-data returned an error, will force-cleanup at the end"
}

echo ""
echo "=== 18. stop backup container ==="
"$AIFS_BIN" backup stop

echo ""
echo "=== 19. final cleanup ==="
cleanup_containers
cleanup_home

echo ""
echo "✓ aifs e2e smoke test completed successfully"
