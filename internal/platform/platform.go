// // Package platform provides cross-platform adaptation: OS detection, dependency checks, default paths.
package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
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

// NeedsPodmanMachine returns whether podman machine is needed (macOS only).
// Windows uses a WSL podman service instead; Linux uses native Podman.
func NeedsPodmanMachine() bool {
	return Detect() == MacOS
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
	return missing
}

// --- Default paths ---

// DefaultConfigDir returns the aifs configuration directory.
func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aifs")
}

// DefaultConfigPath returns the aifs configuration file path.
func DefaultConfigPath() string {
	return filepath.Join(DefaultConfigDir(), "config.yaml")
}

// GetUsedPorts returns the set of TCP ports currently listening on the local host.
// On Windows this queries the WSL podman machine (host networking — all
// containers share the WSL network stack so we must probe). On other platforms
// this returns nil since bridge networking gives each container its own IP.
func GetUsedPorts() map[int]bool {
	if Detect() != Windows {
		return nil
	}
	distro := os.Getenv("PODMAN_MACHINE_NAME")
	if distro == "" {
		distro = "podman-machine-default"
	}
	cmd := exec.Command("wsl", "-d", distro, "--exec", "ss", "-tlnH")
	out, err := cmd.Output()
	if err != nil {
		return nil // can't probe, fall back to sequential assignment
	}
	// ss -tlnH output lines:  LISTEN  0  4096  127.0.0.1:5432  0.0.0.0:*
	re := regexp.MustCompile(`:(\d+)\s`)
	used := make(map[int]bool)
	for _, match := range re.FindAllStringSubmatch(string(out), -1) {
		if port, err := strconv.Atoi(match[1]); err == nil {
			used[port] = true
		}
	}
	return used
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
