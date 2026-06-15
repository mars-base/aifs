// // Package platform provides cross-platform adaptation: OS detection, dependency checks, default paths.
package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// OS represents the operating system type.
type OS int

const (
	Linux   OS = iota // Linux (native Podman)
	MacOS             // macOS (requires podman machine)
	Windows           // Windows (requires podman machine + WSL2)
)

// String returns the human-readable name of the OS.
func (o OS) String() string {
	switch o {
	case Linux:
		return "linux"
	case MacOS:
		return "macOS"
	case Windows:
		return "windows"
	default:
		return "unknown"
	}
}

// Detect returns the current operating system.
func Detect() OS {
	switch runtime.GOOS {
	case "linux":
		return Linux
	case "darwin":
		return MacOS
	case "windows":
		return Windows
	default:
		return Linux // fallback
	}
}

// NeedsPodmanMachine returns whether podman machine is needed (macOS/Windows).
func NeedsPodmanMachine() bool {
	return Detect() != Linux
}

// --- Dependency checks ---

// DepStatus describes the status of a dependency.
type DepStatus struct {
	Name    string // Dependency name (e.g. "podman")
	Found   bool   // Whether it is installed
	Path    string // Binary path
	Version string // Version string
	Hint    string // Installation hint
}

// CheckPodman checks if podman is available, returns its path and version.
func CheckPodman() DepStatus {
	path, err := exec.LookPath("podman")
	if err != nil {
		return DepStatus{
			Name:  "podman",
			Found: false,
			Hint:  podmanInstallHint(),
		}
	}
	ver, _ := runCmd(path, "--version")
	return DepStatus{
		Name:    "podman",
		Found:   true,
		Path:    path,
		Version: ver,
	}
}

// CheckJuiceFS checks if juicefs is available.
func CheckJuiceFS() DepStatus {
	path, err := exec.LookPath("juicefs")
	if err != nil {
		return DepStatus{
			Name:  "juicefs",
			Found: false,
			Hint:  "juicefs is not installed. Download prebuilt binary from https://github.com/juicedata/juicefs/releases, or run aifs setup to auto-download.",
		}
	}
	ver, _ := runCmd(path, "version")
	return DepStatus{
		Name:    "juicefs",
		Found:   true,
		Path:    path,
		Version: ver,
	}
}

// CheckPodmanMachine checks podman machine status (macOS/Windows only).
func CheckPodmanMachine() DepStatus {
	path, err := exec.LookPath("podman")
	if err != nil {
		return DepStatus{
			Name:  "podman-machine",
			Found: false,
			Hint:  "podman is not installed",
		}
	}
	out, err := runCmd(path, "machine", "list")
	if err != nil {
		return DepStatus{
			Name:  "podman-machine",
			Found: false,
			Hint:  fmt.Sprintf("podman machine unavailable: %v", err),
		}
	}
	return DepStatus{
		Name:    "podman-machine",
		Found:   true,
		Path:    path,
		Version: out,
	}
}

// MissingPrereqs returns the list of missing dependencies.
func MissingPrereqs() []DepStatus {
	var missing []DepStatus
	for _, d := range []DepStatus{
		CheckPodman(),
	} {
		if !d.Found {
			missing = append(missing, d)
		}
	}
	// JuiceFS is not a hard dependency (aifs setup can auto-download)
	return missing
}

// --- Default paths ---

// DefaultMountPoint returns the default JuiceFS mount point.
func DefaultMountPoint() string {
	switch Detect() {
	case Linux:
		return "/mnt/aifs"
	case MacOS:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "aifs")
	case Windows:
		return "Z:\\aifs"
	default:
		return "/mnt/aifs"
	}
}

// DefaultCacheDir returns the default JuiceFS cache directory.
func DefaultCacheDir() string {
	switch Detect() {
	case Linux:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".juicefs", "cache")
	case MacOS:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Caches", "aifs")
	case Windows:
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "aifs", "cache")
	default:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".juicefs", "cache")
	}
}

// DefaultConfigDir returns the aifs configuration directory.
func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aifs")
}

// DefaultConfigPath returns the aifs configuration file path.
func DefaultConfigPath() string {
	return filepath.Join(DefaultConfigDir(), "config.yaml")
}

// --- Internal helpers ---

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func podmanInstallHint() string {
	switch Detect() {
	case Linux:
		return "Install podman: curl -fsSL -o ~/.local/bin/podman https://github.com/89luca89/podman-launcher/releases/latest/download/podman-launcher-amd64 && chmod +x ~/.local/bin/podman"
	case MacOS:
		return "Install podman: brew install podman"
	case Windows:
		return "Install podman: winget install RedHat.Podman"
	default:
		return "Please install podman"
	}
}
