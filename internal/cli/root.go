// Package cli wires pgctl's Cobra command tree. Each command is a thin
// adapter over internal/config, internal/dockerutil and internal/upgrade —
// the logic worth testing lives in those packages, not here.
package cli

import (
	"github.com/spf13/cobra"
)

var configPath string

// NewRootCmd builds the top-level `pgctl` command.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "pgctl",
		Short: "A Postgres lifecycle control plane for local Docker clusters",
		Long: `pgctl provisions, configures, scales and decommissions local Postgres
clusters via Docker Compose, and validates major-version upgrades by
replaying captured traffic against the candidate version before cutover.`,
	}

	root.PersistentFlags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to the cluster config file")

	root.AddCommand(
		newCreateCmd(),
		newConfigureCmd(),
		newScaleCmd(),
		newDecommissionCmd(),
		newUpgradeCmd(),
	)

	return root
}
