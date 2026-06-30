package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/mars-base/aifs/internal/bench"
	"github.com/mars-base/aifs/internal/config"
	"github.com/mars-base/aifs/internal/pgfs"
	"github.com/mars-base/aifs/internal/pitr"
	"github.com/mars-base/aifs/internal/platform"
	"github.com/mars-base/aifs/internal/podman"
)

// App is the main application struct that exposes methods to the frontend
// via Wails bindings.
type App struct {
	ctx context.Context
}

// NewApp creates a new App instance.
func NewApp() *App {
	return &App{}
}

// startup is called when the Wails app starts. The context is saved so
// runtime methods can use it.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// InstanceInfo is the data returned to the frontend for each instance.
type InstanceInfo struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Running     bool   `json:"running"`
	PgURL       string `json:"pgUrl"`
	MountPath   string `json:"mountPath"`
	IsFormatted bool   `json:"isFormatted"`
}

// loadCfg loads the aifs config and sets the given instance.
func loadCfg(name string) (*config.Config, error) {
	path := platform.DefaultConfigPath()
	cfg, err := config.Load(path)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if name != "" {
		if err := cfg.SetInstance(name); err != nil {
			return nil, fmt.Errorf("setting instance %q: %w", name, err)
		}
	}
	return cfg, nil
}

// ListInstances returns all configured instances with their status.
func (a *App) ListInstances() ([]InstanceInfo, error) {
	cfg, err := loadCfg("")
	if err != nil {
		return nil, err
	}

	// Build a map of instance name → active mount points.
	// FindInstanceMounts uses the state file first; falls back to scanning
	// running processes (Linux: /proc cmdlines, macOS: ps) so that mounts
	// created before state tracking was introduced are also visible.
	mountMap := map[string]string{}
	for instName := range cfg.Instances {
		if mps, err := pgfs.FindInstanceMounts(instName); err == nil && len(mps) > 0 {
			mountMap[instName] = mps[0] // show the first (usually only) mount point
		}
	}

	// Collect instance names in sorted order so the list is stable across calls.
	names := make([]string, 0, len(cfg.Instances))
	for name := range cfg.Instances {
		names = append(names, name)
	}
	sort.Strings(names)

	var result []InstanceInfo
	for _, name := range names {
		info := InstanceInfo{Name: name, MountPath: mountMap[name]}

		if err := cfg.SetInstance(name); err != nil {
			info.Status = "error"
			result = append(result, info)
			continue
		}

		pm, err := podman.New(cfg)
		if err != nil {
			info.Status = "error"
			result = append(result, info)
			continue
		}

		cs, err := pm.Status()
		if err != nil {
			info.Status = "error"
		} else if cs != nil {
			info.Status = cs.Status
			info.Running = cs.Running
		}
		info.PgURL = cfg.GetPostgresURL()

		// Check if the filesystem has been formatted (only meaningful when running).
		if info.Running {
			ctx := context.Background()
			db, m, _, err := pgfs.Open(ctx, cfg.GetPostgresURL(), cfg.Filesystem.TablePrefix)
			if err == nil {
				if _, err := m.Load(ctx); err == nil {
					info.IsFormatted = true
				}
				db.Close()
			}
		}

		result = append(result, info)
	}
	return result, nil
}

// StartInstance starts the PostgreSQL container for the given instance.
func (a *App) StartInstance(name string) error {
	cfg, err := loadCfg(name)
	if err != nil {
		return err
	}

	pm, err := podman.New(cfg)
	if err != nil {
		return err
	}

	if err := pm.EnsureMachine(); err != nil {
		return fmt.Errorf("ensuring podman machine: %w", err)
	}
	if err := pm.EnsureImage(); err != nil {
		return fmt.Errorf("ensuring image: %w", err)
	}
	if err := pm.EnsureDirs(); err != nil {
		return fmt.Errorf("ensuring directories: %w", err)
	}
	if err := pm.EnsureNetwork(); err != nil {
		return fmt.Errorf("ensuring network: %w", err)
	}
	if err := pm.EnsureContainer(); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	// Wait for PG to be ready.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if ok, _ := pm.PGIsReady(); ok {
			break
		}
		time.Sleep(1 * time.Second)
	}

	// Apply performance tuning.
	if _, err := pm.ApplyPGTuning(); err != nil {
		// Non-fatal: log but don't fail start.
		fmt.Printf("  [!] pg tuning warning: %v\n", err)
	}

	// Initialize PITR stanza if enabled.
	if cfg.PITR.Enabled {
		bm, err := podman.NewBackupManager(cfg)
		if err != nil {
			return fmt.Errorf("creating backup manager: %w", err)
		}

		if _, err := bm.EnsureSSHKey(); err != nil {
			fmt.Printf("  [!] backup ssh key warning: %v\n", err)
		}

		if err := bm.AuthorizeKeyOnInstance(); err != nil {
			fmt.Printf("  [!] backup key authorization warning: %v\n", err)
		}

		if err := bm.EnsureBackupInfra(); err != nil {
			return fmt.Errorf("backup infrastructure: %w", err)
		}

		pt := pitr.New(cfg, pm, bm)
		if err := pt.EnsureStanza(); err != nil {
			fmt.Printf("  [!] stanza create warning: %v\n", err)
		}

		stanza := cfg.PITR.PgBackRestStanza
		archiveCmd := fmt.Sprintf("pgbackrest --stanza=%s archive-push %%p", stanza)
		setSQL := fmt.Sprintf("ALTER SYSTEM SET archive_command TO '%s'", archiveCmd)
		if _, err := pm.Exec("psql", "-U", cfg.Postgres.User, "-d", cfg.Postgres.Database, "-c", setSQL); err != nil {
			fmt.Printf("  [!] setting archive_command: %v\n", err)
		}
		pm.Exec("psql", "-U", cfg.Postgres.User, "-d", cfg.Postgres.Database, "-c", "SELECT pg_reload_conf()")
		pm.Exec("psql", "-U", cfg.Postgres.User, "-d", cfg.Postgres.Database, "-c", "SELECT pg_switch_wal()")

		if err := pt.CheckStanza(); err != nil {
			fmt.Printf("  [!] stanza check warning: %v\n", err)
		}

		// Wait for WAL archiver to catch up.
		for i := 0; i < 30; i++ {
			time.Sleep(2 * time.Second)
			out, err := pm.Exec("psql", "-U", cfg.Postgres.User, "-d", cfg.Postgres.Database,
				"-tAc", "SELECT last_archived_wal FROM pg_stat_archiver WHERE last_archived_wal IS NOT NULL")
			if err == nil && strings.TrimSpace(out) != "" {
				break
			}
		}
	}

	return nil
}

// StopInstance stops the PostgreSQL container for the given instance.
func (a *App) StopInstance(name string) error {
	cfg, err := loadCfg(name)
	if err != nil {
		return err
	}
	pm, err := podman.New(cfg)
	if err != nil {
		return err
	}
	return pm.StopContainer()
}

// MountInstance mounts the filesystem for the given instance at mountpoint.
// It starts a detached aifs child process (not an in-process goroutine) so
// the mount survives GUI restarts.
func (a *App) MountInstance(name, mountpoint string) error {
	cfg, err := loadCfg(name)
	if err != nil {
		return err
	}

	// Expand leading ~ to the user's home directory.
	mountpoint = expandHome(mountpoint)

	// Ensure the mount point directory exists.
	if err := os.MkdirAll(mountpoint, 0755); err != nil {
		return fmt.Errorf("creating mount point: %w", err)
	}

	// Resolve the aifs binary: prefer a sibling "aifs" next to this GUI binary,
	// fall back to whatever is on $PATH.
	aifsBin, err := resolveAifsBin()
	if err != nil {
		return fmt.Errorf("resolving aifs binary: %w", err)
	}

	// Build args: aifs -c <cfg> -i <instance> mount <mountpoint>
	cfgPath := platform.DefaultConfigPath()
	args := []string{"-c", cfgPath, "-i", cfg.Instance, "mount", mountpoint}

	return pgfs.MountBackground(aifsBin, args, mountpoint)
}

// UmountInstance unmounts the filesystem at the given mountpoint.
func (a *App) UmountInstance(mountpoint string) error {
	return pgfs.Umount(mountpoint)
}

// --- Snapshot management -------------------------------------------------

// ListSnapshots returns the snapshots for the given instance.
func (a *App) ListSnapshots(name string) ([]pitr.Snapshot, error) {
	cfg, err := loadCfg(name)
	if err != nil {
		return nil, err
	}
	if !cfg.PITR.Enabled {
		return nil, fmt.Errorf("PITR is not enabled for instance %q", name)
	}
	pm, err := podman.New(cfg)
	if err != nil {
		return nil, err
	}
	bm, err := podman.NewBackupManager(cfg)
	if err != nil {
		return nil, err
	}
	pt := pitr.New(cfg, pm, bm)
	return pt.ListSnapshots(0)
}

// CreateSnapshot creates a backup snapshot of the given type (full/diff).
// Progress is streamed to the frontend via the "snapshot-log" Wails event.
// Each event payload is a single log line string.
func (a *App) CreateSnapshot(name, snapType string) error {
	cfg, err := loadCfg(name)
	if err != nil {
		return err
	}
	pm, err := podman.New(cfg)
	if err != nil {
		return err
	}
	bm, err := podman.NewBackupManager(cfg)
	if err != nil {
		return err
	}
	if err := bm.EnsureBackupInfra(); err != nil {
		return err
	}
	if err := bm.AuthorizeKeyOnInstance(); err != nil {
		return fmt.Errorf("authorizing backup key: %w", err)
	}
	pt := pitr.New(cfg, pm, bm)

	// eventWriter forwards each Write call as a "snapshot-log" Wails event so
	// the frontend can display real-time progress.
	w := &eventWriter{ctx: a.ctx, event: "snapshot-log"}
	_, err = pt.CreateSnapshotToWriter(w, snapType)
	return err
}

// DeleteSnapshot deletes the named snapshot for the given instance.
func (a *App) DeleteSnapshot(name, snapName string) error {
	cfg, err := loadCfg(name)
	if err != nil {
		return err
	}
	pm, err := podman.New(cfg)
	if err != nil {
		return err
	}
	bm, err := podman.NewBackupManager(cfg)
	if err != nil {
		return err
	}
	pt := pitr.New(cfg, pm, bm)
	return pt.DeleteSnapshot(snapName)
}

// RestoreInstance performs PITR for the given instance.
// targetTime must be in format "2006-01-02 15:04:05+00".
func (a *App) RestoreInstance(name, targetTime string, promote bool) error {
	cfg, err := loadCfg(name)
	if err != nil {
		return err
	}

	var t time.Time
	for _, layout := range []string{
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05-07",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
	} {
		if t, err = time.Parse(layout, targetTime); err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("invalid time format %q: use YYYY-MM-DD HH:MM:SS+00", targetTime)
	}

	pm, err := podman.New(cfg)
	if err != nil {
		return err
	}
	bm, err := podman.NewBackupManager(cfg)
	if err != nil {
		return err
	}
	pt := pitr.New(cfg, pm, bm)
	w := &eventWriter{ctx: a.ctx, event: "restore-log"}
	return pt.RestoreToWriter(w, t, promote)
}

// FormatInstance initialises the PG-backed filesystem for the given instance.
// This must be called once after the instance is started for the first time,
// before any mount can succeed.
func (a *App) FormatInstance(name string) error {
	cfg, err := loadCfg(name)
	if err != nil {
		return err
	}
	ctx := context.Background()
	_, err = pgfs.Format(ctx, cfg.GetPostgresURL(), cfg.Filesystem.VolumeName, cfg.Filesystem.TablePrefix, false)
	return err
}

// --- Config setup --------------------------------------------------------

// ConfigStatus describes the current state of the config file.
type ConfigStatus struct {
	Exists  bool   `json:"exists"`
	Path    string `json:"path"`
	BaseDir string `json:"baseDir"`
}

// GetConfigStatus returns whether the config file exists and its key fields.
func (a *App) GetConfigStatus() ConfigStatus {
	path := platform.DefaultConfigPath()
	st := ConfigStatus{Path: path}

	// Check if the file actually exists on disk first — config.Load()
	// returns defaults (nil error) even when the file is missing, so we
	// can't rely on its error to detect existence.
	if _, err := os.Stat(path); err != nil {
		return st // file doesn't exist, Exists remains false
	}

	cfg, err := config.Load(path)
	if err == nil {
		st.Exists = true
		st.BaseDir = cfg.BaseDir
	}
	return st
}

// InitConfig creates a default config file. baseDir is optional.
// Returns an error if the config already exists.
func (a *App) InitConfig(baseDir string) error {
	path := platform.DefaultConfigPath()
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config file already exists: %s", path)
	}

	cfg := config.Default()
	if baseDir != "" {
		baseDir = expandHome(baseDir)
		if info, err := os.Stat(baseDir); err == nil && !info.IsDir() {
			return fmt.Errorf("base-dir %s exists but is not a directory", baseDir)
		}
		cfg.BaseDir = baseDir
		cfg.Backup.DataDir = filepath.Join(baseDir, "backup", "data")
		cfg.Backup.LogDir = filepath.Join(baseDir, "backup", "log")
	}

	cfg.ApplyDefaults()
	return cfg.Save(path)
}

// --- Instance creation ---------------------------------------------------

// CreateInstanceRequest holds the parameters for creating a new instance.
// Only Name is required; DataDir and PITREnabled are optional.
// Password is auto-generated.
type CreateInstanceRequest struct {
	Name        string `json:"name"`
	DataDir     string `json:"data_dir"`    // PG data dir, uses default if empty
	PITREnabled bool   `json:"pitr_enabled"`
}

// CreateInstance adds a new instance to the config file and saves it.
// Port numbers are auto-assigned. The instance is not started automatically.
func (a *App) CreateInstance(req CreateInstanceRequest) error {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return fmt.Errorf("instance name must not be empty")
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return fmt.Errorf("instance name %q contains invalid character %q (use letters, digits, - or _)", name, string(c))
		}
	}

	cfgPath := platform.DefaultConfigPath()
	cfg, err := loadCfg("")
	if err != nil {
		return err
	}
	if _, exists := cfg.Instances[name]; exists {
		return fmt.Errorf("instance %q already exists", name)
	}

	inst := cfg.InstanceDefaults(name)
	inst.Postgres.Password = generatePassword(16)

	if req.DataDir != "" {
		inst.Podman.DataDir = expandHome(req.DataDir)
	}
	inst.PITR.Enabled = req.PITREnabled

	cfg.Instances[name] = *inst
	cfg.ApplyDefaults()

	return cfg.Save(cfgPath)
}

// generatePassword returns a random alphanumeric string of length n.
func generatePassword(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// fallback: use timestamp-seeded values
		for i := range buf {
			buf[i] = chars[i%len(chars)]
		}
	} else {
		for i, b := range buf {
			buf[i] = chars[int(b)%len(chars)]
		}
	}
	return string(buf)
}



// BenchResult holds the results returned to the frontend.
type BenchResult struct {
	WriteBigMiBs        float64 `json:"writeBigMiBs"`
	WriteBigSecsPerFile float64 `json:"writeBigSecsPerFile"`
	ReadBigMiBs         float64 `json:"readBigMiBs"`
	ReadBigSecsPerFile  float64 `json:"readBigSecsPerFile"`
	WriteSmallPerSec    float64 `json:"writeSmallPerSec"`
	WriteSmallMsPerFile float64 `json:"writeSmallMsPerFile"`
	ReadSmallPerSec     float64 `json:"readSmallPerSec"`
	ReadSmallMsPerFile  float64 `json:"readSmallMsPerFile"`
	StatPerSec          float64 `json:"statPerSec"`
	StatMsPerFile       float64 `json:"statMsPerFile"`
}

// RunBench runs a filesystem benchmark at the given path.
// bigSize and smallSize are human strings like "100M", "128K" (empty = default).
func (a *App) RunBench(path, bigSize string, threads int) (BenchResult, error) {
	cfg := bench.DefaultConfig()
	if threads > 0 {
		cfg.Threads = threads
	}
	if bigSize != "" {
		sz, err := parseSizeStr(bigSize)
		if err != nil {
			return BenchResult{}, err
		}
		cfg.BigSize = sz
	}

	res, err := bench.Run(path, cfg)
	if err != nil {
		return BenchResult{}, err
	}
	return BenchResult{
		WriteBigMiBs:        res.WriteBigMiBs,
		WriteBigSecsPerFile: res.WriteBigSecsPerFile,
		ReadBigMiBs:         res.ReadBigMiBs,
		ReadBigSecsPerFile:  res.ReadBigSecsPerFile,
		WriteSmallPerSec:    res.WriteSmallPerSec,
		WriteSmallMsPerFile: res.WriteSmallMsPerFile,
		ReadSmallPerSec:     res.ReadSmallPerSec,
		ReadSmallMsPerFile:  res.ReadSmallMsPerFile,
		StatPerSec:          res.StatPerSec,
		StatMsPerFile:       res.StatMsPerFile,
	}, nil
}

// parseSizeStr parses "1G"/"512M"/"128K" into bytes.
func parseSizeStr(s string) (int64, error) {
	if s == "" || s == "0" {
		return 0, nil
	}
	mul := int64(1)
	upper := s
	if len(upper) > 0 {
		switch upper[len(upper)-1] {
		case 'G', 'g':
			mul = 1024 * 1024 * 1024
			s = s[:len(s)-1]
		case 'M', 'm':
			mul = 1024 * 1024
			s = s[:len(s)-1]
		case 'K', 'k':
			mul = 1024
			s = s[:len(s)-1]
		}
	}
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, fmt.Errorf("cannot parse %q as a size", s)
	}
	return n * mul, nil
}

// --- Utilities -----------------------------------------------------------

// eventWriter is an io.Writer that emits each chunk as a Wails runtime event.
// The frontend can subscribe to these events for real-time log display.
type eventWriter struct {
	ctx   context.Context
	event string
}

func (w *eventWriter) Write(p []byte) (int, error) {
	wailsruntime.EventsEmit(w.ctx, w.event, string(p))
	return len(p), nil
}

// resolveAifsBin finds the aifs CLI binary.
// It first looks for an "aifs" (or "aifs.exe" on Windows) sibling next to
// the running GUI binary, then falls back to $PATH lookup.
func resolveAifsBin() (string, error) {
	self, err := os.Executable()
	if err == nil {
		name := "aifs"
		if goruntime.GOOS == "windows" {
			name = "aifs.exe"
		}
		candidate := filepath.Join(filepath.Dir(self), name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	p, err := exec.LookPath("aifs")
	if err != nil {
		return "", fmt.Errorf("aifs binary not found in PATH or next to GUI binary")
	}
	return p, nil
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err == nil {
			path = home + path[1:]
		}
	}
	return path
}

// GetConfigPath returns the path to the aifs config file.
func (a *App) GetConfigPath() string {
	return platform.DefaultConfigPath()
}

// OpenConfigFile opens the aifs config file in the system default editor.
func (a *App) OpenConfigFile() {
	path := platform.DefaultConfigPath()
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "windows":
		cmd = exec.Command("notepad", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		editor := "xdg-open"
		cmd = exec.Command(editor, path)
	}
	_ = cmd.Start()
}
