// Package platform provides cross-platform adaptation: OS detection, dependency checks, default paths.
package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
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

// CheckFUSE checks whether the FUSE runtime dependency is available.
//
//	macOS:   checks for the macFUSE kernel extension or filesystem bundle
//	Linux:   checks for fusermount3 or fusermount in PATH
//	Windows: checks for WinFsp DLL
func CheckFUSE() DepStatus {
	switch Detect() {
	case MacOS:
		return checkMacFUSE()
	case Linux:
		return checkFusermount()
	case Windows:
		return checkWinFsp()
	default:
		return DepStatus{Name: "fuse", Found: true} // not applicable
	}
}

func checkMacFUSE() DepStatus {
	// Prefer checking the kext -- if loaded, everything works.
	if out, err := runCmd("kextstat"); err == nil {
		if strings.Contains(out, "macfuse") {
			return DepStatus{Name: "macfuse", Found: true}
		}
	}
	// Fallback: check if the filesystem bundle is installed (but maybe not loaded).
	if st, err := os.Stat("/Library/Filesystems/macfuse.fs"); err == nil && st.IsDir() {
		return DepStatus{
			Name:  "macfuse",
			Found: false,
			Hint:  "macFUSE is installed but the kernel extension is not loaded. Run: sudo kextutil /Library/Filesystems/macfuse.fs (may require reboot after approving in System Settings -> Privacy & Security)",
		}
	}
	return DepStatus{
		Name:  "macfuse",
		Found: false,
		Hint:  "Install macFUSE: brew install --cask macfuse, then approve in System Settings -> Privacy & Security and reboot",
	}
}

func checkWinFsp() DepStatus {
	// WinFsp installs its DLLs in Program Files.
	winFspPaths := []string{
		`C:\Program Files (x86)\WinFsp\bin\winfsp-x64.dll`,
		`C:\Program Files\WinFsp\bin\winfsp-x64.dll`,
	}
	for _, p := range winFspPaths {
		if _, err := os.Stat(p); err == nil {
			return DepStatus{Name: "winfsp", Found: true}
		}
	}
	return DepStatus{
		Name:  "winfsp",
		Found: false,
		Hint:  "Install WinFsp: winget install WinFsp.WinFsp (or download from https://winfsp.dev/)",
	}
}

func checkFusermount() DepStatus {
	path, err := exec.LookPath("fusermount3")
	if err != nil {
		path, err = exec.LookPath("fusermount")
		if err != nil {
			return DepStatus{
				Name:  "fusermount3",
				Found: false,
				Hint:  fuseInstallHint(),
			}
		}
	}
	return DepStatus{
		Name:  "fusermount3",
		Found: true,
		Path:  path,
	}
}

func fuseInstallHint() string {
	return "Install fuse3: apt-get install fuse3 (Debian/Ubuntu), dnf install fuse3-libs (Fedora), pacman -S fuse3 (Arch), or equivalent for your distribution"
}

// MissingPrereqs returns the list of missing dependencies.
func MissingPrereqs() []DepStatus {
	var missing []DepStatus
	for _, d := range []DepStatus{
		CheckPodman(),
		CheckFUSE(),
	} {
		if !d.Found {
			missing = append(missing, d)
		}
	}
	if NeedsPodmanMachine() {
		if d := CheckPodmanMachine(); !d.Found {
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

// GetUsedPorts returns the set of TCP ports currently listening on the container
// host. On macOS we use bridge networking -- containers have their own IPs on the
// bridge, but published ports are still visible inside the VM via podman proxy.
// We probe to avoid port collisions.
//
//	Linux:   ss -tlnH directly on the host
//	macOS:   podman machine ssh <name> ss -tlnH (probes inside the VM)
//	Windows: wsl -d <distro> --exec ss -tlnH (containers share the WSL network)
func GetUsedPorts() map[int]bool {
	var cmd *exec.Cmd
	switch Detect() {
	case Linux:
		cmd = exec.Command("ss", "-tlnH")
	case MacOS:
		name := os.Getenv("PODMAN_MACHINE_NAME")
		if name == "" {
			name = "podman-machine-default"
		}
		cmd = exec.Command("podman", "machine", "ssh", name, "ss", "-tlnH")
	case Windows:
		distro := os.Getenv("PODMAN_MACHINE_NAME")
		if distro == "" {
			distro = "podman-machine-default"
		}
		cmd = exec.Command("wsl", "-d", distro, "--exec", "ss", "-tlnH")
	default:
		return nil
	}
	hideConsoleWindow(cmd)
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

// hideConsoleWindow prevents cmd from popping up a console window on Windows.
// On a detached process (e.g. background aifs mount) every child wsl/podman/netsh
// would otherwise flash its own console window. No-op on Linux/macOS.
func hideConsoleWindow(cmd *exec.Cmd) {
	if runtime.GOOS != "windows" {
		return
	}
	hideConsoleWindowWindows(cmd)
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	hideConsoleWindow(cmd)
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
