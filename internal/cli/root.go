// // Package cli provides the aifs command-line interface (Cobra).
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mars-base/aifs/internal/config"
	"github.com/mars-base/aifs/internal/platform"
	"github.com/mars-base/aifs/internal/podman"
)

var (
	cfgPath     string
	cfgInstance string
	cfgOutput   string
	cfg         *config.Config
)

// rootCmd is the root command for aifs.
var rootCmd = &cobra.Command{
	Use:   "aifs",
	Short: "AI-native filesystem with PITR powered by PostgreSQL",
	Long: `aifs gives you a "time machine" filesystem.
Leverages PITR (Point-In-Time Recovery) to rollback the filesystem to any historical moment.`,
	SilenceUsage: true,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgPath, "config", "c", "", "config file path (default ~/.aifs/config.yaml)")
	rootCmd.PersistentFlags().StringVarP(&cfgInstance, "instance", "i", "default", "instance name")
	rootCmd.PersistentFlags().StringVarP(&cfgOutput, "output", "o", "", "output file path (default ~/.aifs/config.yaml)")
}

// loadConfig loads configuration before command execution.
func loadConfig() error {
	path := cfgPath
	if path == "" {
		path = platform.DefaultConfigPath()
	}
	cfgPath = path
	c, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if err := c.SetInstance(cfgInstance); err != nil {
		return err
	}
	cfg = c
	return nil
}

// newPodman creates a Podman manager.
func newPodman() (*podman.Manager, error) {
	return podman.New(cfg)
}

// newBackupManager creates a BackupManager for shared backup container operations.
func newBackupManager() (*podman.BackupManager, error) {
	return podman.NewBackupManager(cfg)
}
