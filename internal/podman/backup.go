package podman

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
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
	"golang.org/x/crypto/ssh"
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

// SSHKeyPair is the on-disk path pair for the backup container SSH key.
type SSHKeyPair struct {
	Private string
	Public  string
}

// SSHKeyPaths returns the host paths to the backup SSH key pair.
func (m *BackupManager) SSHKeyPaths() SSHKeyPair {
	return SSHKeyPair{
		Private: filepath.Join(m.dataDir, "backup", "id_rsa"),
		Public:  filepath.Join(m.dataDir, "backup", "id_rsa.pub"),
	}
}

// SSHConfigPath returns the host path to the backup container's SSH client config.
func (m *BackupManager) SSHConfigPath() string {
	return filepath.Join(m.dataDir, "backup", "ssh_config")
}

// WriteSSHConfig writes an SSH client config that disables host key checking
// for PG containers inside the podman network.
func (m *BackupManager) WriteSSHConfig() (string, error) {
	conf := `Host aifs-pg-*
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    IdentityFile /root/.ssh/id_rsa
    User postgres
    Port 22
`
	path := m.SSHConfigPath()
	if err := os.WriteFile(path, []byte(conf), 0644); err != nil {
		return "", fmt.Errorf("writing ssh config: %w", err)
	}
	return path, nil
}

func (m *BackupManager) EnsureSSHKey() (*SSHKeyPair, error) {
	keys := m.SSHKeyPaths()
	if err := os.MkdirAll(filepath.Dir(keys.Private), 0700); err != nil {
		return nil, fmt.Errorf("creating backup ssh directory: %w", err)
	}

	if _, err := os.Stat(keys.Private); err == nil {
		return &keys, nil
	}

	fmt.Println("→ Generating backup container SSH key pair...")
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generating rsa key: %w", err)
	}

	privFile, err := os.OpenFile(keys.Private, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return nil, fmt.Errorf("creating private key file: %w", err)
	}
	defer privFile.Close()

	privPEM := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}
	if err := pem.Encode(privFile, privPEM); err != nil {
		return nil, fmt.Errorf("writing private key: %w", err)
	}

	pub, err := sshPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("formatting public key: %w", err)
	}
	if err := os.WriteFile(keys.Public, []byte(pub), 0644); err != nil {
		return nil, fmt.Errorf("writing public key: %w", err)
	}

	fmt.Println("  ✓ SSH key pair generated")
	return &keys, nil
}

// sshPublicKey returns an authorized_keys line for the given RSA public key.
func sshPublicKey(pub *rsa.PublicKey) (string, error) {
	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk))), nil
}

// AuthorizeKeyOnContainer installs the backup public key into a PG container.
func (m *BackupManager) AuthorizeKeyOnContainer(containerName string) error {
	keys := m.SSHKeyPaths()
	pub, err := os.ReadFile(keys.Public)
	if err != nil {
		return fmt.Errorf("reading public key: %w", err)
	}

	// Write authorized_keys file via exec, then ensure correct ownership/permissions.
	cmd := fmt.Sprintf("mkdir -p /etc/ssh/authorized_keys && echo '%s' > /etc/ssh/authorized_keys/postgres && chown postgres:postgres /etc/ssh/authorized_keys/postgres && chmod 600 /etc/ssh/authorized_keys/postgres", strings.TrimSpace(string(pub)))
	podmanArgs := []string{"exec", "-u", "root", containerName, "sh", "-c", cmd}
	if _, err := execWithTimeout(m.podman, podmanArgs, 30*time.Second); err != nil {
		return fmt.Errorf("installing authorized_keys on %s: %w", containerName, err)
	}
	return nil
}

// AuthorizeKeyOnInstance is a convenience wrapper for the currently selected instance.
func (m *BackupManager) AuthorizeKeyOnInstance() error {
	return m.AuthorizeKeyOnContainer(m.cfg.Podman.ContainerName)
}

// ─── Image management ─────────────────────────────────────────────

// EnsureBackupImage ensures the shared pgbackrest backup image is available.
// Tries podman pull first (for pre-built registry images), falls back to local build.
func (m *BackupManager) EnsureBackupImage() error {
	tag := m.cfg.Backup.ImageTag

	exists, err := m.imageExists(tag)
	if err != nil {
		return err
	}
	if exists {
		fmt.Printf("→ Backup image %s already exists, skipping pull/build\n", tag)
		return nil
	}

	// Try pull first
	fmt.Printf("→ Pulling backup image %s...\n", tag)
	if _, err := m.run("pull", tag); err == nil {
		fmt.Println("  ✓ Backup image pulled from registry")
		return nil
	}
	fmt.Printf("  Pull failed, falling back to local build...\n")

	// Fallback: build from embed backup.Containerfile
	return m.buildBackupImage(tag)
}

// buildBackupImage builds the backup image from embedded backup.Containerfile.
func (m *BackupManager) buildBackupImage(tag string) error {
	fmt.Println("→ Building pgbackrest backup image...")

	buildDir := filepath.Join(m.dataDir, "backup-build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("creating backup build directory: %w", err)
	}

	containerfile := filepath.Join(buildDir, "Containerfile")
	if err := os.WriteFile(containerfile, []byte(res.BackupContainerfile), 0644); err != nil {
		return fmt.Errorf("writing backup Containerfile: %w", err)
	}

	if err := m.runInteractive("build", "-t", tag, "-f", containerfile, buildDir); err != nil {
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

// EnsureBackupInfra prepares the shared backup infrastructure:
// network, image, directories, config, and container with current PG container IPs.
func (m *BackupManager) EnsureBackupInfra() error {
	if err := m.EnsureNetwork(); err != nil {
		return err
	}
	if err := m.EnsureBackupImage(); err != nil {
		return err
	}
	if err := m.EnsureBackupDirs(); err != nil {
		return err
	}
	confPath, err := m.WritePgbackrestConf()
	if err != nil {
		return err
	}
	hostEntries, err := m.collectPGContainerIPs()
	if err != nil {
		return err
	}
	return m.EnsureBackupContainer(confPath, hostEntries)
}

// collectPGContainerIPs returns the current IP addresses of all PITR-enabled
// PG containers. Containers that do not exist are skipped.
func (m *BackupManager) collectPGContainerIPs() (map[string]string, error) {
	hostEntries := make(map[string]string)
	for _, inst := range m.cfg.Instances {
		if !inst.PITR.Enabled || inst.Podman.ContainerName == "" {
			continue
		}
		out, err := exec.Command(m.podman, "inspect", inst.Podman.ContainerName,
			"--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}").Output()
		if err != nil {
			// Container may not exist yet; skip it.
			continue
		}
		ip := strings.TrimSpace(string(out))
		if ip == "" {
			continue
		}
		hostEntries[inst.Podman.ContainerName] = ip
	}
	return hostEntries, nil
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

		// pgbackrest runs from the backup container and connects to each PG
		// container via SSH (pg1-host). The PG container runs sshd as root
		// and accepts the backup container's public key for the postgres user.
		sb.WriteString("[")
		sb.WriteString(stanza)
		sb.WriteString("]\n")
		fmt.Fprintf(&sb, "pg1-host=%s\n", inst.Podman.ContainerName)
		fmt.Fprintf(&sb, "pg1-path=/var/lib/postgresql/data\n")
		fmt.Fprintf(&sb, "pg1-user=postgres\n\n")
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
// hostEntries maps PG container hostnames to IPs for /etc/hosts injection.
// Caller is responsible for calling EnsureNetwork() first.
func (m *BackupManager) EnsureBackupContainer(confPath string, hostEntries map[string]string) error {
	containerName := m.cfg.Backup.ContainerName

	exists, err := m.containerExists(containerName)
	if err != nil {
		return err
	}

	if exists {
		// Check whether the existing container has the required --add-host entries.
		// PG container IPs can change after stop/start, so recreate if stale.
		stale, err := m.hostEntriesStale(hostEntries)
		if err != nil {
			return fmt.Errorf("checking backup container host entries: %w", err)
		}
		if stale {
			fmt.Println("→ Backup container host entries are stale, recreating...")
			if _, err := m.run("rm", "-f", containerName); err != nil {
				return fmt.Errorf("removing stale backup container: %w", err)
			}
			return m.createBackupContainer(confPath, hostEntries)
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

	fmt.Println("→ Creating and starting backup container...")
	return m.createBackupContainer(confPath, hostEntries)
}

// hostEntriesStale returns true if the running backup container's ExtraHosts
// do not contain all required hostEntries.
func (m *BackupManager) hostEntriesStale(hostEntries map[string]string) (bool, error) {
	out, err := m.run("inspect", m.cfg.Backup.ContainerName, "--format", "{{json .HostConfig.ExtraHosts}}")
	if err != nil {
		return false, err
	}
	out = strings.TrimSpace(out)
	if out == "" || out == "null" {
		return len(hostEntries) > 0, nil
	}

	var extraHosts []string
	if err := json.Unmarshal([]byte(out), &extraHosts); err != nil {
		// Fallback: parse raw string slice if JSON unmarshal fails.
		extraHosts = strings.Split(strings.Trim(out, "[]"), " ")
	}

	current := make(map[string]string, len(extraHosts))
	for _, entry := range extraHosts {
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) == 2 {
			current[parts[0]] = parts[1]
		}
	}

	for host, ip := range hostEntries {
		if current[host] != ip {
			return true, nil
		}
	}
	return false, nil
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
// If tailLogs is true, the container's stdout/stderr is also streamed to the
// process stdout/stderr.
func (m *BackupManager) BackupExec(tailLogs bool, args ...string) (string, error) {
	podmanArgs := append([]string{"exec", "-i=false", m.cfg.Backup.ContainerName}, args...)
	if tailLogs {
		return execWithTimeoutStreaming(m.podman, podmanArgs, 10*time.Minute)
	}
	return execWithTimeout(m.podman, podmanArgs, 10*time.Minute)
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

func (m *BackupManager) createBackupContainer(confPath string, hostEntries map[string]string) error {
	keys := m.SSHKeyPaths()

	// Ensure SSH config is written before mounting.
	sshConfPath, err := m.WriteSSHConfig()
	if err != nil {
		return err
	}

	args := []string{
		"run", "-d",
		"--name", m.cfg.Backup.ContainerName,
		"--network", m.cfg.Podman.Network,
	}

	// Add /etc/hosts entries for PG containers so pgbackrest can resolve pg1-host.
	// Required when the rootless podman network backend does not provide DNS.
	for hostname, ip := range hostEntries {
		args = append(args, "--add-host", fmt.Sprintf("%s:%s", hostname, ip))
	}

	args = append(args,
		"-v", fmt.Sprintf("%s:/var/lib/pgbackrest", hostMountPath(m.cfg.Backup.DataDir)),
		"-v", fmt.Sprintf("%s:/var/log/pgbackrest", hostMountPath(m.cfg.Backup.LogDir)),
		"-v", fmt.Sprintf("%s:/etc/pgbackrest/pgbackrest.conf:ro", hostMountPath(confPath)),
		"-v", fmt.Sprintf("%s:/root/.ssh/id_rsa:ro", hostMountPath(keys.Private)),
		"-v", fmt.Sprintf("%s:/root/.ssh/id_rsa.pub:ro", hostMountPath(keys.Public)),
		"-v", fmt.Sprintf("%s:/root/.ssh/config:ro", hostMountPath(sshConfPath)),
		m.cfg.Backup.ImageTag,
	)

	if _, err := m.run(args...); err != nil {
		return fmt.Errorf("creating backup container: %w", err)
	}

	fmt.Println("  ✓ Backup container started")
	return nil
}
