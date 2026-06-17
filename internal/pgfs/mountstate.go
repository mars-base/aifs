package pgfs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mars-base/aifs/internal/platform"
)

// SentinelName is the synthetic file that aifs mounts expose at their root.
// It can be used to detect whether a mount is currently alive.
const SentinelName = ".aifs-mounted"

// MountRecord describes a single active aifs mount.
type MountRecord struct {
	MountPoint string    `json:"mount_point"`
	Instance   string    `json:"instance"`
	PID        int       `json:"pid"`
	StartedAt  time.Time `json:"started_at"`
}

var (
	stateMu sync.Mutex
)

func mountStateFile() string {
	return filepath.Join(platform.DefaultConfigDir(), "mounts.json")
}

func ensureConfigDir() error {
	dir := platform.DefaultConfigDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	return nil
}

func loadMountState() (map[string]MountRecord, error) {
	records := make(map[string]MountRecord)
	path := mountStateFile()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return records, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return records, nil
	}
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("parsing mount state: %w", err)
	}
	return records, nil
}

func saveMountState(records map[string]MountRecord) error {
	if err := ensureConfigDir(); err != nil {
		return err
	}
	path := mountStateFile()
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// AddMountState records a new active mount.
func AddMountState(rec MountRecord) error {
	stateMu.Lock()
	defer stateMu.Unlock()

	records, err := loadMountState()
	if err != nil {
		return err
	}
	records[rec.MountPoint] = rec
	return saveMountState(records)
}

// RemoveMountState removes a mount record.
func RemoveMountState(mountPoint string) error {
	stateMu.Lock()
	defer stateMu.Unlock()

	records, err := loadMountState()
	if err != nil {
		return err
	}
	delete(records, mountPoint)
	return saveMountState(records)
}

// GetMountState returns the recorded mount for mountPoint and whether it exists.
func GetMountState(mountPoint string) (MountRecord, bool, error) {
	stateMu.Lock()
	defer stateMu.Unlock()

	records, err := loadMountState()
	if err != nil {
		return MountRecord{}, false, err
	}
	rec, ok := records[mountPoint]
	return rec, ok, nil
}

// ListMountState returns all recorded mount records.
func ListMountState() ([]MountRecord, error) {
	stateMu.Lock()
	defer stateMu.Unlock()

	records, err := loadMountState()
	if err != nil {
		return nil, err
	}
	out := make([]MountRecord, 0, len(records))
	for _, rec := range records {
		out = append(out, rec)
	}
	return out, nil
}
