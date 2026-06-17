//go:build windows

package cli

// activeAIFSMounts returns an empty list on Windows until WinFsp integration
// records active mounts in a state file.
func activeAIFSMounts() ([]string, error) {
	return nil, nil
}
