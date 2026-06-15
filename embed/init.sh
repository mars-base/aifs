#!/bin/bash
# aifs PostgreSQL initialization script
# Executed on first PostgreSQL container startup, configures WAL archiving.
#
# Environment variables:
#   PGBACKREST_STANZA - pgbackrest stanza name (default: aifs)

set -e

STANZA="${PGBACKREST_STANZA:-aifs}"

cat >> "$PGDATA/postgresql.conf" << EOF

# === aifs PITR configuration ===
wal_level = replica
archive_mode = on
archive_command = 'pgbackrest --stanza=${STANZA} archive-push %p'
archive_timeout = 60
max_wal_senders = 10
EOF

echo "aifs: WAL archive configuration written (stanza=${STANZA})"
