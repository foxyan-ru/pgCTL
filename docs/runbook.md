# Runbook: major-version upgrade validation

This walks through the end-to-end flagship flow and two incident scenarios
worth demoing on camera.

## Normal flow

```bash
pgctl create
./scripts/generate-traffic.sh 30

SOURCE_DSN="postgres://pgctl:pgctl@localhost:5432/appdb"
TARGET_DSN="postgres://pgctl:pgctl@localhost:5433/appdb"

pgctl upgrade capture --dsn "$SOURCE_DSN" --out queries.json
pgctl upgrade replay  --dsn "$SOURCE_DSN" --queries queries.json --out before.json --label before
pgctl upgrade replay  --dsn "$TARGET_DSN" --queries queries.json --out after.json  --label after
pgctl upgrade compare --before before.json --after after.json --threshold 0.20

# if compare passes:
pgctl upgrade cutover --to new
```

## Incident: compare flags a regression

`pgctl upgrade compare` exits non-zero and prints each regressed query with
its before/after latency and plan node type. Triage:

1. Read the `[REGRESSED]` lines — look for a plan node type change first
   (e.g. `Index Scan -> Seq Scan`); that's almost always the actual cause.
2. If it's a plan change: check whether the candidate version's planner
   needs updated statistics (`ANALYZE`) or a changed default
   (`default_statistics_target`, a removed/renamed GUC) — check the target
   version's release notes for the specific query's table.
3. If it's latency-only with no plan change: re-run `replay` once more
   against both instances — a single run can be noisy (see ADR 0002) —
   before concluding it's a real regression.
4. Do **not** run `pgctl upgrade cutover --to new` until compare passes.

## Incident: a bad cutover needs to be undone

```bash
pgctl upgrade rollback
```

This reverts `cutover-state.json` to whatever was active before the last
cutover. In this local demo, "active" is a marker file a downstream script or
app would read to pick its connection string — wire your own app's config
loader to read it if you want the demo to show an app actually switching
targets live.

## Incident: demo the safety net on camera

A good 30-45s clip: intentionally make the candidate version regress (e.g.
drop an index that a hot query relies on before starting `pg-new`), run the
full flow above, and show `compare` catching it and refusing to let you claim
success — then fix it and show a clean pass.

`pgctl upgrade watch` is now the easier way to record that clip: it fills in
the comparison table row-by-row live as each query replays, so the regressed
row turning red is visible in real time instead of only appearing in a
static printout at the end of the run.
