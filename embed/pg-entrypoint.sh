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

# Start sshd in background
/usr/sbin/sshd

# Hand off to the official postgres entrypoint
exec docker-entrypoint.sh "$@"
