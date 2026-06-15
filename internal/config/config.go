// Package config provides loading, validation, merging, and saving of aifs configuration files.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/mars-base/aifs/internal/platform"
)

// Config is the complete aifs configuration.
type Config struct {
	BaseDir    string                     `yaml:"base_dir,omitempty"`
	Network    string                     `yaml:"network,omitempty"` // shared podman network name, persisted at top level
	Postgres   PostgresConfig             `yaml:"postgres"`
	Filesystem FilesystemConfig           `yaml:"filesystem"`
	Podman     PodmanConfig               `yaml:"podman"`
	PITR       PITRConfig                 `yaml:"pitr"`
	Logging    LoggingConfig              `yaml:"logging"`
	Backup     BackupConfig               `yaml:"backup"`
	Instances  map[string]InstanceConfig  `yaml:"instances"`

	Instance string `yaml:"-"` // current instance name (set at runtime, not persisted)
}

// InstanceConfig is the configuration for a single database instance.
type InstanceConfig struct {
	Postgres PostgresConfig `yaml:"postgres"`
	Podman   PodmanConfig   `yaml:"podman"`
	PITR     PITRConfig     `yaml:"pitr"`
}

// PostgresConfig holds PostgreSQL connection settings.
type PostgresConfig struct {
	URL      string `yaml:"url"`       // connection string (postgres://user:pass@host:port/db)
	Host     string `yaml:"host"`      // host, default localhost
	Port     int    `yaml:"port"`      // port, default 5432
	User     string `yaml:"user"`      // user, default aifs
	Password string `yaml:"password"`  // password, default aifs
	Database string `yaml:"database"`  // database name, default aifs
}

// FilesystemConfig holds JuiceFS filesystem settings.
type FilesystemConfig struct {
	Name         string `yaml:"name"`          // filesystem name
	MountPoint   string `yaml:"mount_point"`   // mount point, "auto" uses platform default
	CacheDir     string `yaml:"cache_dir"`     // cache directory, "auto" uses platform default
	CacheSizeMB  int    `yaml:"cache_size_mb"` // cache size in MiB, default 10240
}

// PodmanConfig holds Podman container settings.
type PodmanConfig struct {
	ContainerName string `yaml:"container_name"` // PG container name, default aifs-pg
	DataDir       string `yaml:"data_dir"`       // PG data directory (host path), default ~/.aifs/dbdata/<name>/data
	WALDir        string `yaml:"wal_dir"`        // WAL archive directory (host path), default ~/.aifs/dbdata/<name>/wal
	ImageTag      string `yaml:"image_tag"`      // image tag, default ghcr.io/mars-base/aifs/aifs-pg:18-2.58.0
	HostPort      int    `yaml:"host_port"`       // host port for PG mapping, 0=auto-assign from 25432
	Network       string `yaml:"network"`         // podman network name, default aifs-net
}

// PITRConfig holds PITR backup/restore settings.
type PITRConfig struct {
	Enabled          bool   `yaml:"enabled"`           // whether PITR is enabled
	PgBackRestStanza string `yaml:"pgbackrest_stanza"` // pgBackRest stanza name
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level string `yaml:"level"` // debug / info / warn / error, default info
}

// BackupConfig holds shared pgbackrest backup container settings.
type BackupConfig struct {
	ContainerName string `yaml:"container_name"` // backup container name, default aifs-backup
	ImageTag      string `yaml:"image_tag"`      // backup image tag, default aifs-backup:2.58.0
	DataDir       string `yaml:"data_dir"`       // pgbackrest repo dir, default ~/.aifs/backup/data
	LogDir        string `yaml:"log_dir"`        // pgbackrest log dir, default ~/.aifs/backup/log
	RetentionFull int    `yaml:"retention_full"`  // number of full backups to retain, default 7
}

// Default returns a Config populated with default values.
func Default() *Config {
	return &Config{
		BaseDir: "", // empty means use platform default
		Postgres: PostgresConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "aifs",
			Password: "aifs",
			Database: "aifs",
		},
		Filesystem: FilesystemConfig{
			Name:        "aifs",
			MountPoint:  "auto",
			CacheDir:    "auto",
			CacheSizeMB: 10240,
		},
		Podman: PodmanConfig{
			ContainerName: "aifs-pg",
			DataDir:       filepath.Join(platform.DefaultConfigDir(), "dbdata", "data"),
			WALDir:        filepath.Join(platform.DefaultConfigDir(), "dbdata", "wal"),
			ImageTag:      "ghcr.io/mars-base/aifs/aifs-pg:18-2.58.0",
			Network:       "aifs-net",
		},
		PITR: PITRConfig{
			Enabled:          true,
			PgBackRestStanza: "aifs",
		},
		Logging: LoggingConfig{
			Level: "info",
		},
		Backup: BackupConfig{
			ContainerName: "aifs-backup",
			ImageTag:      "ghcr.io/mars-base/aifs/aifs-backup:2.58.0",
			DataDir:       filepath.Join(platform.DefaultConfigDir(), "backup", "data"),
			LogDir:        filepath.Join(platform.DefaultConfigDir(), "backup", "log"),
			RetentionFull: 7,
		},
		Instances: make(map[string]InstanceConfig),
	}
}

// InstanceDefaults returns default configuration for the named instance.
// Container names, stanza names, etc. are derived from the instance name.
// If BaseDir is set, data paths are relative to it; otherwise uses platform default.
func (c *Config) InstanceDefaults(name string) *InstanceConfig {
	baseDir := platform.DefaultConfigDir()
	if c.BaseDir != "" {
		baseDir = c.BaseDir
	}
	return &InstanceConfig{
		Postgres: PostgresConfig{
			Host:     c.Postgres.Host,
			Port:     5432,
			User:     c.Postgres.User,
			Password: c.Postgres.Password,
			Database: name + "_db",
		},
		Podman: PodmanConfig{
			ContainerName: "aifs-pg-" + name,
			DataDir:       filepath.Join(baseDir, "dbdata", name, "data"),
			WALDir:        filepath.Join(baseDir, "dbdata", name, "wal"),
			ImageTag:      c.Podman.ImageTag,
			HostPort:      0, // auto-assigned starting from 25432
		},
		PITR: PITRConfig{
			Enabled:          true,
			PgBackRestStanza: "aifs_" + name,
		},
	}
}

// SetInstance merges the named instance's configuration into top-level fields.
// Instance-level values take precedence over global defaults.
func (c *Config) SetInstance(name string) error {
	c.Instance = name

	inst, ok := c.Instances[name]
	if !ok {
		return fmt.Errorf("instance %q not found in config", name)
	}

	// Merge Postgres config
	if inst.Postgres.Host != "" {
		c.Postgres.Host = inst.Postgres.Host
	}
	if inst.Postgres.Port != 0 {
		c.Postgres.Port = inst.Postgres.Port
	}
	if inst.Postgres.User != "" {
		c.Postgres.User = inst.Postgres.User
	}
	if inst.Postgres.Password != "" {
		c.Postgres.Password = inst.Postgres.Password
	}
	if inst.Postgres.Database != "" {
		c.Postgres.Database = inst.Postgres.Database
	}
	if inst.Postgres.URL != "" {
		c.Postgres.URL = inst.Postgres.URL
	}

	// Merge Podman config
	if inst.Podman.ContainerName != "" {
		c.Podman.ContainerName = inst.Podman.ContainerName
	}
	if inst.Podman.DataDir != "" {
		c.Podman.DataDir = inst.Podman.DataDir
	}
	if inst.Podman.WALDir != "" {
		c.Podman.WALDir = inst.Podman.WALDir
	}
	if inst.Podman.ImageTag != "" {
		c.Podman.ImageTag = inst.Podman.ImageTag
	}
	// HostPort maps to Postgres.Port for external connections (GetPostgresURL)
	if inst.Podman.HostPort != 0 {
		c.Podman.HostPort = inst.Podman.HostPort
		c.Postgres.Port = inst.Podman.HostPort
	}

	// Merge PITR config
	c.PITR.Enabled = inst.PITR.Enabled
	if inst.PITR.PgBackRestStanza != "" {
		c.PITR.PgBackRestStanza = inst.PITR.PgBackRestStanza
	}

	return nil
}

// Load reads configuration from a file and merges it with defaults.
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // config file doesn't exist, return defaults
		}
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	cfg.applyDefaults()
	return cfg, nil
}

// displayConfig is the serializable subset of Config for save/display.
// Global postgres/podman/pitr are excluded — they are in-memory defaults only.
type displayConfig struct {
	BaseDir    string                     `yaml:"base_dir,omitempty"`
	Network    string                     `yaml:"network,omitempty"`
	Filesystem FilesystemConfig           `yaml:"filesystem"`
	Logging    LoggingConfig              `yaml:"logging"`
	Backup     BackupConfig               `yaml:"backup"`
	Instances  map[string]InstanceConfig  `yaml:"instances"`
}

// Display returns a view of the config suitable for display or saving.
func (c *Config) Display() displayConfig {
	return displayConfig{
		BaseDir:    c.BaseDir,
		Network:    c.Podman.Network,
		Filesystem: c.Filesystem,
		Logging:    c.Logging,
		Backup:     c.Backup,
		Instances:  c.Instances,
	}
}

// Save writes the configuration to a file.
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(c.Display())
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing config file %s: %w", path, err)
	}
	return nil
}

// Validate checks that the configuration is complete.
func (c *Config) Validate() error {
	if c.Filesystem.Name == "" {
		return fmt.Errorf("filesystem.name must not be empty")
	}
	if c.Podman.ContainerName == "" {
		return fmt.Errorf("podman.container_name must not be empty")
	}
	if c.PITR.Enabled && c.PITR.PgBackRestStanza == "" {
		return fmt.Errorf("pitr.pgbackrest_stanza must not be empty (PITR is enabled)")
	}
	return nil
}

// GetPostgresURL returns the PostgreSQL connection string.
func (c *Config) GetPostgresURL() string {
	if c.Postgres.URL != "" {
		return c.Postgres.URL
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
		c.Postgres.User, c.Postgres.Password,
		c.Postgres.Host, c.Postgres.Port,
		c.Postgres.Database)
}

// GetMountPoint returns the effective mount point ("auto" resolves to platform default).
func (c *Config) GetMountPoint() string {
	if c.Filesystem.MountPoint == "auto" {
		return platform.DefaultMountPoint()
	}
	return c.Filesystem.MountPoint
}

// GetCacheDir returns the effective cache directory ("auto" resolves to platform default).
func (c *Config) GetCacheDir() string {
	if c.Filesystem.CacheDir == "auto" {
		return platform.DefaultCacheDir()
	}
	return c.Filesystem.CacheDir
}

// applyDefaults fills zero-value fields with their defaults.
func (c *Config) applyDefaults() {
	d := Default()

	// Postgres
	if c.Postgres.Host == "" {
		c.Postgres.Host = d.Postgres.Host
	}
	if c.Postgres.Port == 0 {
		c.Postgres.Port = d.Postgres.Port
	}
	if c.Postgres.User == "" {
		c.Postgres.User = d.Postgres.User
	}
	if c.Postgres.Password == "" {
		c.Postgres.Password = d.Postgres.Password
	}
	if c.Postgres.Database == "" {
		c.Postgres.Database = d.Postgres.Database
	}

	// Filesystem
	if c.Filesystem.Name == "" {
		c.Filesystem.Name = d.Filesystem.Name
	}
	if c.Filesystem.MountPoint == "" {
		c.Filesystem.MountPoint = d.Filesystem.MountPoint
	}
	if c.Filesystem.CacheDir == "" {
		c.Filesystem.CacheDir = d.Filesystem.CacheDir
	}
	if c.Filesystem.CacheSizeMB == 0 {
		c.Filesystem.CacheSizeMB = d.Filesystem.CacheSizeMB
	}

	// Podman
	if c.Podman.ContainerName == "" {
		c.Podman.ContainerName = d.Podman.ContainerName
	}
	if c.Podman.DataDir == "" {
		c.Podman.DataDir = d.Podman.DataDir
	}
	if c.Podman.WALDir == "" {
		c.Podman.WALDir = d.Podman.WALDir
	}
	if c.Podman.ImageTag == "" {
		c.Podman.ImageTag = d.Podman.ImageTag
	}
	if c.Podman.Network == "" {
		if c.Network != "" {
			c.Podman.Network = c.Network
		} else {
			c.Podman.Network = d.Podman.Network
		}
	}

	// PITR
	if c.PITR.PgBackRestStanza == "" {
		c.PITR.PgBackRestStanza = d.PITR.PgBackRestStanza
	}

	// Logging
	if c.Logging.Level == "" {
		c.Logging.Level = d.Logging.Level
	}

	// Backup
	if c.Backup.ContainerName == "" {
		c.Backup.ContainerName = d.Backup.ContainerName
	}
	if c.Backup.ImageTag == "" {
		c.Backup.ImageTag = d.Backup.ImageTag
	}
	if c.Backup.DataDir == "" {
		c.Backup.DataDir = d.Backup.DataDir
	}
	if c.Backup.LogDir == "" {
		c.Backup.LogDir = d.Backup.LogDir
	}
	if c.Backup.RetentionFull == 0 {
		c.Backup.RetentionFull = d.Backup.RetentionFull
	}

	// Instances: apply per-instance defaults
	if c.Instances == nil {
		c.Instances = make(map[string]InstanceConfig)
	}
	for name, inst := range c.Instances {
		def := c.InstanceDefaults(name)
		if inst.Postgres.Host == "" {
			inst.Postgres.Host = def.Postgres.Host
		}
		if inst.Postgres.Port == 0 {
			inst.Postgres.Port = def.Postgres.Port
		}
		if inst.Postgres.User == "" {
			inst.Postgres.User = def.Postgres.User
		}
		if inst.Postgres.Password == "" {
			inst.Postgres.Password = def.Postgres.Password
		}
		if inst.Postgres.Database == "" {
			inst.Postgres.Database = def.Postgres.Database
		}
		if inst.Podman.ContainerName == "" {
			inst.Podman.ContainerName = def.Podman.ContainerName
		}
		if inst.Podman.DataDir == "" {
			inst.Podman.DataDir = def.Podman.DataDir
		}
		if inst.Podman.WALDir == "" {
			inst.Podman.WALDir = def.Podman.WALDir
		}
		if inst.Podman.ImageTag == "" {
			inst.Podman.ImageTag = def.Podman.ImageTag
		}
		if inst.Podman.HostPort == 0 {
			inst.Podman.HostPort = def.Podman.HostPort
		}
		if inst.PITR.PgBackRestStanza == "" {
			inst.PITR.PgBackRestStanza = def.PITR.PgBackRestStanza
		}
		c.Instances[name] = inst
	}

	// Auto-assign host ports for instances that don't have one set.
	// Ports start at 5432 and increment. Explicitly-set ports are reserved.
	c.autoAssignHostPorts()
}

// autoAssignHostPorts assigns sequential host ports to instances that have HostPort=0.
// Instances are processed in alphabetical order by name. Explicitly-set ports are reserved
// and skipped. Ports start at 25432. The "default" instance always gets 25432 if not explicitly set.
func (c *Config) autoAssignHostPorts() {
	names := make([]string, 0, len(c.Instances))
	for name := range c.Instances {
		names = append(names, name)
	}
	sort.Strings(names)

	// Put "default" first so it always gets 5432
	sorted := make([]string, 0, len(names))
	for _, n := range names {
		if n == "default" {
			sorted = append([]string{n}, sorted...)
		} else {
			sorted = append(sorted, n)
		}
	}

	nextPort := 25432
	for _, name := range sorted {
		inst := c.Instances[name]
		if inst.Podman.HostPort == 0 {
			inst.Podman.HostPort = nextPort
			nextPort++
		} else {
			// User set a specific port — skip past it
			if inst.Podman.HostPort >= nextPort {
				nextPort = inst.Podman.HostPort + 1
			}
		}
		c.Instances[name] = inst
	}
}
