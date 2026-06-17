//go:build windows

package podman

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

const containerHostEnv = "tcp://localhost:2375"

// EnsurePodmanService ensures the WSL podman API service is listening on
// localhost:2375 so that the Windows podman client can operate without a
// podman machine. It sets CONTAINER_HOST in the current process for all
// subsequent podman invocations.
func EnsurePodmanService() error {
	if podmanServiceReachable() {
		_ = os.Setenv("CONTAINER_HOST", containerHostEnv)
		return nil
	}

	fmt.Println("→ Starting WSL podman service...")
	distro := os.Getenv("PODMAN_MACHINE_NAME")
	if distro == "" {
		distro = "podman-machine-default"
	}

	// Start the podman API service inside the WSL distro. It binds to 0.0.0.0
	// so the Windows host can reach it via localhost forwarded by WSL.
	script := "nohup podman system service -t 0 tcp://0.0.0.0:2375 >/tmp/podman-service.log 2>&1 &"
	cmd := exec.Command("wsl", "-d", distro, "--exec", "sh", "-c", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Fall back to the default WSL distro if the named distro is missing.
		cmd = exec.Command("wsl", "--exec", "sh", "-c", script)
		if out2, err2 := cmd.CombinedOutput(); err2 != nil {
			return fmt.Errorf("starting WSL podman service: %w (fallback: %w)\n%s\n%s", err, err2, strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
		}
	}

	for i := 0; i < 30; i++ {
		if podmanServiceReachable() {
			_ = os.Setenv("CONTAINER_HOST", containerHostEnv)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("podman service on %s did not become reachable", containerHostEnv)
}

func podmanServiceReachable() bool {
	conn, err := net.DialTimeout("tcp", "localhost:2375", 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
