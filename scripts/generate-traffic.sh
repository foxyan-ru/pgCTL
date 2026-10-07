#!/usr/bin/env bash
# Seeds pg-old with pgbench's standard schema and runs a short workload so
# pg_stat_statements has something for `pgctl upgrade capture` to read.
#
# Usage: ./scripts/generate-traffic.sh [duration_seconds]
set -euo pipefail

DSN="${SOURCE_DSN:-postgres://pgctl:pgctl@localhost:5432/appdb}"
DURATION="${1:-30}"

echo "initializing pgbench schema against $DSN"
pgbench -i -s 10 "$DSN"

echo "running pgbench for ${DURATION}s"
pgbench -c 4 -j 2 -T "$DURATION" "$DSN"

echo "done — run: pgctl upgrade capture --dsn \"$DSN\" --out queries.json"
