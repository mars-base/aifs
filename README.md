# aifs

A database filesystem built for AI Agents — give your agents a time machine.

AI agents are powerful but unpredictable: they can delete files, corrupt data, or make mistakes that are hard to undo. `aifs` solves this with **PITR (Point-In-Time Recovery)** powered by **PostgreSQL**, letting you rewind the entire filesystem to any moment in time. Agents can work fearlessly, knowing nothing is ever truly lost.

## Prerequisites

- **Linux**: podman runs natively, no VM required. On Debian/Ubuntu, enable unprivileged user namespaces for rootless containers:
  ```bash
  sudo sysctl kernel.unprivileged_userns_clone=1
  echo 'kernel.unprivileged_userns_clone=1' | sudo tee /etc/sysctl.d/99-rootless-podman.conf
  ```
- **macOS**: `brew install podman` + `podman machine init` + `podman machine start`
- **Windows**: Run `scripts/aifs-setup.ps1` to install WSL2 + Podman (see below), or install manually

### Windows setup script

On Windows you need to complete **two steps**:

1. Run `scripts/aifs-setup.ps1` to prepare the environment (WSL2 + Podman).
2. Install `aifs` itself (see [Installation](#installation) below).

`aifs-setup.ps1` checks CPU virtualization, enables WSL2, installs Podman, and initializes a podman machine. The first run may enable Windows features that require a reboot:

```powershell
irm https://raw.githubusercontent.com/mars-base/aifs/main/scripts/aifs-setup.ps1 | iex
```

If the script reports a reboot is required, restart the machine and then **run the same command again**. Repeat until the script prints the "Setup Complete" / environment-ready message. After that, proceed to install `aifs`.

## Installation

### Pre-built binaries

Linux / macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/mars-base/aifs/main/scripts/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/mars-base/aifs/main/scripts/install.ps1 | iex
```

## Quick Start

### Linux / macOS

```bash
# 1. Initialize config (choose a dedicated data directory)
aifs config init --add your-project --base-dir ~/.aifs

# 2. Start PostgreSQL and backup container
aifs start -i your-project

# 3. Format the filesystem (one-time setup)
aifs format -i your-project

# 4. Mount the filesystem
mkdir -p ~/mnt
aifs mount -i your-project ~/mnt -d        # -d runs mount in background

# 5. Use it like a normal filesystem
echo "hello aifs" > ~/mnt/hello.txt
mkdir ~/mnt/projects
cat ~/mnt/hello.txt                         # → hello aifs

# 6. Create a snapshot before risky work
aifs snapshot create --type full

# 7. Agent does its work... (read, write, delete, experiment freely)

# 8. If something goes wrong, rewind first, then remount
aifs umount ~/mnt
aifs restore -i your-project --time "2026-06-15 14:30:00+00"
aifs mount -i your-project ~/mnt -d

# 9. Everything is back — nothing lost
cat ~/mnt/hello.txt                         # still there
```

### Windows

```powershell
# 1. Initialize config (choose a dedicated data directory)
aifs config init --add your-project --base-dir D:\aifs

# 2. Start PostgreSQL and backup container
aifs start -i your-project

# 3. Format the filesystem (one-time setup)
aifs format -i your-project

# 4. Mount as a drive letter or directory
aifs mount -i your-project Z: -d            # drive letter (session-independent)
aifs mount -i your-project C:\mnt\aifs -d  # or directory path

# 5. Use it like a normal drive
echo hello aifs > Z:\hello.txt
mkdir Z:\projects
type Z:\hello.txt                           # → hello aifs

# 6. Create a snapshot before risky work
aifs snapshot create --type full

# 7. Agent does its work...

# 8. If something goes wrong, rewind first, then remount
aifs umount Z:
aifs restore -i your-project --time "2026-06-15 14:30:00+00"
aifs mount -i your-project Z: -d

# 9. Everything is back
type Z:\hello.txt                           # still there
```

> **Tip**: On Windows, use a drive letter (Z:, X:, etc.) for session-independent access. Directory mounts require an interactive logged-on console session.

## Data Storage

By default all data (PostgreSQL database files, backups, WAL archives) lives under
`~/.aifs/`. Use `--base-dir` to put it on a dedicated disk — this is the
recommended setup for production or long-lived projects.

```bash
# Store everything on a dedicated volume
aifs config init --add your-project --base-dir /mnt/ssd/aifs
```

When `--base-dir` is set, the directory layout looks like this:

```
/mnt/ssd/aifs/
├── config.yaml          # aifs configuration
├── dbdata/              # data files (per instance)
│   └── your-project/
│       └── data/
├── backup/              # backup repository
│   ├── data/
│   └── log/
```

### Best practices by platform

| Platform | Recommendation |
|----------|---------------|
| **Linux** | Mount a dedicated SSD or NVMe at `/data/aifs` or `/mnt/aifs`. Use XFS or ext4 — avoid NFS/CIFS for the database data directory. Ensure the user owns the mount point: `sudo chown $USER:$USER /data/aifs`. |
| **macOS** | Use an external Thunderbolt/USB4 SSD for active projects. Default `~/tmp` is already shared with podman machine on macOS; if you use a path outside `/Users`, add a volume mount: `podman machine ssh podman-machine-default -- sudo mount -t virtiofs ...`. |
| **Windows** | Mount to a drive letter on a fast local SSD (e.g., `--base-dir D:\aifs`). The WSL2 podman backend maps Windows drives under `/mnt/<letter>/`. Avoid network drives — database latency over SMB is prohibitive. |

> **Important**: The database data directory (`dbdata/`) should always live on
> local low-latency storage (SSD/NVMe). Backup archives can be on slower HDD or
> network storage via `backup.data_dir` override in config. Never place
> PostgreSQL data files on NFS or SMB — it will corrupt your database.

## Daily Operations Manual

### After reboot or logout

Background mounts are **session-scoped** — they disappear on logout or reboot. WSL VM and podman survive via a Scheduled Task keep-alive.

```powershell
# Windows: after logging back in
aifs mount -i your-project Z: -d
```

```bash
# Linux/macOS: after logging back in
aifs mount -i your-project ~/mnt -d
```

If the podman WSL VM was cold-restarted (e.g. `wsl --shutdown` or system reboot), containers with `--restart unless-stopped` may not auto-recover in podman under WSL. Use `--all` to bring them all back:

```bash
aifs start --all
```

### Viewing status

```bash
# Quick overview: all instances and their containers
aifs list

# Detailed status for the current instance, including snapshots
aifs status -i your-project

# Snapshot list with full/diff/incr type, labels, and UTC timestamps
aifs snapshot list -i your-project
```

### Snapshot workflow

Snapshots are your safety net. Take one before any risky operation.

```bash
# Full baseline (do this weekly)
aifs snapshot create -i your-project --type full

# Daily incremental (fast, small)
aifs snapshot create -i your-project --type diff
```

### PITR restore

Restore rewinds the filesystem to a point in time. By default, after restore the database is **paused** (read-only, `pg_is_in_recovery()=t`). This lets you inspect files and verify correctness without committing to the new timeline.

```bash
# 1. Unmount before restore
aifs umount Z:                           # Windows
aifs umount ~/mnt                         # Linux/macOS

# 2. Restore to a point in time (pauses in read-only by default)
aifs restore -i your-project --time "2026-06-24 10:30:00+00" --force

# 3. Mount and inspect the restored state
aifs mount -i your-project Z: -d
# Read files, check contents — database is read-only

# 4. If it's wrong, try a different time (just restore again)
aifs umount Z:
aifs restore -i your-project --time "2026-06-24 10:28:00+00" --force
aifs mount -i your-project Z: -d

# 5. Once satisfied, promote the cluster to read-write
aifs umount Z:
aifs restore -i your-project --time "2026-06-24 10:30:00+00" --promote --force
aifs mount -i your-project Z: -d
# Now you can write again
```

**Promote fast path**: If the cluster is already paused at the **same** target time, `--promote` skips re-restore and directly promotes (`promoting directly` message). This lets you inspect first, then promote instantly.

**Promote full path**: If you change the target time or the cluster is already promoted, `--promote` does a full wipe + restore + replay + promote cycle.

### Restore workflow diagram

```
Snapshot A ──→ writes B ──→ writes C ──→ ... ──→ now
               (14:00)      (14:05)

# "Oh no, B deleted important files at 14:00"
aifs restore --time "2026-06-24 13:59:00+00"   # rewind to just before B
# → inspect → wrong? Adjust time →
aifs restore --time "2026-06-24 14:02:00+00"   # rewind to between B and C
# → inspect → correct! Promote →
aifs restore --time "2026-06-24 14:02:00+00" --promote
```

### Restore time format

Use UTC with timezone offset. When in doubt, check snapshot timestamps with `aifs status -i your-project` (labelled `UTC`).

```bash
aifs restore -i your-project --time "2026-06-24 10:30:00+00"
aifs restore -i your-project --time "2026-06-24 18:30:00+08"    # same moment, UTC+8
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

## Destroying an instance

When you no longer need an instance, `aifs destroy` removes it:

```bash
# Keep local data (default) — preserves PostgreSQL data on disk
aifs destroy -i your-project

# Skip the confirmation prompt
aifs destroy -i your-project --force
```

By default, `destroy` stops the container and removes the config entry but **preserves** host data directories. If you need to permanently delete everything:

```bash
# Also delete data directory, WAL archives, and backup stanza
aifs destroy -i your-project --clean-data --force
```

| Flag | Effect |
|------|--------|
| *(none)* | Stop container + remove config entry, **keep** local data |
| `--clean-data` | Also delete `dbdata/`, WAL archives, and backup repository |
| `--force` | Skip confirmation prompt |

## Build from source

```bash
git clone https://github.com/mars-base/aifs.git
cd aifs
make build
```

## License

[CC BY-NC 4.0](LICENSE)
