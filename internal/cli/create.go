package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"pgctl/internal/config"
	"pgctl/internal/dockerutil"
)

var createComposeFile string

func newCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Provision a new local Postgres cluster from the config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			cluster, err := config.Load(configPath)
			if err != nil {
				return err
			}

			out, err := dockerutil.Up(cmd.Context(), createComposeFile, "pg-old")
			if err != nil {
				return fmt.Errorf("bringing up cluster %q: %w", cluster.Name, err)
			}
			fmt.Println(out)
			fmt.Printf("cluster %q is up on port %d (image %s)\n", cluster.Name, cluster.Port, cluster.Image)
			return nil
		},
	}
	cmd.Flags().StringVar(&createComposeFile, "compose-file", "docker-compose.yml", "path to the docker-compose file to apply")
	return cmd
}
