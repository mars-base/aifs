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

- **Linux**: podman runs natively, no VM required. On Debian/Ubuntu, enable unprivileged user namespaces for rootless containers:
  ```bash
  sudo sysctl kernel.unprivileged_userns_clone=1
  echo 'kernel.unprivileged_userns_clone=1' | sudo tee /etc/sysctl.d/99-rootless-podman.conf
  ```
- **macOS**: `brew install podman` + `podman machine init`
- **Windows**: Run `scripts/aifs-setup.ps1` to install WSL2 + Podman (see below), or install manually

### Windows setup script

On Windows you need to complete **two steps**:

1. Run `scripts/aifs-setup.ps1` to prepare the environment (WSL2 + Podman).
2. Install `aifs` itself (see [Installation](#installation) below).

`aifs-setup.ps1` checks CPU virtualization, enables WSL2, installs Podman, and initializes a podman machine. The first run may enable Windows features that require a reboot:

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/mars-base/aifs/main/scripts/aifs-setup.ps1 | iex

# With a proxy
$env:HTTPS_PROXY="http://proxy.example.com:8080"
irm https://raw.githubusercontent.com/mars-base/aifs/main/scripts/aifs-setup.ps1 | iex
```

If the script reports a reboot is required, restart the machine and then **run the same command again**. Repeat until the script prints the "Setup Complete" / environment-ready message. After that, proceed to install `aifs`.

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

# 6. If something goes wrong, rewind (accepts multiple timezone formats)
aifs restore -i default --time "2026-06-15 14:30:00+00"

# 7. Everything is back — nothing lost
```

## Restore time formats

`aifs restore --time` accepts the following timezone-aware formats:

```bash
aifs restore -i default --time "2026-06-15 14:30:00+00:00"
aifs restore -i default --time "2026-06-15 14:30:00+0000"
aifs restore -i default --time "2026-06-15 14:30:00+00"
aifs restore -i default --time "2026-06-15 22:30:00+08"
```

You can also omit the timezone offset, in which case the time is interpreted
as **UTC**:

```bash
aifs restore -i default --time "2026-06-15 14:30:00"
```

The provided time is normalized to the same absolute point in time before being
passed to pgBackRest, so `+08` and `+00` inputs that represent the same moment
will restore to the same state.

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
