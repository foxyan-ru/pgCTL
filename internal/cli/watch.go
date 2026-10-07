package cli

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"pgctl/internal/db"
	"pgctl/internal/tui"
	"pgctl/internal/upgrade"
)

// newUpgradeWatchCmd builds `pgctl upgrade watch`: a live terminal UI that
// replays the captured query set against both instances one query at a time,
// filling in a comparison table row-by-row as results come in. It's the
// interactive counterpart to the scriptable `replay` + `compare` two-step
// flow — meant to be watched (and screen-recorded), not piped.
func newUpgradeWatchCmd() *cobra.Command {
	var sourceDSN, targetDSN, queriesPath string
	var threshold float64

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Replay captured queries against both instances live, in a terminal UI",
		Long: `watch is the interactive counterpart to "replay" + "compare": instead of
writing two report files and diffing them afterward, it connects to both the
source and target instances up front and replays each captured query against
both, one at a time, filling in a comparison table row-by-row as results
arrive.

It's meant for a human watching a terminal (or recording one) — a CI
pipeline should still use the scriptable "replay" + "compare" sequence, which
watch does not replace.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var queries []upgrade.QueryStat
			if err := readJSON(queriesPath, &queries); err != nil {
				return err
			}

			sourcePool, err := db.Connect(cmd.Context(), sourceDSN)
			if err != nil {
				return fmt.Errorf("connecting to source: %w", err)
			}
			defer sourcePool.Close()

			targetPool, err := db.Connect(cmd.Context(), targetDSN)
			if err != nil {
				return fmt.Errorf("connecting to target: %w", err)
			}
			defer targetPool.Close()

			model := tui.NewModel(queries, sourcePool, targetPool, threshold)

			finalModel, err := tea.NewProgram(model, tea.WithAltScreen()).Run()
			if err != nil {
				return fmt.Errorf("running watch UI: %w", err)
			}

			m, ok := finalModel.(tui.Model)
			if !ok {
				return fmt.Errorf("unexpected model type returned from watch UI")
			}

			fmt.Printf("watched %d/%d queries\n", len(m.Results()), len(queries))

			if m.AnyRegression() {
				return fmt.Errorf("regression(s) detected — see above")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&sourceDSN, "source-dsn", "", "source instance connection string (required)")
	cmd.Flags().StringVar(&targetDSN, "target-dsn", "", "target instance connection string (required)")
	cmd.Flags().StringVar(&queriesPath, "queries", "queries.json", "captured query set from `upgrade capture`")
	cmd.Flags().Float64Var(&threshold, "threshold", 0.20, "fractional latency regression that fails the gate, e.g. 0.20 = 20%")
	cmd.MarkFlagRequired("source-dsn")
	cmd.MarkFlagRequired("target-dsn")

	return cmd
}
