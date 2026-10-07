package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"pgctl/internal/config"
)

func newConfigureCmd() *cobra.Command {
	var sets []string

	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Update fields on the cluster config (key=value pairs)",
		Example: `  pgctl configure --set image=postgres:16 --set replicas=2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cluster, err := config.Load(configPath)
			if err != nil {
				return err
			}

			for _, kv := range sets {
				parts := strings.SplitN(kv, "=", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid --set %q, expected key=value", kv)
				}
				key, value := parts[0], parts[1]

				switch key {
				case "image":
					cluster.Image = value
				case "port":
					p, err := strconv.Atoi(value)
					if err != nil {
						return fmt.Errorf("port must be an integer: %w", err)
					}
					cluster.Port = p
				case "replicas":
					r, err := strconv.Atoi(value)
					if err != nil {
						return fmt.Errorf("replicas must be an integer: %w", err)
					}
					cluster.Replicas = r
				case "storage":
					cluster.Storage = value
				default:
					return fmt.Errorf("unknown config key %q (expected image, port, replicas, storage)", key)
				}
			}

			if err := cluster.Save(configPath); err != nil {
				return err
			}
			fmt.Printf("updated %s\n", configPath)
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&sets, "set", nil, "a key=value pair to update, may be repeated")
	return cmd
}
