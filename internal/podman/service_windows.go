//go:build windows

package podman

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const containerHostEnv = "tcp://localhost:2375"

var (
	serviceOnce sync.Once
	serviceErr  error
)

// EnsurePodmanService ensures the WSL podman API service is listening on
// localhost:2375 so that the Windows podman client can operate without a
// podman machine. It sets CONTAINER_HOST in the current process for all
// subsequent podman invocations.
//
// On the first call, it launches the podman API service as a fully detached
// child process (via DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP).  The
// orphan wsl.exe keeps the WSL VM alive even after aifs exits.  A netsh
// portproxy rule forwards localhost:2375 into WSL so that the Windows podman
// CLI can reach the service regardless of whether WSL localhost forwarding
// is broken.
func EnsurePodmanService() error {
	serviceOnce.Do(startPodmanService)
	if serviceErr != nil {
		return serviceErr
	}
	_ = os.Setenv("CONTAINER_HOST", containerHostEnv)
	return nil
}

func startPodmanService() {
	distro := os.Getenv("PODMAN_MACHINE_NAME")
	if distro == "" {
		distro = "podman-machine-default"
	}

	// If the TCP API service is already listening inside WSL, just ensure
	// the portproxy is up-to-date and return.
	if tcpServiceListening(distro) {
		ensurePortproxy(distro)
		return
	}

	fmt.Println("→ Starting WSL podman service...")

	// Clean stale boot-ID cache from a previous WSL session.
	exec.Command("wsl", "-d", distro, "--exec", "sh", "-c",
		"rm -rf /tmp/storage-run-1000/containers /tmp/storage-run-1000/libpod/tmp 2>/dev/null").Run()

	// Launch podman system service via cmd /c start "" /B.
	//
	// Go's exec.Cmd.Start puts the child into Go's own Windows Job Object,
	// which is torn down when aifs exits — killing wsl.exe and shutting down
	// the WSL VM.  cmd /c start uses ShellExecuteEx internally, which creates
	// the process outside of Go's job, so wsl.exe survives aifs exit.
	//
	// The /B flag prevents `start` from opening a new console window for the
	// long-running podman service. Without it, a black cmd window stays open
	// on the desktop showing the service's WARN/log lines, and closing that
	// window kills wsl.exe and stops the service. /B keeps the process
	// windowless while still detached from Go's job. HideWindow also hides
	// the transient cmd.exe itself.
	launch := func(distroArg string) error {
		args := []string{"/c", "start", "", "/B", "wsl"}
		if distroArg != "" {
			args = append(args, "-d", distroArg)
		}
		args = append(args, "--exec", "podman", "system", "service", "-t", "0", "tcp://0.0.0.0:2375")
		cmd := exec.Command("cmd", args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		return cmd.Run()
	}

	if err := launch(distro); err != nil {
		if err2 := launch(""); err2 != nil {
			serviceErr = fmt.Errorf("starting WSL podman service: %w (fallback: %w)", err, err2)
			return
		}
	}

	// Wait for the TCP service to become reachable inside WSL.
	for i := 0; i < 30; i++ {
		if tcpServiceListening(distro) {
			ensurePortproxy(distro)
			fmt.Println("  ✓ podman service ready")
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	serviceErr = fmt.Errorf("podman service on %s did not become reachable after 15 s", containerHostEnv)
}

// tcpServiceListening checks whether the podman API is listening on TCP
// port 2375 inside the WSL VM.  We check inside WSL (not from Windows)
// because WSL localhost forwarding may be broken and the portproxy may
// be stale.
func tcpServiceListening(distro string) bool {
	cmd := exec.Command("wsl", "-d", distro, "--exec", "sh", "-c",
		"ss -tlnp 2>/dev/null | grep -q 2375")
	return cmd.Run() == nil
}

// ensurePortproxy adds or updates a netsh portproxy rule that forwards
// localhost:2375 into the WSL VM.
func ensurePortproxy(distro string) {
	wslIP := getWSLIP(distro)
	if wslIP == "" {
		return
	}

	// Skip if the rule already points to the correct IP.
	if out, _ := exec.Command("netsh", "interface", "portproxy", "show", "v4tov4").Output(); strings.Contains(string(out), wslIP+":2375") {
		return
	}

	// Delete any stale rule, then add the current one.
	exec.Command("netsh", "interface", "portproxy", "delete", "v4tov4",
		"listenaddress=0.0.0.0", "listenport=2375").Run()

	addCmd := exec.Command("netsh", "interface", "portproxy", "add", "v4tov4",
		"listenaddress=0.0.0.0", "listenport=2375",
		"connectaddress="+wslIP, "connectport=2375")
	if out, err := addCmd.CombinedOutput(); err != nil {
		fmt.Printf("  ⚠ portproxy add failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
		return
	}
	fmt.Printf("  ✓ portproxy: 0.0.0.0:2375 → %s:2375\n", wslIP)
}

// getWSLIP returns the IPv4 address of the WSL distro's eth0 interface.
func getWSLIP(distro string) string {
	cmd := exec.Command("wsl", "-d", distro, "--exec", "sh", "-c",
		"ip -4 -br addr show eth0 2>/dev/null | grep -oE '[0-9]+\\.[0-9]+\\.[0-9]+\\.[0-9]+' | head -1")
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		cmd2 := exec.Command("wsl", "--exec", "sh", "-c",
			"ip -4 -br addr show eth0 2>/dev/null | grep -oE '[0-9]+\\.[0-9]+\\.[0-9]+\\.[0-9]+' | head -1")
		out2, err2 := cmd2.Output()
		if err2 != nil || len(out2) == 0 {
			return ""
		}
		out = out2
	}
	return strings.TrimSpace(string(out))
}

// Allows tests to reset the once guard.
func resetServiceGuard() {
	serviceOnce = sync.Once{}
	serviceErr = nil
}
