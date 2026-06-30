//go:build !windows

package cli

import (
	"path/filepath"
	"strings"
)

// parseAIFSArgs parses the argument list of a process and returns the
// -i / --instance value and the positional mountpoint argument that follows
// the "mount" subcommand.  Returns ("", "") if this does not look like an
// aifs mount invocation.
//
// args must already be split into individual tokens (no NUL bytes).
// args[0] is expected to be the executable path.
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

	// Find the first non-flag argument after "mount".
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
			// Boolean flags take no value.
			if s == "-d" || s == "--background" || s == "-l" || s == "--list" {
				continue
			}
			i++ // skip flag value
			continue
		}
		mountpoint = s
		return
	}
	return "", ""
}
