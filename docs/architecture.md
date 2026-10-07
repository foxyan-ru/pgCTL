# pgctl architecture

## Problem

Upgrading Postgres across a major version (e.g. 15 → 16) is the single
riskiest routine operation in running Postgres yourself: a plan-shape change,
a removed feature, or a subtle semantic difference can silently regress
production, and you usually only find out from users. pgctl's flagship
feature is a repeatable way to answer *"is this upgrade safe to cut over to?"*
using traffic shaped like what actually runs against your database, before
you touch production.

## Components

```
                    ┌────────────────────┐
                    │   pgctl (CLI)       │
                    │  cmd/pgctl/main.go   │
                    └─────────┬───────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
┌───────▼────────┐   ┌────────▼─────────┐   ┌───────▼────────┐
│ internal/config │   │ internal/upgrade │   │ internal/       │
│ cluster.yaml     │   │  capture         │   │ dockerutil      │
│ load/save/valid. │   │  replay          │   │  compose up/    │
│                  │   │  compare         │   │  scale/down     │
│                  │   │  cutover/rollback│   │                 │
└──────────────────┘   └────────┬─────────┘   └────────┬────────┘
                                 │                      │
                        ┌────────▼────────┐    ┌────────▼────────┐
                        │ internal/db      │    │ docker compose   │
                        │ pgx pool connect │    │ pg-old / pg-new /│
                        └────────┬────────┘    │ pg-replica       │
                                 │              └─────────────────┘
                        ┌────────▼────────┐
                        │  Postgres        │
                        │  (source/target) │
                        └─────────────────┘
```

## The upgrade validation flow

1. **Capture** (`internal/upgrade/capture.go`) reads the hottest queries from
   `pg_stat_statements` on the running source instance — real shapes and call
   frequencies, not synthetic guesses.
2. **Replay** (`internal/upgrade/replay.go`) runs each captured query as
   `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` inside a transaction that's
   always rolled back, against both the source and the candidate instance.
   See [ADR 0001](adr/0001-why-transaction-wrapped-replay.md) for why this is
   safe to do even for captured writes.
3. **Compare** (`internal/upgrade/compare.go`) diffs the two reports:
   latency delta against a threshold, and — independently — whether the
   query's plan shape changed at all. See
   [ADR 0002](adr/0002-naive-threshold-vs-statistical-gate.md) for why this
   threshold is intentionally naive, and where the rigorous version of this
   problem lives instead (BenchGate).
4. **Cutover / Rollback** (`internal/upgrade/cutover.go`) flips a local
   marker recording which instance is "active" and remembers the prior state,
   so a bad cutover has a one-command undo. In a real deployment this step is
   "reconfigure the connection proxy (PgBouncer/HAProxy) or your app's
   connection string" — pgctl models the decision procedure, not the network
   plumbing. See [ADR 0003](adr/0003-operator-vs-imperative-automation.md)
   for the broader scope call this reflects.

## What's explicitly out of scope

- A Kubernetes operator (ADR 0003).
- Statistically rigorous regression detection across multiple runs (ADR
  0002) — that's BenchGate's job.
- Real connection-proxy reconfiguration during cutover.
- Multi-cloud/Terraform provisioning — the local demo is Docker Compose only.
