package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"pgctl/internal/config"
	"pgctl/internal/dockerutil"
)

func newScaleCmd() *cobra.Command {
	var replicas int
	var composeFile string

	cmd := &cobra.Command{
		Use:   "scale",
		Short: "Scale the number of read replicas for the cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			cluster, err := config.Load(configPath)
			if err != nil {
				return err
			}

			out, err := dockerutil.Scale(cmd.Context(), composeFile, "pg-replica", replicas)
			if err != nil {
				return fmt.Errorf("scaling %q to %d replicas: %w", cluster.Name, replicas, err)
			}
			fmt.Println(out)

			cluster.Replicas = replicas
			if err := cluster.Save(configPath); err != nil {
				return err
			}
			fmt.Printf("cluster %q now has %d replica(s)\n", cluster.Name, replicas)
			return nil
		},
	}

	cmd.Flags().IntVar(&replicas, "replicas", 1, "desired number of read replicas")
	cmd.Flags().StringVar(&composeFile, "compose-file", "docker-compose.yml", "path to the docker-compose file to apply")
	return cmd
}
