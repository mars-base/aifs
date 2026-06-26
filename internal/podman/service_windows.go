//go:build windows

package podman

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
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

	// Clean stale boot-ID cache from a previous WSL session.
	cleanCmd := exec.Command("wsl", "-d", distro, "--exec", "sh", "-c",
		"rm -rf /tmp/storage-run-1000/containers /tmp/storage-run-1000/libpod/tmp 2>/dev/null")
	hideWindow(cleanCmd)
	cleanCmd.Run()

	// Launch podman system service via cmd /c start "" /B.
	//
	// Go's exec.Cmd.Start puts the child into Go's own Windows Job Object,
	// which is torn down when aifs exits -- killing wsl.exe and shutting down
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
		hideWindow(cmd)
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
	hideWindow(cmd)
	return cmd.Run() == nil
}

// ensurePortproxy adds or updates a netsh portproxy rule that forwards
// localhost:2375 into the WSL VM.
func ensurePortproxy(distro string) {
	ensurePortproxyPort(distro, 2375)
}

// ensurePortproxyPort adds or updates a netsh portproxy rule for an arbitrary
// port (used to forward PG host ports into the WSL VM).
func ensurePortproxyPort(distro string, port int) {
	wslIP := getWSLIP(distro)
	if wslIP == "" {
		return
	}
	portStr := fmt.Sprintf("%d", port)

	// Skip if the rule already points to the correct IP.
	showCmd := exec.Command("netsh", "interface", "portproxy", "show", "v4tov4")
	hideWindow(showCmd)
	if out, _ := showCmd.Output(); strings.Contains(string(out), wslIP+":"+portStr) {
		return
	}

	// Delete any stale rule, then add the current one.
	delCmd := exec.Command("netsh", "interface", "portproxy", "delete", "v4tov4",
		"listenaddress=0.0.0.0", "listenport="+portStr)
	hideWindow(delCmd)
	delCmd.Run()

	addCmd := exec.Command("netsh", "interface", "portproxy", "add", "v4tov4",
		"listenaddress=0.0.0.0", "listenport="+portStr,
		"connectaddress="+wslIP, "connectport="+portStr)
	hideWindow(addCmd)
	if out, err := addCmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "  [!] portproxy add failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
	}
}

// getWSLIP returns the IPv4 address of the WSL distro's eth0 interface.
func getWSLIP(distro string) string {
	cmd := exec.Command("wsl", "-d", distro, "--exec", "sh", "-c",
		"ip -4 -br addr show eth0 2>/dev/null | grep -oE '[0-9]+\\.[0-9]+\\.[0-9]+\\.[0-9]+' | head -1")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		cmd2 := exec.Command("wsl", "--exec", "sh", "-c",
			"ip -4 -br addr show eth0 2>/dev/null | grep -oE '[0-9]+\\.[0-9]+\\.[0-9]+\\.[0-9]+' | head -1")
		hideWindow(cmd2)
		out2, err2 := cmd2.Output()
		if err2 != nil || len(out2) == 0 {
			return ""
		}
		out = out2
	}
	return strings.TrimSpace(string(out))
}

// EnsurePGPortProxy adds a netsh portproxy rule forwarding the configured
// PostgreSQL host port from Windows localhost into the WSL VM.  This is
// required when the container runs with host networking (the default on
// Windows) because WSL host-networking binds to the WSL VM's localhost, not
// the Windows host's localhost.
func (m *Manager) EnsurePGPortProxy() {
	distro := os.Getenv("PODMAN_MACHINE_NAME")
	if distro == "" {
		distro = "podman-machine-default"
	}
	ensurePortproxyPort(distro, m.cfg.Podman.HostPort)
}

// Allows tests to reset the once guard.
func resetServiceGuard() {
	serviceOnce = sync.Once{}
	serviceErr = nil
}
