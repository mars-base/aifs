package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

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
	Name      string `json:"name"`
	Status    string `json:"status"`
	Running   bool   `json:"running"`
	Port      int    `json:"port"`
	MountPath string `json:"mountPath"`
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

	var result []InstanceInfo
	for name := range cfg.Instances {
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
		info.Port = cfg.Postgres.Port
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
		if err == nil {
			pt := pitr.New(cfg, pm, bm)
			if err := pt.EnsureStanza(); err != nil {
				fmt.Printf("  [!] stanza warning: %v\n", err)
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
func (a *App) MountInstance(name, mountpoint string) error {
	cfg, err := loadCfg(name)
	if err != nil {
		return err
	}
	fsCfg := cfg.EffectiveFilesystem()
	rec := pgfs.MountRecord{
		MountPoint: mountpoint,
		Instance:   cfg.Instance,
		PID:        os.Getpid(),
	}
	onMounted := func() {
		if err := pgfs.AddMountState(rec); err != nil {
			// Non-fatal: status display may miss this mount, but the FS works fine.
			_ = err
		}
	}
	mountErr := pgfs.Mount(a.ctx, cfg.GetPostgresURL(), fsCfg.TablePrefix, cfg.Podman.DataDir, mountpoint, onMounted)
	_ = pgfs.RemoveMountState(mountpoint)
	return mountErr
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

// CreateSnapshot creates a backup snapshot of the given type (full/diff/incr).
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
	_, err = pt.CreateSnapshot(snapType, false)
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
	return pt.Restore(t, promote, false, false)
}

// --- Bench ---------------------------------------------------------------

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

// GetConfigPath returns the path to the aifs config file.
func (a *App) GetConfigPath() string {
	return platform.DefaultConfigPath()
}

// OpenConfigFile opens the aifs config file in the system default editor.
func (a *App) OpenConfigFile() {
	path := platform.DefaultConfigPath()
	var cmd *exec.Cmd
	switch runtime.GOOS {
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
