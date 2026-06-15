// // Package podman manages the Podman container lifecycle:
// machine management, image building, directory creation, container start/stop, command execution.
package podman

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mars-base/aifs/internal/config"
	"github.com/mars-base/aifs/internal/platform"
	res "github.com/mars-base/aifs/embed"
)

// Manager encapsulates Podman operations, bound to a configuration.
type Manager struct {
	cfg      *config.Config
	podman   string // podman binary path
	dataDir  string // aifs data directory (~/.aifs)
}

// New creates a Podman manager.
func New(cfg *config.Config) (*Manager, error) {
	path, err := exec.LookPath("podman")
	if err != nil {
		return nil, fmt.Errorf("podman is not installed: %w", err)
	}
	dataDir := platform.DefaultConfigDir()
	return &Manager{
		cfg:     cfg,
		podman:  path,
		dataDir: dataDir,
	}, nil
}

// ─── Machine management ─────────────────────────────────────────

// EnsureMachine ensures podman machine is initialized and running (macOS/Windows only).
// No-op on Linux.
func (m *Manager) EnsureMachine() error {
	if !platform.NeedsPodmanMachine() {
		return nil // Linux: no machine needed
	}

	// Check if machine exists
	out, err := m.run("machine", "list", "--format", "{{.Name}}")
	if err != nil {
		return fmt.Errorf("checking podman machine list: %w", err)
	}

	hasMachine := false
	machineRunning := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		hasMachine = true
		// Check if running
		statusOut, _ := m.run("machine", "list", "--format", "{{.LastUp}}")
		if strings.TrimSpace(statusOut) != "" {
			machineRunning = true
		}
		break
	}

	if !hasMachine {
		fmt.Println("→ Initializing podman machine (first use, may take a few minutes)...")
		if err := m.runInteractive("machine", "init"); err != nil {
			return fmt.Errorf("podman machine init: %w", err)
		}
	}

	if !machineRunning {
		fmt.Println("→ Starting podman machine...")
		if err := m.runInteractive("machine", "start"); err != nil {
			return fmt.Errorf("podman machine start: %w", err)
		}
	}

	return nil
}

// ─── Image management ─────────────────────────────────────────────

// EnsureImage ensures the PostgreSQL + pgBackRest image is available.
// Tries podman pull first (for pre-built registry images), falls back to local build.
func (m *Manager) EnsureImage() error {
	tag := m.cfg.Podman.ImageTag

	exists, err := m.imageExists(tag)
	if err != nil {
		return err
	}
	if exists {
		fmt.Printf("→ Image %s already exists, skipping pull/build\n", tag)
		return nil
	}

	// Try pull first
	fmt.Printf("→ Pulling image %s...\n", tag)
	if _, err := m.run("pull", tag); err == nil {
		fmt.Println("  ✓ Image pulled from registry")
		return nil
	}
	fmt.Printf("  Pull failed, falling back to local build...\n")

	// Fallback: build from embed Containerfile
	return m.buildImage(tag)
}

// buildImage builds the PG image from embedded Containerfile and init.sh.
func (m *Manager) buildImage(tag string) error {
	fmt.Println("→ Building PostgreSQL + pgBackRest image...")

	buildDir := filepath.Join(m.dataDir, "build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("creating build directory: %w", err)
	}

	containerfile := filepath.Join(buildDir, "Containerfile")
	if err := os.WriteFile(containerfile, []byte(res.Containerfile), 0644); err != nil {
		return fmt.Errorf("writing Containerfile: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "init.sh"), []byte(res.InitShell), 0644); err != nil {
		return fmt.Errorf("writing init.sh: %w", err)
	}

	if err := m.runInteractive("build", "-t", tag, "-f", containerfile, buildDir); err != nil {
		return fmt.Errorf("podman build: %w", err)
	}

	return nil
}

// ─── Network management ──────────────────────────────────────────

// EnsureNetwork creates the shared podman network if it doesn't exist.
// All aifs containers (PG + backup) communicate via this bridge network,
// using container names as DNS hostnames.
func (m *Manager) EnsureNetwork() error {
	netName := m.cfg.Podman.Network
	exists, err := m.networkExists(netName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	fmt.Printf("→ Creating podman network: %s\n", netName)
	if _, err := m.run("network", "create", netName); err != nil {
		return fmt.Errorf("creating network %s: %w", netName, err)
	}
	return nil
}

func (m *Manager) networkExists(name string) (bool, error) {
	out, err := m.run("network", "ls", "--format", "{{.Name}}")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

// EnsureDirs creates required data directories on the host.
func (m *Manager) EnsureDirs() error {
	dirs := []string{
		m.cfg.Podman.DataDir,
		m.cfg.Podman.WALDir,
	}
	// Ensure backup dirs exist (shared, host directories)
	for _, dir := range []string{m.cfg.Backup.DataDir, m.cfg.Backup.LogDir} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating backup directory %s: %w", dir, err)
		}
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating data directory %s: %w", dir, err)
		}
		fmt.Printf("→ Data directory ensured: %s\n", dir)
	}
	return nil
}

// ─── Container management ─────────────────────────────────────────────

// ContainerStatus represents the running status of a container.
type ContainerStatus struct {
	Name    string
	Running bool
	Status  string
	Ports   string
}

// EnsureContainer ensures the PostgreSQL container is created and running.
// Creates the container if it does not exist, starts it if stopped.
// Caller is responsible for calling EnsureNetwork() first.
func (m *Manager) EnsureContainer() error {
	exists, err := m.containerExists(m.cfg.Podman.ContainerName)
	if err != nil {
		return err
	}

	if !exists {
		fmt.Println("→ Creating and starting PostgreSQL container...")
		return m.createContainer()
	}

	running, err := m.containerRunning(m.cfg.Podman.ContainerName)
	if err != nil {
		return err
	}
	if !running {
		fmt.Println("→ Starting PostgreSQL container...")
		return m.StartContainer()
	}

	fmt.Printf("→ Container %s is already running\n", m.cfg.Podman.ContainerName)
	return nil
}

// StartContainer starts an existing container.
func (m *Manager) StartContainer() error {
	if _, err := m.run("start", m.cfg.Podman.ContainerName); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}
	fmt.Println("  ✓ Container started (check readiness with: aifs status)")
	return nil
}

// StopContainer stops the container.
func (m *Manager) StopContainer() error {
	if _, err := m.run("stop", m.cfg.Podman.ContainerName); err != nil {
		return fmt.Errorf("stopping container: %w", err)
	}
	return nil
}

// Status returns detailed container status.
func (m *Manager) Status() (*ContainerStatus, error) {
	out, err := m.run("ps", "-a",
		"--filter", "name="+m.cfg.Podman.ContainerName,
		"--format", "{{.Names}}\t{{.Status}}\t{{.Ports}}",
	)
	if err != nil {
		return nil, fmt.Errorf("querying container status: %w", err)
	}

	out = strings.TrimSpace(out)
	if out == "" {
		return &ContainerStatus{Name: m.cfg.Podman.ContainerName, Status: "not created"}, nil
	}

	parts := strings.SplitN(out, "\t", 3)
	cs := &ContainerStatus{Name: m.cfg.Podman.ContainerName}
	if len(parts) >= 2 {
		cs.Status = parts[1]
		cs.Running = strings.HasPrefix(strings.ToLower(parts[1]), "up")
	}
	if len(parts) >= 3 {
		cs.Ports = parts[2]
	}
	return cs, nil
}

// Exec runs a command inside the container, returns stdout.
func (m *Manager) Exec(args ...string) (string, error) {
	execArgs := append([]string{"exec", m.cfg.Podman.ContainerName}, args...)
	return m.run(execArgs...)
}

// Destroy removes the container. Data directories on the host are preserved.
func (m *Manager) Destroy() error {
	fmt.Println("⚠️  Removing container (host data directories are preserved)")

	// Stopping and removing container
	m.run("stop", m.cfg.Podman.ContainerName)
	m.run("rm", "-f", m.cfg.Podman.ContainerName)

	fmt.Printf("  Data preserved at: %s\n", m.cfg.Podman.DataDir)
	fmt.Printf("  WAL preserved at:  %s\n", m.cfg.Podman.WALDir)
	return nil
}

// PGIsReady checks if PostgreSQL is accepting connections.
// Each podman exec call uses a 10s timeout to prevent hanging if podman is unresponsive.
func (m *Manager) PGIsReady() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	execArgs := append([]string{"exec", m.cfg.Podman.ContainerName}, "pg_isready", "-U", m.cfg.Postgres.User, "-d", m.cfg.Postgres.Database)
	out, err := m.runCtx(ctx, execArgs...)
	if err != nil {
		return false, nil // pg_isready failed = not ready
	}
	return strings.Contains(out, "accepting connections"), nil
}

// ─── Internal methods ─────────────────────────────────────────────

func (m *Manager) run(args ...string) (string, error) {
	slog.Debug("podman", "args", args)
	cmd := exec.Command(m.podman, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("podman %s: %s", strings.Join(args, " "), string(exitErr.Stderr))
		}
		return "", fmt.Errorf("podman %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// runCtx runs podman with a context. Use for exec calls that should not hang indefinitely.
func (m *Manager) runCtx(ctx context.Context, args ...string) (string, error) {
	slog.Debug("podman", "args", args)
	cmd := exec.CommandContext(ctx, m.podman, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("podman %s: %s", strings.Join(args, " "), string(exitErr.Stderr))
		}
		return "", fmt.Errorf("podman %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

func (m *Manager) runInteractive(args ...string) error {
	slog.Debug("podman", "args", args)
	cmd := exec.Command(m.podman, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func (m *Manager) imageExists(tag string) (bool, error) {
	out, err := m.run("images", "--format", "{{.Repository}}:{{.Tag}}")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == tag {
			return true, nil
		}
	}
	return false, nil
}

func (m *Manager) containerExists(name string) (bool, error) {
	out, err := m.run("ps", "-a", "--filter", "name="+name, "--format", "{{.Names}}")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

func (m *Manager) containerRunning(name string) (bool, error) {
	out, err := m.run("ps", "--filter", "name="+name, "--filter", "status=running", "--format", "{{.Names}}")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == name, nil
}

func (m *Manager) createContainer() error {
	// Generate per-instance pgbackrest.conf
	confPath, err := m.writeInstancePgbackrestConf()
	if err != nil {
		return fmt.Errorf("generating pgbackrest.conf: %w", err)
	}

	// Use the shared backup data volume so pgbackrest archive-push writes
	// to the same repo the backup container manages.
	backupVol := m.cfg.Backup.DataDir
	hostPort := m.cfg.Podman.HostPort

	args := []string{
		"run", "-d",
		"--name", m.cfg.Podman.ContainerName,
		"--network", m.cfg.Podman.Network,
		"-p", fmt.Sprintf("%d:5432", hostPort),
		"-v", fmt.Sprintf("%s:/var/lib/postgresql", m.cfg.Podman.DataDir),
		"-v", fmt.Sprintf("%s:/var/lib/pgbackrest", backupVol),
		"-v", fmt.Sprintf("%s:/etc/pgbackrest/pgbackrest.conf:ro", confPath),
		"-v", fmt.Sprintf("%s:/var/lib/postgresql/wal-archive", m.cfg.Podman.WALDir),
		"-e", fmt.Sprintf("POSTGRES_DB=%s", m.cfg.Postgres.Database),
		"-e", fmt.Sprintf("POSTGRES_USER=%s", m.cfg.Postgres.User),
		"-e", fmt.Sprintf("POSTGRES_PASSWORD=%s", m.cfg.Postgres.Password),
		"-e", fmt.Sprintf("PGBACKREST_STANZA=%s", m.cfg.PITR.PgBackRestStanza),
		m.cfg.Podman.ImageTag,
	}
	if _, err := m.run(args...); err != nil {
		return fmt.Errorf("creating container: %w", err)
	}
	fmt.Println("  ✓ Container created, PostgreSQL is initializing (check with: aifs status)")
	return nil
}

// writeInstancePgbackrestConf writes a per-instance pgbackrest.conf for the PG container.
// Returns the path to the config file to be mounted.
func (m *Manager) writeInstancePgbackrestConf() (string, error) {
	stanza := m.cfg.PITR.PgBackRestStanza
	content := fmt.Sprintf(`[%s]
pg1-path=/var/lib/postgresql/data

[global]
repo1-path=/var/lib/pgbackrest
repo1-retention-full=%d
log-level-console=info
`, stanza, m.cfg.Backup.RetentionFull)

	confPath := filepath.Join(m.dataDir, fmt.Sprintf("pgbackrest-%s.conf", m.cfg.Podman.ContainerName))
	if err := os.WriteFile(confPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("writing %s: %w", confPath, err)
	}
	return confPath, nil
}
