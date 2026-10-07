# pgctl

A Postgres lifecycle control plane for local Docker clusters, built around one
flagship capability: **validating a major-version upgrade by replaying
real, captured traffic against the candidate version before you cut over** —
then giving you a scripted, one-command rollback if it doesn't hold up.

## Problem statement

Postgres major-version upgrades are the operation most likely to silently
break production: a plan-shape change, a removed feature, or a subtle
semantic difference shows up as "queries got slower" days later, not as an
error during the upgrade itself. Most teams either upgrade and hope, or
build a bespoke one-off validation script per upgrade. pgctl makes that
validation a repeatable, four-step workflow: capture → replay → compare →
cutover (with rollback).

Everything runs locally against Docker — no cloud spend, nothing that needs
to stay running.

## Architecture

See [docs/architecture.md](docs/architecture.md) for the full component
diagram. In short:

```
pgctl upgrade capture   →  reads pg_stat_statements on the source instance
pgctl upgrade replay    →  EXPLAIN ANALYZE each query, rolled back, on a target
pgctl upgrade compare   →  diffs latency + plan shape between two replay reports
pgctl upgrade cutover   →  marks "old" or "new" as active, remembers the prior state
pgctl upgrade rollback  →  undoes the last cutover
```

The `create`/`configure`/`scale`/`decommission` commands are a deliberately
thin wrapper around Docker Compose — see
[ADR 0003](docs/adr/0003-operator-vs-imperative-automation.md) for why a full
Kubernetes operator is explicitly out of scope for this project.

Design decisions worth reading before the code:

- [ADR 0001 — why replay is transaction-wrapped `EXPLAIN ANALYZE`](docs/adr/0001-why-transaction-wrapped-replay.md)
- [ADR 0002 — why the regression gate is a naive threshold (and where the rigorous version lives)](docs/adr/0002-naive-threshold-vs-statistical-gate.md)
- [ADR 0003 — operator vs. imperative automation](docs/adr/0003-operator-vs-imperative-automation.md)

## Setup

Requires Go 1.24+ and Docker.

```bash
go mod tidy       # fetches cobra, pgx, yaml.v3 — not run yet in this scaffold
make build        # builds ./bin/pgctl
```

## Quickstart

```bash
pgctl create                          # starts pg-old (15) + pg-new (16) via Docker Compose
./scripts/generate-traffic.sh 30      # seeds pgbench schema, runs traffic for 30s

SOURCE_DSN="postgres://pgctl:pgctl@localhost:5432/appdb"
TARGET_DSN="postgres://pgctl:pgctl@localhost:5433/appdb"

pgctl upgrade capture --dsn "$SOURCE_DSN" --out queries.json
pgctl upgrade replay  --dsn "$SOURCE_DSN" --queries queries.json --out before.json --label before
pgctl upgrade replay  --dsn "$TARGET_DSN" --queries queries.json --out after.json  --label after
pgctl upgrade compare --before before.json --after after.json --threshold 0.20

pgctl upgrade cutover --to new        # only after compare passes
pgctl upgrade rollback                # undo, if needed
```

Full walkthrough plus two incident scenarios: [docs/runbook.md](docs/runbook.md).

### Watching it live

`pgctl upgrade watch` is a live terminal UI version of the `replay` +
`compare` pair above: it connects to both instances directly and fills in a
comparison table row-by-row as each query gets replayed, instead of writing
two report files and diffing them afterward. It's built for a human watching
a terminal — this is what the project's demo recording uses — and is
complementary to, not a replacement for, the scriptable flow: a CI pipeline
should still gate on `pgctl upgrade compare` against pre-generated JSON
reports, not on a TUI.

```bash
pgctl upgrade watch --source-dsn "$SOURCE_DSN" --target-dsn "$TARGET_DSN" --queries queries.json --threshold 0.20
```

## Status

This is a fresh scaffold: the CLI and core packages
(`internal/config`, `internal/db`, `internal/upgrade`, `internal/dockerutil`)
are real, non-empty implementations, and `internal/upgrade` has unit test
coverage for the comparison and cutover logic. It has **not yet been built or
run** — `go mod tidy`, a build, and an end-to-end run against real containers
are the next steps.

## Companion project

[BenchGate](../benchgate) (not yet scaffolded) is a variance-controlled
statistical benchmarking gate for Postgres — the rigorous regression
detection that ADR 0002 explicitly keeps out of pgctl.
