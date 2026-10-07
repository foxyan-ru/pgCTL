package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryResult is the outcome of replaying one captured query against a
// target instance.
type QueryResult struct {
	Query         string  `json:"query"`
	LatencyMs     float64 `json:"latencyMs"`     // Postgres-reported execution time
	PlanNodeType  string  `json:"planNodeType"`  // top-level plan node, e.g. "Seq Scan", "Index Scan"
	PlanTotalCost float64 `json:"planTotalCost"`
	Error         string  `json:"error,omitempty"`
}

// Report is a full replay run against one target, ready to be diffed by
// Compare or persisted as JSON for later comparison.
type Report struct {
	Label       string        `json:"label"` // e.g. "before" (source) or "after" (upgraded candidate)
	GeneratedAt time.Time     `json:"generatedAt"`
	Results     []QueryResult `json:"results"`
}

// explainRow mirrors the shape Postgres returns for
// EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) <query>.
type explainRow struct {
	Plan struct {
		NodeType        string  `json:"Node Type"`
		TotalCost       float64 `json:"Total Cost"`
		ActualTotalTime float64 `json:"Actual Total Time"`
	} `json:"Plan"`
	ExecutionTime float64 `json:"Execution Time"`
}

// Replay runs every captured query against pool inside a transaction that is
// always rolled back, so a captured INSERT/UPDATE/DELETE can be safely
// replayed for timing and plan-shape comparison without mutating the target
// database. This is what makes it safe to replay production-shaped traffic
// (including writes) against a candidate instance without a throwaway
// dataset or write-blocking triggers.
//
// See docs/adr/0001-why-transaction-wrapped-replay.md for the reasoning and
// its limits (multi-statement transactions and side effects outside the DB,
// e.g. an application-level webhook a stored procedure might call, are not
// covered).
func Replay(ctx context.Context, pool *pgxpool.Pool, queries []QueryStat, label string) (*Report, error) {
	report := &Report{Label: label, GeneratedAt: time.Now().UTC()}

	for _, q := range queries {
		result := QueryResult{Query: q.Query}

		err := func() error {
			tx, err := pool.Begin(ctx)
			if err != nil {
				return fmt.Errorf("begin: %w", err)
			}
			defer tx.Rollback(ctx) // always rollback: replay must never mutate the target

			var raw []byte
			explainSQL := "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) " + q.Query
			if err := tx.QueryRow(ctx, explainSQL).Scan(&raw); err != nil {
				return err
			}

			var rows []explainRow
			if err := json.Unmarshal(raw, &rows); err != nil {
				return fmt.Errorf("decoding explain output: %w", err)
			}
			if len(rows) == 0 {
				return fmt.Errorf("empty explain output")
			}

			result.LatencyMs = rows[0].ExecutionTime
			result.PlanNodeType = rows[0].Plan.NodeType
			result.PlanTotalCost = rows[0].Plan.TotalCost
			return nil
		}()

		if err != nil {
			// A single unreplayable query (e.g. it references a temp table,
			// or uses a parameter placeholder pg_stat_statements normalized
			// away) shouldn't abort the whole run — record it and move on.
			result.Error = err.Error()
		}

		report.Results = append(report.Results, result)
	}

	return report, nil
}
