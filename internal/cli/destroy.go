package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mars-base/aifs/internal/config"
	"github.com/mars-base/aifs/internal/platform"
	"github.com/mars-base/aifs/internal/podman"
)

var destroyForce bool

func init() {
	rootCmd.AddCommand(destroyCmd)
	destroyCmd.Flags().BoolVar(&destroyForce, "force", false, "Skip confirmation prompt")
}

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Destroy an instance and remove its configuration",
	Long: `destroy stops and removes the container, then removes the
instance's configuration entry. Host data directories are preserved.

Use --force to skip the confirmation prompt.

Examples:
  aifs destroy -i proj01
  aifs destroy -i proj01 --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := cfgPath
		if path == "" {
			path = platform.DefaultConfigPath()
		}

		if _, err := os.Stat(path); os.IsNotExist(err) {
			return fmt.Errorf("config file not found: %s", path)
		}

		cfg, err := config.Load(path)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		inst, ok := cfg.Instances[cfgInstance]
		if !ok {
			return fmt.Errorf("instance %q not found in config", cfgInstance)
		}

		// Merge instance config for container operations
		if err := cfg.SetInstance(cfgInstance); err != nil {
			return fmt.Errorf("loading instance config: %w", err)
		}

		pm, err := podman.New(cfg)
		if err != nil {
			return fmt.Errorf("podman: %w", err)
		}

		if !destroyForce {
			fmt.Printf("⚠️  This will destroy instance %q:\n", cfgInstance)
			fmt.Printf("  - Stop and remove container: %s\n", inst.Podman.ContainerName)
			fmt.Printf("  - Remove config entry\n")
			fmt.Printf("\n  Data directories preserved on host:\n")
			fmt.Printf("    data: %s\n", inst.Podman.DataDir)
			fmt.Printf("    wal:  %s\n", inst.Podman.WALDir)
			fmt.Printf("\nConfirm? [y/N]: ")

			var answer string
			fmt.Scanln(&answer)
			if answer != "y" && answer != "Y" && answer != "yes" {
				fmt.Println("Cancelled")
				return nil
			}
		}

		// 1. Destroy container
		fmt.Printf("→ Stopping and removing container %s...\n", inst.Podman.ContainerName)
		if err := pm.Destroy(); err != nil {
			fmt.Printf("  ⚠️  Warning: failed to destroy container: %v\n", err)
		}

		// 2. Remove config entry
		delete(cfg.Instances, cfgInstance)
		if err := cfg.Save(path); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("✓ instance %q destroyed\n", cfgInstance)
		return nil
	},
}
