package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"pgctl/internal/dockerutil"
)

func newDecommissionCmd() *cobra.Command {
	var composeFile string
	var yes bool

	cmd := &cobra.Command{
		Use:   "decommission",
		Short: "Tear down the local cluster and remove its volumes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("this deletes all data in the cluster's volumes; re-run with --yes to confirm")
			}
			out, err := dockerutil.Down(cmd.Context(), composeFile)
			if err != nil {
				return fmt.Errorf("tearing down cluster: %w", err)
			}
			fmt.Println(out)
			fmt.Println("cluster decommissioned")
			return nil
		},
	}

	cmd.Flags().StringVar(&composeFile, "compose-file", "docker-compose.yml", "path to the docker-compose file to tear down")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm irreversible teardown, including volumes")
	return cmd
}
