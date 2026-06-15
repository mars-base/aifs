FROM docker.io/library/debian:13-slim

# Add PostgreSQL APT repository to get the same pgbackrest version as postgres:18
RUN apt-get update && apt-get install -y curl ca-certificates gnupg \
    && curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc | \
       gpg --dearmor -o /usr/share/keyrings/pgdg.gpg \
    && echo "deb [signed-by=/usr/share/keyrings/pgdg.gpg] http://apt.postgresql.org/pub/repos/apt trixie-pgdg main" \
       > /etc/apt/sources.list.d/pgdg.list \
    && apt-get update && apt-get install -y pgbackrest \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /var/lib/pgbackrest /var/log/pgbackrest /etc/pgbackrest

VOLUME ["/var/lib/pgbackrest", "/var/log/pgbackrest"]

# pgbackrest.conf is mounted at runtime: -v <path>:/etc/pgbackrest/pgbackrest.conf:ro
# WAL volumes are mounted as: -v <wal_vol>:/wal/<instance>:ro

ENTRYPOINT ["sleep", "infinity"]
