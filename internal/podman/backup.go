package podman

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mars-base/aifs/internal/config"
	"github.com/mars-base/aifs/internal/platform"
	res "github.com/mars-base/aifs/embed"
)

// BackupManager manages the shared pgbackrest backup container.
// Unlike Manager (which is bound to a single PG instance after SetInstance),
// BackupManager operates on all instances configured in the config file.
type BackupManager struct {
	cfg     *config.Config
	podman  string // podman binary path
	dataDir string // aifs data directory (~/.aifs)
}

// NewBackupManager creates a BackupManager.
func NewBackupManager(cfg *config.Config) (*BackupManager, error) {
	path, err := exec.LookPath("podman")
	if err != nil {
		return nil, fmt.Errorf("podman is not installed: %w", err)
	}
	return &BackupManager{
		cfg:     cfg,
		podman:  path,
		dataDir: platform.DefaultConfigDir(),
	}, nil
}

// ─── Image management ─────────────────────────────────────────────

// EnsureBackupImage builds the shared pgbackrest backup image.
func (m *BackupManager) EnsureBackupImage() error {
	tag := m.cfg.Backup.ImageTag

	exists, err := m.imageExists(tag)
	if err != nil {
		return err
	}
	if exists {
		fmt.Printf("→ Backup image %s already exists, skipping build\n", tag)
		return nil
	}

	fmt.Println("→ Building pgbackrest backup image...")

	buildDir := filepath.Join(m.dataDir, "backup-build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("creating backup build directory: %w", err)
	}

	// Write backup.Containerfile
	containerfile := filepath.Join(buildDir, "Containerfile")
	if err := os.WriteFile(containerfile, []byte(res.BackupContainerfile), 0644); err != nil {
		return fmt.Errorf("writing backup Containerfile: %w", err)
	}

	if err := m.runInteractive("build",
		"-t", tag,
		"-f", containerfile,
		buildDir,
	); err != nil {
		return fmt.Errorf("podman build backup image: %w", err)
	}

	fmt.Println("  ✓ Backup image built:", tag)
	return nil
}

// ─── Network management ──────────────────────────────────────────

// EnsureNetwork creates the shared podman network if it doesn't exist.
func (m *BackupManager) EnsureNetwork() error {
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

func (m *BackupManager) networkExists(name string) (bool, error) {
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

// ─── Directory management ──────────────────────────────────────

// EnsureBackupDirs creates the backup data and log directories on the host.
func (m *BackupManager) EnsureBackupDirs() error {
	dirs := []string{
		m.cfg.Backup.DataDir,
		m.cfg.Backup.LogDir,
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating backup directory %s: %w", dir, err)
		}
		fmt.Printf("→ Backup directory ensured: %s\n", dir)
	}
	return nil
}

// ─── pgbackrest.conf generation ──────────────────────────────────

// WritePgbackrestConf generates pgbackrest.conf with all instance stanzas.
// Returns the path to the generated config file.
func (m *BackupManager) WritePgbackrestConf() (string, error) {
	var sb strings.Builder

	// Build stanza for each instance with PITR enabled
	for name, inst := range m.cfg.Instances {
		if !inst.PITR.Enabled {
			continue
		}
		stanza := inst.PITR.PgBackRestStanza
		if stanza == "" {
			stanza = "aifs_" + name
		}

		// PG container name for inter-container DNS on the bridge network
		pgContainer := inst.Podman.ContainerName
		if pgContainer == "" {
			pgContainer = "aifs-pg-" + name
		}

		sb.WriteString("[")
		sb.WriteString(stanza)
		sb.WriteString("]\n")
		fmt.Fprintf(&sb, "pg1-host=%s\n", pgContainer)
		fmt.Fprintf(&sb, "pg1-path=/var/lib/postgresql/data\n\n")
	}

	// Global section
	sb.WriteString("[global]\n")
	sb.WriteString("repo1-path=/var/lib/pgbackrest\n")
	fmt.Fprintf(&sb, "repo1-retention-full=%d\n", m.cfg.Backup.RetentionFull)
	sb.WriteString("log-level-console=info\n")
	sb.WriteString("start-fast=y\n")
	sb.WriteString("compress-type=zst\n")

	confPath := filepath.Join(m.dataDir, "pgbackrest.conf")
	if err := os.WriteFile(confPath, []byte(sb.String()), 0644); err != nil {
		return "", fmt.Errorf("writing pgbackrest.conf: %w", err)
	}

	fmt.Printf("→ pgbackrest.conf generated: %s (%d stanzas)\n", confPath, len(m.cfg.Instances))
	return confPath, nil
}

// ─── Container management ─────────────────────────────────────────────

// EnsureBackupContainer creates and starts the backup container if it doesn't exist.
func (m *BackupManager) EnsureBackupContainer(confPath string) error {
	// Ensure shared network exists first
	if err := m.EnsureNetwork(); err != nil {
		return err
	}

	containerName := m.cfg.Backup.ContainerName

	exists, err := m.containerExists(containerName)
	if err != nil {
		return err
	}

	if !exists {
		fmt.Println("→ Creating and starting backup container...")
		return m.createBackupContainer(confPath)
	}

	running, err := m.containerRunning(containerName)
	if err != nil {
		return err
	}
	if !running {
		fmt.Println("→ Starting backup container...")
		return m.StartBackupContainer()
	}

	fmt.Printf("→ Backup container %s is already running\n", containerName)
	return nil
}

// StartBackupContainer starts the backup container.
func (m *BackupManager) StartBackupContainer() error {
	if _, err := m.run("start", m.cfg.Backup.ContainerName); err != nil {
		return fmt.Errorf("starting backup container: %w", err)
	}
	return nil
}

// StopBackupContainer stops the backup container.
func (m *BackupManager) StopBackupContainer() error {
	if _, err := m.run("stop", m.cfg.Backup.ContainerName); err != nil {
		return fmt.Errorf("stopping backup container: %w", err)
	}
	return nil
}

// BackupContainerStatus returns the backup container status.
func (m *BackupManager) BackupContainerStatus() (*ContainerStatus, error) {
	out, err := m.run("ps", "-a",
		"--filter", "name="+m.cfg.Backup.ContainerName,
		"--format", "{{.Names}}\t{{.Status}}\t{{.Ports}}",
	)
	if err != nil {
		return nil, fmt.Errorf("querying backup container status: %w", err)
	}

	out = strings.TrimSpace(out)
	if out == "" {
		return &ContainerStatus{Name: m.cfg.Backup.ContainerName, Status: "not created"}, nil
	}

	parts := strings.SplitN(out, "\t", 3)
	cs := &ContainerStatus{Name: m.cfg.Backup.ContainerName}
	if len(parts) >= 2 {
		cs.Status = parts[1]
		cs.Running = strings.HasPrefix(strings.ToLower(parts[1]), "up")
	}
	if len(parts) >= 3 {
		cs.Ports = parts[2]
	}
	return cs, nil
}

// CheckContainerRunning checks if a container with the given name is running.
func (m *BackupManager) CheckContainerRunning(name string) (bool, error) {
	return m.containerRunning(name)
}

// BackupExec runs a command inside the backup container.
func (m *BackupManager) BackupExec(args ...string) (string, error) {
	execArgs := append([]string{"exec", m.cfg.Backup.ContainerName}, args...)
	return m.run(execArgs...)
}

// ─── Internal methods ─────────────────────────────────────────────

func (m *BackupManager) run(args ...string) (string, error) {
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

func (m *BackupManager) runInteractive(args ...string) error {
	slog.Debug("podman", "args", args)
	cmd := exec.Command(m.podman, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func (m *BackupManager) imageExists(tag string) (bool, error) {
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

func (m *BackupManager) containerExists(name string) (bool, error) {
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

func (m *BackupManager) containerRunning(name string) (bool, error) {
	out, err := m.run("ps", "--filter", "name="+name, "--filter", "status=running", "--format", "{{.Names}}")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == name, nil
}

func (m *BackupManager) createBackupContainer(confPath string) error {
	args := []string{
		"run", "-d",
		"--name", m.cfg.Backup.ContainerName,
		"--network", m.cfg.Podman.Network,
		"-v", fmt.Sprintf("%s:/var/lib/pgbackrest", m.cfg.Backup.DataDir),
		"-v", fmt.Sprintf("%s:/var/log/pgbackrest", m.cfg.Backup.LogDir),
		"-v", fmt.Sprintf("%s:/etc/pgbackrest/pgbackrest.conf:ro", confPath),
	}

	// Mount WAL directories for each PITR-enabled instance
	for name, inst := range m.cfg.Instances {
		if !inst.PITR.Enabled {
			continue
		}
		args = append(args, "-v", fmt.Sprintf("%s:/wal/%s:ro", inst.Podman.WALDir, name))
	}

	args = append(args, m.cfg.Backup.ImageTag)

	if _, err := m.run(args...); err != nil {
		return fmt.Errorf("creating backup container: %w", err)
	}

	fmt.Println("  ✓ Backup container started")
	return nil
}
