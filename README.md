# aifs

A database filesystem built for AI Agents — give your agents a time machine.

AI agents are powerful but unpredictable: they can delete files, corrupt data, or make mistakes that are hard to undo. `aifs` solves this with **PITR (Point-In-Time Recovery)** powered by **PostgreSQL**, letting you rewind the entire filesystem to any moment in time. Agents can work fearlessly, knowing nothing is ever truly lost.

## Why aifs

| Problem | aifs Solution |
|---------|---------------|
| Agent accidentally deletes files | Rewind to just before the deletion |
| Agent corrupts data mid-task | Restore to the last known-good snapshot |
| Need to experiment safely | Branch the filesystem, test, then merge or discard |
| Multiple agents share state | Isolated instances with per-agent time travel |

## Prerequisites

| Dependency | Purpose | Install |
|------------|---------|---------|
| Podman | Container runtime | [podman.io](https://podman.io) |

- **Linux**: podman runs natively, no VM required
- **macOS**: `brew install podman` + `podman machine init`
- **Windows**: Install WSL2 first, then `winget install podman` + `podman machine init`

## Installation

### Pre-built binaries

```bash
# Linux / macOS
curl -fsSL https://github.com/mars-base/aifs/releases/latest/download/install.sh | sh

# Windows (PowerShell)
irm https://github.com/mars-base/aifs/releases/latest/download/install.ps1 | iex
```

## Quick Start

```bash
# 1. Initialize config (with a default instance)
aifs config init --add default

# 2. Start PostgreSQL + pgBackRest backup container
aifs start -i default

# 3. Check status
aifs status -i default

# 4. Create a snapshot before letting the agent loose
aifs snapshot create --type full --comment "before-agent-run"

# 5. Agent does its work...

# 6. If something goes wrong, rewind
aifs restore -i default --time "2026-06-15 14:30:00"

# 7. Everything is back — nothing lost
```

## Snapshot types

`aifs snapshot create` supports three pgBackRest backup types:

| Type | Description | Use case |
|------|-------------|----------|
| `full` | Full database backup | Baseline, run periodically (e.g. weekly) |
| `diff` | Changes since the last `full` | Daily backups, simpler restore chain than `incr` |
| `incr` | Changes since the last backup of any type | Smallest/fastest, but restore requires the full chain |

**Recommended schedule**: `full` weekly + `diff` daily. This balances storage, backup speed, and restore reliability.

```bash
aifs snapshot create --type full
aifs snapshot create --type diff
```

## Config file

```yaml
# ~/.aifs/config.yaml
base_dir: ""
network: "aifs-net"

backup:
  container_name: "aifs-backup"
  image_tag: "ghcr.io/mars-base/aifs/aifs-backup:2.58.0"
  data_dir: "~/.aifs/backup/data"
  log_dir: "~/.aifs/backup/log"
  retention_full: 7

logging:
  level: "info"

instances:
  default:
    postgres:
      host: "localhost"
      port: 5432
      user: "aifs"
      password: "<random>"
      database: "default_db"
    podman:
      container_name: "aifs-pg-default"
      data_dir: "~/.aifs/dbdata/default/data"
      wal_dir: "~/.aifs/dbdata/default/wal"
      image_tag: "ghcr.io/mars-base/aifs/aifs-pg:18-2.58.0"
      host_port: 25432
    pitr:
      enabled: true
      pgbackrest_stanza: "aifs_default"
```

## Build from source

```bash
git clone https://github.com/mars-base/aifs.git
cd aifs
go build -o aifs ./cmd/aifs/
```

## License

[CC BY-NC 4.0](LICENSE)
