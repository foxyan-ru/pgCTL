package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"pgctl/internal/db"
	"pgctl/internal/upgrade"
)

func newUpgradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Validate and perform a major-version Postgres upgrade",
		Long: `upgrade is pgctl's flagship workflow: capture real traffic from the
current (source) instance, replay it safely against the candidate (target)
instance, compare the two, and only then cut over — with a one-command
rollback if anything looks wrong.

Typical sequence:

  pgctl upgrade capture --dsn "$SOURCE_DSN"          --out queries.json
  pgctl upgrade replay  --dsn "$SOURCE_DSN"  --queries queries.json --out before.json --label before
  pgctl upgrade replay  --dsn "$TARGET_DSN"  --queries queries.json --out after.json  --label after
  pgctl upgrade compare --before before.json --after after.json --threshold 0.20
  pgctl upgrade cutover --to new
  pgctl upgrade rollback   # if you need to undo the cutover above

For an interactive, screen-recording-friendly alternative to the manual
replay+compare two-step above, see "pgctl upgrade watch", which connects to
both instances and fills in the same comparison live, one query at a time.`,
	}

	cmd.AddCommand(
		newUpgradeCaptureCmd(),
		newUpgradeReplayCmd(),
		newUpgradeCompareCmd(),
		newUpgradeWatchCmd(),
		newUpgradeCutoverCmd(),
		newUpgradeRollbackCmd(),
	)
	return cmd
}

func newUpgradeCaptureCmd() *cobra.Command {
	var dsn, out string
	var limit int

	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Capture the hottest queries from pg_stat_statements on the source instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, err := db.Connect(cmd.Context(), dsn)
			if err != nil {
				return err
			}
			defer pool.Close()

			queries, err := upgrade.Capture(cmd.Context(), pool, limit)
			if err != nil {
				return err
			}
			if len(queries) == 0 {
				fmt.Println("captured 0 queries — has any traffic run against this instance yet?")
			}
			if err := writeJSON(out, queries); err != nil {
				return err
			}
			fmt.Printf("captured %d queries -> %s\n", len(queries), out)
			return nil
		},
	}

	cmd.Flags().StringVar(&dsn, "dsn", "", "source instance connection string (required)")
	cmd.Flags().StringVar(&out, "out", "queries.json", "where to write the captured query list")
	cmd.Flags().IntVar(&limit, "limit", 50, "number of top queries to capture, by total execution time")
	cmd.MarkFlagRequired("dsn")
	return cmd
}

func newUpgradeReplayCmd() *cobra.Command {
	var dsn, queriesPath, out, label string

	cmd := &cobra.Command{
		Use:   "replay",
		Short: "Replay a captured query set against an instance and record latency/plan shape",
		RunE: func(cmd *cobra.Command, args []string) error {
			var queries []upgrade.QueryStat
			if err := readJSON(queriesPath, &queries); err != nil {
				return err
			}

			pool, err := db.Connect(cmd.Context(), dsn)
			if err != nil {
				return err
			}
			defer pool.Close()

			report, err := upgrade.Replay(cmd.Context(), pool, queries, label)
			if err != nil {
				return err
			}
			if err := writeJSON(out, report); err != nil {
				return err
			}
			fmt.Printf("replayed %d queries against %q -> %s\n", len(report.Results), label, out)
			return nil
		},
	}

	cmd.Flags().StringVar(&dsn, "dsn", "", "target instance connection string (required)")
	cmd.Flags().StringVar(&queriesPath, "queries", "queries.json", "captured query set from `upgrade capture`")
	cmd.Flags().StringVar(&out, "out", "report.json", "where to write the replay report")
	cmd.Flags().StringVar(&label, "label", "after", "label for this report, e.g. \"before\" or \"after\"")
	cmd.MarkFlagRequired("dsn")
	return cmd
}

func newUpgradeCompareCmd() *cobra.Command {
	var beforePath, afterPath, out string
	var threshold float64

	cmd := &cobra.Command{
		Use:   "compare",
		Short: "Diff two replay reports and flag latency/plan regressions",
		RunE: func(cmd *cobra.Command, args []string) error {
			var before, after upgrade.Report
			if err := readJSON(beforePath, &before); err != nil {
				return err
			}
			if err := readJSON(afterPath, &after); err != nil {
				return err
			}

			comparison := upgrade.Compare(&before, &after, threshold)
			if out != "" {
				if err := writeJSON(out, comparison); err != nil {
					return err
				}
			}

			printComparisonSummary(comparison)

			if comparison.AnyRegression {
				return fmt.Errorf("regression(s) detected — see above")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&beforePath, "before", "before.json", "replay report from the source version")
	cmd.Flags().StringVar(&afterPath, "after", "after.json", "replay report from the candidate version")
	cmd.Flags().StringVar(&out, "out", "comparison.json", "where to write the full comparison report (optional)")
	cmd.Flags().Float64Var(&threshold, "threshold", 0.20, "fractional latency regression that fails the gate, e.g. 0.20 = 20%")
	return cmd
}

func printComparisonSummary(c *upgrade.ComparisonReport) {
	for _, comp := range c.Comparisons {
		switch {
		case comp.MissingAfter:
			fmt.Printf("[MISSING] %s\n", truncate(comp.Query, 80))
		case comp.Regressed:
			fmt.Printf("[REGRESSED] %s\n    %.2fms -> %.2fms (%.0f%%), plan: %s -> %s\n",
				truncate(comp.Query, 80), comp.BeforeMs, comp.AfterMs, comp.DeltaPct*100, comp.BeforePlan, comp.AfterPlan)
		default:
			fmt.Printf("[ok]        %s (%.2fms -> %.2fms)\n", truncate(comp.Query, 80), comp.BeforeMs, comp.AfterMs)
		}
	}
	fmt.Printf("\n%d/%d queries regressed (threshold %.0f%%)\n",
		countRegressed(c), len(c.Comparisons), c.FailThreshold*100)
}

func countRegressed(c *upgrade.ComparisonReport) int {
	n := 0
	for _, comp := range c.Comparisons {
		if comp.Regressed || comp.MissingAfter {
			n++
		}
	}
	return n
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func newUpgradeCutoverCmd() *cobra.Command {
	var to, statePath string

	cmd := &cobra.Command{
		Use:   "cutover",
		Short: "Mark the given target (\"old\" or \"new\") as active",
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := upgrade.Cutover(statePath, to)
			if err != nil {
				return err
			}
			fmt.Printf("cutover complete: active=%s (previous=%s)\n", state.Active, state.Previous)
			return nil
		},
	}

	cmd.Flags().StringVar(&to, "to", "new", "cutover target: \"old\" or \"new\"")
	cmd.Flags().StringVar(&statePath, "state", "cutover-state.json", "path to the cutover state file")
	return cmd
}

func newUpgradeRollbackCmd() *cobra.Command {
	var statePath string

	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Undo the most recent cutover",
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := upgrade.Rollback(statePath)
			if err != nil {
				return err
			}
			fmt.Printf("rolled back: active=%s\n", state.Active)
			return nil
		},
	}

	cmd.Flags().StringVar(&statePath, "state", "cutover-state.json", "path to the cutover state file")
	return cmd
}

func writeJSON(path string, v interface{}) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling json: %w", err)
	}
	return os.WriteFile(path, b, 0o644)
}

func readJSON(path string, v interface{}) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	return nil
}
