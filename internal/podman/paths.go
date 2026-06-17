package podman

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// hostMountPath returns the path that should be passed to podman -v.
// On Linux/macOS this is the cleaned absolute host path. On Windows it is
// translated to the WSL path understood by the WSL-based podman service.
func hostMountPath(hostPath string) string {
	if runtime.GOOS != "windows" {
		abs, _ := filepath.Abs(hostPath)
		if abs == "" {
			return hostPath
		}
		return abs
	}

	abs, err := filepath.Abs(hostPath)
	if err != nil {
		abs = hostPath
	}

	// Try to use wslpath inside the podman machine distro.
	converted := wslPath(abs)
	if converted != "" {
		return converted
	}

	// Fallback: map C:\foo\bar to /mnt/c/foo/bar.
	return windowsToWSLPath(abs)
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
