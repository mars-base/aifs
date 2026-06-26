# aifs

Aifs is a database file system designed for AI-Agent and various needs to prevent accidental deletion, making data modifications reliable and controllable, and allowing data to be returned to any time.

In the AI Agent era, agents autonomously read, write, and modify files at scale — a single misstep can destroy hours of work. aifs makes every change traceable and reversible. Powered by PostgreSQL PITR (Point-In-Time Recovery), it turns the filesystem into a time machine: rewind to any moment, recover deleted files, undo mistakes, and let agents work fearlessly knowing nothing is ever truly lost.

## Prerequisites

- **Linux**: podman runs natively, no VM required. On Debian/Ubuntu, enable unprivileged user namespaces for rootless containers:
  ```bash
  sudo sysctl kernel.unprivileged_userns_clone=1
  echo 'kernel.unprivileged_userns_clone=1' | sudo tee /etc/sysctl.d/99-rootless-podman.conf
  ```
- **macOS**: `brew install podman` + `podman machine init` + `podman machine start`
- **Windows**: Run `scripts/aifs-setup.ps1` to install WSL2 + Podman (see below), or install manually. Windows 10 users should review the [Windows 10 requirements](#windows-10-requirements) first.

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

> **Tip**: Installation and upgrade use the same command — just re-run it to update to the latest version.

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

# 1.1 or create project and multi instances
aifs create -i your-project

# 2. Start PostgreSQL and backup container
aifs start -i your-project

# 2.1 Check status
aifs status -i your-project

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

# 7. Agent does its risky work... (read, write, delete, experiment freely)

# 8. If something goes wrong, rewind first, then remount
aifs umount ~/mnt
aifs restore -i your-project --time "2026-06-15 14:30:00+00"  # Note! Just read-only
aifs mount -i your-project ~/mnt -d

# 9. Everything is back — nothing lost
cat ~/mnt/hello.txt                         # still there

# 10. Verify the data looks right, then promote to read-write
#     restore leaves the filesystem read-only (paused at the target time).
#     Once you've confirmed the files are what you expect, promote to resume
#     normal read-write access — the filesystem travels back in time and
#     becomes fully writable again.
aifs umount ~/mnt
aifs restore -i your-project --time "2026-06-15 14:30:00+00" --promote
aifs mount -i your-project ~/mnt -d
echo "back in time and fully writable" > ~/mnt/hello.txt
```

### Adding a second project instance

Each project is an independent instance with its own database and mount point:

```bash
# Create a new instance
aifs create -i project-b --base-dir /data/aifs/project-b

aifs start  -i project-b
aifs format -i project-b
mkdir -p ~/mnt/project-b
aifs mount  -i project-b ~/mnt/project-b -d

# List all instances
aifs list

# Stop when done
aifs stop -i project-b
```

### Windows (PowerShell)

```powershell
# 1. Initialize config (choose a dedicated data directory)
aifs config init --add your-project --base-dir D:\aifs

# 1.1 or create project and multi instances
aifs create -i your-project

# 2. Start PostgreSQL and backup container
aifs start -i your-project

# 2.1 Check status
aifs status -i your-project

# 3. Format the filesystem (one-time setup)
aifs format -i your-project

# 4. Mount as a drive letter (recommended)
aifs mount -i your-project Z: -d

# 5. Use it like a normal drive
echo hello aifs > Z:\hello.txt
mkdir Z:\projects
type Z:\hello.txt                           # → hello aifs

# 6. Create a snapshot before risky work
aifs snapshot create --type full

# 7. Agent does its risky work...

# 8. If something goes wrong, rewind first, then remount
aifs umount Z:
aifs restore -i your-project --time "2026-06-15 14:30:00+00"  # Note! Just read-only
aifs mount -i your-project Z: -d

# 9. Everything is back
type Z:\hello.txt                           # still there

# 10. Verify the data looks right, then promote to read-write
#     restore leaves the filesystem read-only (paused at the target time).
#     Once you've confirmed the files are what you expect, promote to resume
#     normal read-write access — the filesystem travels back in time and
#     becomes fully writable again.
aifs umount Z:
aifs restore -i your-project --time "2026-06-15 14:30:00+00" --promote
aifs mount -i your-project Z: -d
echo back in time and fully writable > Z:\hello.txt
```

> [!IMPORTANT]
> **Always use a drive letter (Z:, X:, etc.) on Windows.** Drive-letter mounts are session-independent — they survive closing the terminal, work across all processes, and persist across user logins until explicitly unmounted.
>
> ### Directory mounts (not recommended for daily use)
>
> aifs also supports directory-path mounts (`aifs mount -i your-project C:\mnt\aifs -d`), but with **significant limitations**:
>
> | Limitation | Detail |
> |---|---|
> | **Terminal-bound** | Closing the PowerShell/cmd window **kills the mount**. This is inherent to how Windows delivers `CTRL_CLOSE_EVENT` to console-attached processes — rclone, SSHFS-Win, and all other WinFsp-based tools have the same behavior. |
> | **Interactive session only** | Directory mounts require an active interactive logon session (WinSta0). They don't work from services, SSH, or non-interactive contexts. |
> | **Mount point must NOT exist** | The target directory must be absent — WinFsp creates it on mount. If `C:\mnt\aifs` already exists, you'll get an error telling you to remove it first. |
> | **Logout kills mount** | Like all session-scoped mounts (including drive letters), directory mounts are lost on logout. |
>
> **Use directory mounts only for quick, temporary access** (e.g., `aifs mount -i test1 C:\mnt\aifs` to peek at files). For daily work, use a drive letter.

## Data Storage

By default all data (PostgreSQL database files, backups, WAL archives) lives under
`~/.aifs/`. Use `--base-dir` to put it on a dedicated disk — this is the
recommended setup for production or long-lived projects.

```bash
# Store everything on a dedicated volume
aifs config init --add your-project --base-dir /mnt/ssd/aifs
```

### Multiple instances

aifs supports multiple independent instances on the same machine. Use `aifs create` to add more:

```bash
# Create a second instance on a dedicated path
aifs create -i project-b --base-dir /data/aifs/project-b

# Start, format, and mount it
aifs start -i project-b
aifs format -i project-b
mkdir -p ~/mnt/project-b
aifs mount -i project-b ~/mnt/project-b -d
```

Each instance gets its own PostgreSQL container, port, and data directory. All commands accept `-i <name>` to target a specific instance. To start all instances at once:

```bash
aifs start --all
aifs list           # overview of all instances
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

## Windows 10 requirements

aifs requires WSL2, which has the following minimum requirements on Windows 10:

| Architecture | Minimum version | Minimum build |
|---|---|---|
| x64 | Windows 10 Version 1903 | Build 18362.1049 |
| ARM64 | Windows 10 Version 2004 | Build 19041 |

Windows 10 also requires manually installing the **WSL2 Linux kernel update package** before running `aifs-setup.ps1`. See [Manual installation steps for older versions of WSL](https://learn.microsoft.com/en-us/windows/wsl/install-manual#step-4---download-the-linux-kernel-update-package) for the download links and instructions.

Windows 11 does not require this step.

## Benchmark

aifs ships a built-in benchmark command to measure I/O performance on any path:

```bash
# Linux / macOS
aifs bench ~/mnt/your-project          # default: 100 MiB big file, 10 small files, 1 thread
aifs bench ~/mnt/your-project -p 4    # 4 concurrent threads
aifs bench ~/mnt/your-project --big-file-size 0  # small files only
aifs bench /tmp                        # baseline against local disk
```

```powershell
# Windows — pass the drive letter directly
aifs bench Z:
aifs bench Z: -p 4
```

### Reference results (SATA HDD, single thread)

Measured on a SATA mechanical disk with the aifs data directory (`--base-dir`) on that same disk:

```
BlockSize: 1 MiB, BigFileSize: 100 MiB, SmallFileSize: 128 KiB, SmallFileCount: 10, NumThreads: 1
+------------------+-----------------+---------------+
|       ITEM       |      VALUE      |     COST      |
+------------------+-----------------+---------------+
| Write big file   | 1.30 MiB/s      | 77.10 s/file  |
| Read big file    | 43.43 MiB/s     | 2.30 s/file   |
| Write small file | 11.8 files/s    | 84.40 ms/file |
| Read small file  | 1685.8 files/s  | 0.59 ms/file  |
| Stat file        | 16246.6 files/s | 0.06 ms/file  |
+------------------+-----------------+---------------+
```

### Reference results (NVMe SSD, single thread)

Same machine, aifs data directory on a local NVMe SSD (LUKS-encrypted):

```
BlockSize: 1 MiB, BigFileSize: 100 MiB, SmallFileSize: 128 KiB, SmallFileCount: 10, NumThreads: 1
+------------------+-----------------+--------------+
|       ITEM       |      VALUE      |     COST     |
+------------------+-----------------+--------------+
| Write big file   | 9.11 MiB/s      | 10.97 s/file |
| Read big file    | 45.12 MiB/s     | 2.22 s/file  |
| Write small file | 300.6 files/s   | 3.33 ms/file |
| Read small file  | 2023.1 files/s  | 0.49 ms/file |
| Stat file        | 13807.5 files/s | 0.07 ms/file |
+------------------+-----------------+--------------+
```

Placing `--base-dir` on NVMe delivers **~11× faster big-file writes** and **~23× faster small-file writes** compared to SATA HDD. The bottleneck is PostgreSQL WAL fsync latency, which NVMe reduces dramatically. Read speeds are similar because both measurements were taken without root (page cache not dropped).

### Why the write speed is lower than a regular filesystem

aifs is **not** designed for high-throughput sequential writes or large-scale storage. Every write to the aifs filesystem is durably stored as a row in PostgreSQL — each block goes through WAL, fsync, and buffer management before it is acknowledged. This is what makes the filesystem a time machine: every change is transactional and can be rewound to any point in history.

**aifs is designed for a different goal**: letting your database and filesystem **travel back in time together**. If you need to ask "what did this file look like at 14:02 yesterday?", or "undo everything my agent wrote in the last 10 minutes", aifs is the right tool. If you need to stream 1 GB/s of writes, a regular filesystem is the right tool.

For best write performance, place `--base-dir` on a **local NVMe SSD**. Avoid spinning HDDs and network-attached storage for the PostgreSQL data directory.

## Troubleshooting

### Windows: `aifs start` fails with `Input/output error` on E: (or other drives)

**Symptom**

```
Error: creating backup directory E:\aifs\backup\data (wsl): wsl mkdir /mnt/e/aifs/backup/data:
exit status 1 (output: mkdir: cannot create directory '/mnt/e/aifs': Input/output error)
```

Or the container fails to start with:

```
Error: unable to start container "...": crun: cannot stat `/mnt/e/aifs/pgbackrest-aifs-pg-ai01.conf`:
No such file or directory
```

**Cause**

After `wsl --shutdown` (which can be triggered by an aifs upgrade or a manual restart), the WSL DrvFs 9p connection to non-C: drives becomes stale. The mount entry still exists in `/proc/mounts` but the underlying 9p socket is broken, causing `Input/output error` on any access.

**Fix**

Remount the affected drive inside WSL (no shutdown needed):

```powershell
# Replace E: with whichever drive your --base-dir is on
wsl -d podman-machine-default -u root --exec sh -c "umount /mnt/e && mount -t drvfs E: /mnt/e -o metadata"
```

Then remove the stale container and start again:

```powershell
$env:CONTAINER_HOST = "tcp://localhost:2375"
podman rm -f aifs-pg-<instance>
aifs -i <instance> start
```

## License

[PolyForm Noncommercial 1.0.0](LICENSE) — free for personal, educational, academic, non-profit, and government use. Commercial use requires a separate license.
