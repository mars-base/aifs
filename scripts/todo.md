# scripts/todo.md — 脚本/部署任务跟踪

## 进行中

### 1. Windows e2e 测试 (HIH-D-34696, 10.246.7.155, vagrant)

**已完成**

- [x] 交叉编译 Windows 二进制：`GOOS=windows go build -o build/aifs-windows-amd64.exe ./cmd/aifs`
- [x] WSL podman service 持久化修复 (`service_windows.go`)
  - 核心问题：Go `exec.Cmd.Start` 将子进程放入 Go 的 Windows Job Object，aifs 退出时 Job Object 销毁 → wsl.exe 被杀 → WSL VM 关闭
  - 解决方案：`cmd /c start "" wsl ...` — `start` 通过 `ShellExecuteEx` 创建进程，脱离 Go Job Object
  - 已验证：wsl.exe 存活超过 53s（远超 8s 空闲超时），2375/25432/32201 端口正常监听
- [x] aifs config init + start 流程验证通过
  - podman service 启动 → portproxy 自动配置 → PG 容器创建 → backup 容器创建 → stanza create
- [x] `mkdir Z:` 修复 (`mount_windows.go`)：盘符模式下跳过 `os.MkdirAll`
- [x] mount 路径规范化 (`NormalizeMountPoint` / `MountPathJoin`)
  - 路径拼接（`Z:`+`.aifs-mounted` → `Z:\.aifs-mounted`）需要补 `\`
  - `host.Mount` 用原始盘符 `Z:`（WinFsp 不接受 `Z:\`）
- [x] background mount 失败修复
  - 根因：mount 子进程里 `host.Mount` 被传了 `Z:\` 而不是 `Z:`，且 sentinel 文件只在 `Getattr` 按路径处理，Windows `CreateFile`/`Stat` 走的 `Open`/`Getattr(fh)` 返回 `ENOENT`
  - 修复：`cli/mount_windows.go` 保持原始 `Z:` 传给子进程；`pgfs/fs_windows.go` 在 `Open`、`Getattr(fh)`、`Read`、`Readdir`、`Create` 中统一处理 `.aifs-mounted`
- [x] stanza-create 瞬态失败修复 (`pitr/pitr.go`)
  - 根因：PG 启动未完成时 pgBackRest `stanza-create` 报 `FATAL: the database system is starting up`
  - 修复：重试 15 次，遇到 `database system is starting up` / `unable to check pg1` 时 sleep 2s 继续
- [x] 完整 snapshot 验证通过
- [x] restore 命令本身验证通过（`pgbackrest restore` 返回成功）

**当前问题（已解决）**

- [x] **restore 后 PostgreSQL 容器启动失败**：根因是 Windows 上 `EnsureRepoReadable` 为 no-op，恢复后 pgBackRest repo 文件权限为 640/750，PG 容器内 `postgres` 用户执行 `archive-get` 时权限不足
  - 修复：`internal/podman/repo_windows.go` 改为在 WSL 中对 repo 目录递归执行 `chmod -R a+rX`
  - 验证：完整 e2e 跑通，`E2E TEST PASSED`

**接下来操作**

1. ~~验证/修复 `EnsureRepoReadable` 在 Windows 上的行为~~ ✅
2. ~~重新部署并触发完整 e2e 跑一遍~~ ✅
3. ~~移除 `fs_windows.go` / `mount_windows.go` 中的临时 debug 日志~~ ✅

### Windows 目录（pathname）挂载限制

- **问题**：`aifs mount C:\some-dir` 在 WinRM / S4U 计划任务等非交互式会话下无法挂载，WinFsp/cgofuse 的 `host.Mount` 返回 false。
- **根因**：WinFsp 目录挂载需要交互式窗口站 `WinSta0`；盘符挂载通过 Mount Manager，可在服务/非交互会话工作。
- **验证**：最小 cgofuse smoke 测试 `dirsmoke.exe` 同样失败；盘符挂载正常。
- **当前处理**：`internal/pgfs/mount_windows.go` 已增加会话检测，非交互会话下目录挂载直接返回清晰错误：
  ```
  directory mounts on Windows require an interactive session (WinSta0); use a drive letter such as Z: or run aifs from a logged-on console
  ```
- **后续**：如需支持非交互目录挂载，需研究 WinFsp Launcher/服务方式（launchctl）或改用盘符+目录 junction。
- **相关改动**：
  - `internal/cli/mount_windows.go`：目录挂载不再使用 `DETACHED_PROCESS`
  - `internal/pgfs/mount_windows.go`：新增 `isInteractiveSession()` 与目录挂载限制提示

**参考**
- 脚本：`scripts/test-e2e.ps1` — PITR 全流程
- 文件传输：`~/bucket/tools/` HTTP 站 `http://10.241.21.97:1357/`
- WinRM：`winrm-tool -host 10.246.7.155 -user vagrant -pass 'ymovPv1L2S8R9FW3tdqD3O2L' -port 5985 -https=false -commands /tmp/winrm-*.json`
- 二进制：`/home/fish/bucket/tools/aifs-windows-amd64.exe`
- 测试 runner：`scripts/test-e2e-runner.ps1`（S4U 计划任务方式），同步到 `/home/fish/bucket/tools/test-e2e-runner.ps1`
- WinRM 输出读取：数据写 `Out-File` 后用 `cmd /c type` 读回（避免 PS stdout 被吞和中文乱码）

## 已完成

### aifs-setup.ps1 部署 ✅
| 阶段 | 状态 | 详情 |
|------|------|------|
| Phase 1 - CPU 虚拟化 | ✅ | 嵌套 VM，`wsl --status` 交叉验证通过 |
| Phase 2 - WSL2 | ✅ | 已安装，默认版本 2 |
| Phase 3 - Podman | ✅ | v5.8.3 已安装 |
| Phase 4 - WinFsp | ✅ | v2.1.25156 已安装（MSI 静默安装） |
| Phase 5 - Podman CLI | ✅ | CLI 可用 |
| Summary | ✅ | WSL2 + Podman + WinFsp 全部就绪

### WSL podman service 持久化 ✅
- `cmd /c start ""` 脱离 Go Job Object → wsl.exe 存活
- portproxy 自动配置、boot-ID 缓存清理、TCP 健康检查（ss -tlnp）

## Windows VM 日志文件速查

| 文件（VM 路径） | 说明 |
|---|---|
| `%USERPROFILE%\e2e-full.log` | **S4U 完整测试日志**（Start-Transcript 捕获，最可靠） |
| `%USERPROFILE%\aifs-windows-amd64.exe` | aifs 二进制 |
| `%USERPROFILE%\test-e2e-runner.ps1` | S4U 测试 runner（下载+部署） |
| `%USERPROFILE%\test-e2e-wrapper.ps1` | S4U wrapper（Transcript 捕获） |
| `%USERPROFILE%\test-e2e.ps1` | e2e 测试脚本本体 |
| `%TEMP%\aifs-mount-*.log` | mount 子进程日志（后台 mount 失败时查） |
| `%TEMP%\aifs-pitr-win-*\config.yaml` | 测试实例配置 |
| `%TEMP%\aifs-pitr-win-*\backup\data` | pgBackRest repo 目录 |
| `%TEMP%\aifs-pitr-win-*\data\data\log` | PostgreSQL 日志目录 |
| `%USERPROFILE%\status.txt` | 临时状态检查输出（WinRM 诊断用） |
| `%USERPROFILE%\proc.txt` | 进程检查输出（WinRM 诊断用） |

**读取方式**：`cmd /c type <path>`（避免 PS stdout 被 WinRM 吞掉）
