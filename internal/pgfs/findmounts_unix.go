//go:build !windows

package pgfs

import (
	"path/filepath"
	"strings"
)

// parseAIFSArgs inspects an argv slice and returns the -i/--instance value
// and the positional mountpoint argument that follows the "mount" subcommand.
// Returns ("", "") if the argv does not look like an aifs mount invocation.
func parseAIFSArgs(args []string) (instance, mountpoint string) {
	if len(args) == 0 {
		return
	}
	if !strings.HasSuffix(filepath.Base(args[0]), "aifs") {
		return
	}

	var hasMountCmd bool
	for i := 1; i < len(args); i++ {
		s := args[i]
		switch {
		case s == "mount":
			hasMountCmd = true
		case (s == "-i" || s == "--instance") && i+1 < len(args):
			instance = args[i+1]
			i++
		case strings.HasPrefix(s, "-i") && len(s) > 2:
			instance = s[2:]
		case strings.HasPrefix(s, "--instance="):
			instance = strings.TrimPrefix(s, "--instance=")
		}
	}
	if !hasMountCmd {
		return "", ""
	}

	// First non-flag argument after "mount" is the mountpoint.
	foundMount := false
	for i := 1; i < len(args); i++ {
		s := args[i]
		if !foundMount {
			if s == "mount" {
				foundMount = true
			}
			continue
		}
		if strings.HasPrefix(s, "-") {
			if s == "-d" || s == "--background" || s == "-l" || s == "--list" {
				continue // boolean flags, no value
			}
			i++ // skip flag value
			continue
		}
		mountpoint = s
		return
	}
	return "", ""
}
