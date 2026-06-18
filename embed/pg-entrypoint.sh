#!/bin/bash
# aifs PostgreSQL entrypoint wrapper: starts sshd, then delegates to the
# official postgres entrypoint.

set -e

# Generate host keys if absent
for key in /etc/ssh/ssh_host_rsa_key /etc/ssh/ssh_host_ed25519_key; do
    if [[ ! -f "$key" ]]; then
        alg=${key##*_}
        alg=${alg%_key}
        ssh-keygen -t "${alg:-rsa}" -f "$key" -N '' -q
    fi
done

# Install the backup container's public key if provided via bind-mount.
# The key is mounted read-only at /run/aifs/backup_id_rsa.pub so that it
# survives PG container recreation. We copy it (rather than mounting directly)
# so sshd sees correct postgres ownership and permissions.
if [[ -f /run/aifs/backup_id_rsa.pub ]]; then
    mkdir -p /etc/ssh/authorized_keys
    cp /run/aifs/backup_id_rsa.pub /etc/ssh/authorized_keys/postgres
    chown postgres:postgres /etc/ssh/authorized_keys/postgres
    chmod 600 /etc/ssh/authorized_keys/postgres
fi

# Start sshd in background. On Windows (host networking) each PG instance
# gets a unique AIFS_SSH_PORT to avoid port collisions.
SSHD_OPTS=""
if [ -n "${AIFS_SSH_PORT:-}" ]; then
    SSHD_OPTS="-p ${AIFS_SSH_PORT}"
fi
/usr/sbin/sshd $SSHD_OPTS

# Hand off to the official postgres entrypoint
exec docker-entrypoint.sh "$@"
