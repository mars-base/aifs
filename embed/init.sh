#!/bin/bash
# aifs PostgreSQL initialization script
# Executed on first PostgreSQL container startup, configures WAL archiving.
#
# Environment variables:
#   PGBACKREST_STANZA - pgbackrest stanza name (default: aifs)

set -e

STANZA="${PGBACKREST_STANZA:-aifs}"

# Create a 'postgres' superuser role so pgbackrest can connect via peer/Unix
# socket when running as the OS postgres user over SSH.
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-'EOSQL'
    DO $$
    BEGIN
        IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'postgres') THEN
            CREATE ROLE postgres WITH LOGIN SUPERUSER;
        END IF;
    END
    $$;
EOSQL

cat >> "$PGDATA/postgresql.conf" << EOF

# === aifs PITR configuration ===
wal_level = replica
archive_mode = on
# Run pgbackrest as root via sudo so the backup repo (shared with the backup
# container) is accessed by the same host-mapped UID in rootless podman.
archive_command = 'sudo -n -u root pgbackrest --stanza=${STANZA} archive-push %p'
archive_timeout = 60
max_wal_senders = 10
EOF

echo "aifs: WAL archive configuration written (stanza=${STANZA})"
