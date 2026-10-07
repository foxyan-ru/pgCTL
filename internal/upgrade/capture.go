// Package upgrade implements pgctl's flagship feature: capturing real query
// traffic from a running Postgres instance, replaying it safely against a
// candidate (upgraded) instance, and comparing the two to decide whether a
// major-version upgrade is safe to cut over to.
package upgrade

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryStat is one row of captured traffic: a distinct query shape and its
// observed call frequency on the source instance, taken from
// pg_stat_statements. Capturing shape+frequency (not just a query list) lets
// replay weight expensive-but-rare queries against cheap-but-hot ones the
// same way production traffic would.
type QueryStat struct {
	Query   string  `json:"query"`
	Calls   int64   `json:"calls"`
	MeanMs  float64 `json:"meanMs"`
	Rows    int64   `json:"rows"`
}

// Capture reads the top `limit` queries by total execution time from
// pg_stat_statements. It requires the extension to be created and
// shared_preload_libraries='pg_stat_statements' set on the source instance
// (see docker-compose.yml).
func Capture(ctx context.Context, pool *pgxpool.Pool, limit int) ([]QueryStat, error) {
	const q = `
		SELECT query, calls, mean_exec_time, rows
		FROM pg_stat_statements
		WHERE query NOT ILIKE '%pg_stat_statements%'
		ORDER BY total_exec_time DESC
		LIMIT $1`

	rows, err := pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("querying pg_stat_statements (is the extension installed?): %w", err)
	}
	defer rows.Close()

	var out []QueryStat
	for rows.Next() {
		var s QueryStat
		if err := rows.Scan(&s.Query, &s.Calls, &s.MeanMs, &s.Rows); err != nil {
			return nil, fmt.Errorf("scanning pg_stat_statements row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating pg_stat_statements: %w", err)
	}
	return out, nil
}
