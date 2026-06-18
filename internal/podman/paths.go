package podman

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mars-base/aifs/internal/platform"
)

// hostMountPath returns the path that should be passed to podman -v.
// On Linux/macOS this is the cleaned absolute host path. On Windows it is
// translated to a WSL-internal ext4 path (not drvfs /mnt/c/...) so containers
// get full POSIX filesystem semantics (chmod, etc.).
func hostMountPath(hostPath string) string {
	if runtime.GOOS != "windows" {
		abs, _ := filepath.Abs(hostPath)
		if abs == "" {
			return hostPath
		}
		return abs
	}

	// Windows: use WSL-native ext4 path for POSIX filesystem semantics.
	// Falls back to wslpath → /mnt/c/... for paths outside the config dir.
	return wslNativePath(hostPath)
}

func wslPath(hostPath string) string {
	distro := os.Getenv("PODMAN_MACHINE_NAME")
	if distro == "" {
		distro = "podman-machine-default"
	}

	try := func(d string) string {
		cmd := exec.Command("wsl", "-d", d, "--exec", "wslpath", "-u", hostPath)
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}

	if p := try(distro); p != "" {
		return p
	}
	return try("")
}

func windowsToWSLPath(hostPath string) string {
	// Convert backslashes to forward slashes first.
	p := strings.ReplaceAll(hostPath, `\`, "/")
	if len(p) >= 2 && p[1] == ':' {
		drive := strings.ToLower(string(p[0]))
		p = fmt.Sprintf("/mnt/%s%s", drive, p[2:])
	}
	return p
}

// ─── WSL ext4 path mapping (Windows) ─────────────────────────────

// wslDistro returns the WSL distribution name for the podman machine.
func wslDistro() string {
	distro := os.Getenv("PODMAN_MACHINE_NAME")
	if distro == "" {
		distro = "podman-machine-default"
	}
	return distro
}

// cachedWSLHome caches the WSL user's home directory (e.g., /home/user).
var cachedWSLHome string

// wslHomeDir returns the home directory of the WSL user inside the podman
// machine distro. Result is cached after first successful query.
func wslHomeDir() string {
	if cachedWSLHome != "" {
		return cachedWSLHome
	}
	distro := wslDistro()
	cmd := exec.Command("wsl", "-d", distro, "--exec", "sh", "-c", "echo $HOME")
	out, err := cmd.Output()
	if err != nil {
		cachedWSLHome = "/home/user" // sensible default
		return cachedWSLHome
	}
	cachedWSLHome = strings.TrimSpace(string(out))
	if cachedWSLHome == "" {
		cachedWSLHome = "/home/user"
	}
	return cachedWSLHome
}

// wslNativePath converts a Windows host path to a WSL-internal ext4 path.
// Unlike wslpath (which returns drvfs /mnt/c/... paths), this maps paths
// under the user's .aifs config directory to the WSL user's home directory
// on the ext4 filesystem, giving full POSIX semantics.
//
// Paths outside the config directory fall back to wslpath → /mnt/c/...
func wslNativePath(windowsPath string) string {
	configDir := platform.DefaultConfigDir()

	abs, err := filepath.Abs(windowsPath)
	if err != nil {
		abs = windowsPath
	}

	// If path is under the .aifs config directory, map to WSL home.
	rel, err := filepath.Rel(configDir, abs)
	if err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
		return toWSLPath(wslHomeDir(), ".aifs", rel)
	}

	// Handle the config directory itself (e.g., C:\Users\foo\.aifs).
	if abs == configDir || rel == "." {
		return toWSLPath(wslHomeDir(), ".aifs")
	}

	// Fallback: use wslpath (returns /mnt/c/... drvfs path).
	if p := wslPath(abs); p != "" {
		return p
	}
	return windowsToWSLPath(abs)
}

// toWSLPath joins path elements with forward slashes (for WSL/podman usage).
func toWSLPath(elem ...string) string {
	p := filepath.Join(elem...)
	return strings.ReplaceAll(p, "\\", "/")
}

// wslMkdirAll creates a directory tree on the WSL ext4 filesystem.
func wslMkdirAll(wslPath string) error {
	distro := wslDistro()
	cmd := exec.Command("wsl", "-d", distro, "--exec", "mkdir", "-p", wslPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wsl mkdir %s: %w (output: %s)", wslPath, err, string(out))
	}
	return nil
}

// wslReadFile reads the contents of a file from the WSL ext4 filesystem.
func wslReadFile(wslPath string) ([]byte, error) {
	distro := wslDistro()
	cmd := exec.Command("wsl", "-d", distro, "--exec", "cat", wslPath)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("wsl read %s: %w", wslPath, err)
	}
	return out, nil
}
// given permission bits. The parent directory is created if it doesn't exist.
func wslWriteFile(wslPath string, data []byte, perm os.FileMode) error {
	distro := wslDistro()
	dir := filepath.Dir(wslPath)
	cmd := exec.Command("wsl", "-d", distro, "--exec", "sh", "-c",
		fmt.Sprintf("mkdir -p '%s' && cat > '%s' && chmod %o '%s'", dir, wslPath, perm, wslPath))
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wsl write %s: %w (output: %s)", wslPath, err, string(out))
	}
	return nil
}
