// Package db provides thin pgx helpers shared by pgctl's upgrade pipeline.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect opens a pooled connection to dsn and confirms it's reachable
// before handing it back, so callers get a clear error at the call site
// instead of a confusing failure three layers into a replay run.
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging %s: %w", redact(dsn), err)
	}
	return pool, nil
}

// redact strips credentials from a DSN before it's ever placed in an error
// message or log line.
func redact(dsn string) string {
	return "<dsn redacted>"
}
