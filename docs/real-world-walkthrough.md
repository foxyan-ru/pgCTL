# pgctl: Real-World Walkthrough

> **Audience:** Backend engineers reading the repo + Product managers who need the plain-English version.
>
> This document walks through exactly how `pgctl` would be used at a real company,
> step by step, with diagrams, file references, design patterns, and tradeoffs.

---

## The Problem pgctl Solves

PostgreSQL major-version upgrades (e.g., 15 → 16) are the single most dangerous routine
operation a backend team performs. The database doesn't just store your data — its **query
planner** decides *how* to retrieve that data. A new major version ships a new planner,
and that planner can silently choose worse execution plans for queries that worked fine before.

**What goes wrong without pgctl:**

| Company analogy | What happens |
|-----------------|-------------|
| **Stripe** upgrades Postgres 15 → 16 | A payment-lookup query that used an index scan now does a sequential scan. Latency jumps from 4ms to 200ms. Checkout timeouts spike. Revenue loss. |
| **Shopify** upgrades Postgres 14 → 15 | A product-search query plan changes. The storefront is 3× slower during Black Friday prep. Rollback takes 45 minutes of downtime. |
| **Robinhood** upgrades Postgres 13 → 14 | A portfolio-balance aggregation regresses. Users see stale data for 20 minutes before anyone notices. SEC compliance concern. |

**pgctl automates the "did anything get worse?" check *before* you flip the switch.**

---

## Real-World Scenario: Acme Fintech

Let's follow **Acme Fintech**, a Series B payments company, through a Postgres 15 → 16
upgrade. They have:

- A primary Postgres 15 instance handling transactions
- ~500 distinct query shapes hitting the database
- A 3-person platform team that can't afford a week of manual testing
- A PM who needs to know "is it safe to upgrade?" with a yes/no answer

---

## Step 0: Cluster Setup

### Plain English

Before anything else, the platform engineer describes what the cluster looks like today
and what the upgrade target is. This is a YAML file — think of it as a "blueprint" for
the database setup.

### What Happens

The engineer creates or edits `cluster.yaml`:

```yaml
name: acme-payments
image: postgres:15          # what we run today
port: 5432
replicas: 1
storage: 50GiB
upgrade:
  targetImage: postgres:16  # what we want to move to
  targetPort: 5433
  failThreshold: 0.20       # flag anything 20%+ slower
```

Then runs:

```bash
pgctl create --compose-file docker-compose.yml
```

This spins up two Docker containers side by side: the current Postgres 15 and the
candidate Postgres 16, both with `pg_stat_statements` enabled.

### High-Level Diagram

```mermaid
graph LR
    A[Engineer runs<br/>'pgctl create'] --> B[Load cluster.yaml]
    B --> C[docker compose up -d]
    C --> D["pg-old (Postgres 15)<br/>port 5432"]
    C --> E["pg-new (Postgres 16)<br/>port 5433"]
    D --- F[pg_stat_statements<br/>extension enabled]
    E --- G[pg_stat_statements<br/>extension enabled]
```

### Low-Level Diagram

```mermaid
sequenceDiagram
    participant Eng as Engineer
    participant CLI as pgctl CLI
    participant Cfg as config.Load()
    participant Docker as dockerutil.Up()
    participant PG15 as pg-old container
    participant PG16 as pg-new container

    Eng->>CLI: pgctl create
    CLI->>Cfg: Load("cluster.yaml")
    Cfg-->>CLI: Cluster{Name:"acme-payments", Image:"postgres:15", ...}
    CLI->>Docker: Up(ctx, "docker-compose.yml", "pg-old", "pg-new")
    Docker->>PG15: docker compose up -d pg-old
    Docker->>PG16: docker compose up -d pg-new
    Note over PG15: Runs init-pg-stat-statements.sql
    Note over PG16: Runs init-pg-stat-statements.sql
    Docker-->>CLI: containers running
    CLI-->>Eng: ✓ Cluster "acme-payments" created
```

### Repo Files

| File | What it does |
|------|-------------|
| [`internal/config/config.go:16-26`](../internal/config/config.go) | `Cluster` and `Upgrade` structs — the blueprint schema |
| [`internal/config/config.go:38-53`](../internal/config/config.go) | `Load()` — reads YAML, validates defaults |
| [`internal/config/config.go:65-82`](../internal/config/config.go) | `Validate()` — applies defaults, rejects bad input |
| [`internal/cli/create.go:14-35`](../internal/cli/create.go) | `newCreateCmd()` — wires the `create` subcommand |
| [`internal/dockerutil/compose.go:15-28`](../internal/dockerutil/compose.go) | `Compose()` — shells out to `docker compose` |
| [`internal/dockerutil/compose.go:30-35`](../internal/dockerutil/compose.go) | `Up()` — starts services in detached mode |
| [`docker-compose.yml`](../docker-compose.yml) | Defines pg-old, pg-new, pg-replica services |
| [`scripts/init-pg-stat-statements.sql`](../scripts/init-pg-stat-statements.sql) | Auto-creates the `pg_stat_statements` extension |

### Design Pattern: Declarative Configuration

**Pattern:** The cluster is described declaratively (YAML), not imperatively (a series of
`docker run` commands). The config struct is validated on load, with sensible defaults
applied automatically.

**Architectural principle:** *Separation of description from execution.* The YAML
describes *what* the cluster should look like. The CLI commands decide *how* to make it
happen. This is the same principle behind Kubernetes manifests, Terraform HCL, and
Docker Compose files themselves.

**Tradeoffs:**

| Decision | Upside | Downside |
|----------|--------|----------|
| YAML config with defaults | Engineers only specify what differs from the norm | Yet another config file to maintain |
| Docker Compose under the hood | Zero infrastructure cost, runs on any laptop | Not production-representative (no real networking, no real disk I/O patterns) |
| Shell-out to `docker compose` CLI | No Docker SDK dependency, easy to debug | Fragile string parsing, no structured error handling from Docker |
| `pg_stat_statements` enabled via Compose command flags | Works out of the box, no manual setup | Ties the tool to Postgres's built-in stats; won't work with proxies that strip extensions |

---

## Step 1: Capture — "What queries does production actually run?"

### Plain English

**For the PM:** We take a snapshot of the top 50 most expensive queries running against
our current database. Think of it like recording the "greatest hits" playlist of database
activity — these are the queries that matter most to our users.

**For the engineer:** We query `pg_stat_statements` to extract the top N queries ranked
by total execution time. Each entry includes the normalized query text, call count,
mean latency, and rows returned.

### What Happens at Acme Fintech

The platform engineer points pgctl at their source database:

```bash
pgctl upgrade capture \
  --dsn "postgres://pgctl:pgctl@localhost:5432/appdb" \
  --out queries.json \
  --limit 50
```

This produces a JSON file with entries like:

```json
[
  {
    "query": "SELECT balance FROM accounts WHERE id = $1",
    "calls": 1482937,
    "mean_ms": 0.42,
    "rows": 1482937
  },
  {
    "query": "INSERT INTO transactions (from_id, to_id, amount) VALUES ($1, $2, $3)",
    "calls": 312044,
    "mean_ms": 1.8,
    "rows": 312044
  }
]
```

### High-Level Diagram

```mermaid
graph LR
    A["Production Postgres 15<br/>(pg-old)"] -->|pg_stat_statements| B[pgctl upgrade capture]
    B --> C["queries.json<br/>(top 50 by exec time)"]

    style A fill:#326CE5,color:#fff
    style C fill:#2ECC71,color:#fff
```

### Low-Level Diagram

```mermaid
sequenceDiagram
    participant Eng as Engineer
    participant CLI as upgrade.go:newUpgradeCaptureCmd()
    participant DB as db.Connect()
    participant PG as Postgres 15 (pg-old)
    participant Cap as upgrade.Capture()

    Eng->>CLI: pgctl upgrade capture --dsn ... --out queries.json --limit 50
    CLI->>DB: Connect(ctx, dsn)
    DB->>PG: pgxpool.New() + Ping()
    PG-->>DB: connection pool ready
    DB-->>CLI: *pgxpool.Pool

    CLI->>Cap: Capture(ctx, pool, 50)
    Cap->>PG: SELECT query, calls, mean_exec_time, rows<br/>FROM pg_stat_statements<br/>ORDER BY total_exec_time DESC<br/>LIMIT 50
    PG-->>Cap: 50 rows
    Cap-->>CLI: []QueryStat

    CLI->>CLI: writeJSON("queries.json", stats)
    CLI-->>Eng: captured 50 queries → queries.json
```

### Repo Files

| File | What it does |
|------|-------------|
| [`internal/upgrade/capture.go:19-25`](../internal/upgrade/capture.go) | `QueryStat` struct — one captured query shape |
| [`internal/upgrade/capture.go:30-56`](../internal/upgrade/capture.go) | `Capture()` — queries `pg_stat_statements`, returns top N |
| [`internal/cli/upgrade.go:48-82`](../internal/cli/upgrade.go) | `newUpgradeCaptureCmd()` — CLI wiring for `capture` |
| [`internal/db/db.go:15-30`](../internal/db/db.go) | `Connect()` — pooled connection with health check |
| [`internal/db/db.go:32-34`](../internal/db/db.go) | `redact()` — prevents DSN leakage in error messages |

### Design Pattern: Observation Over Instrumentation

**Pattern:** Instead of requiring the application to tag or instrument its queries, pgctl
reads what Postgres *already knows* via `pg_stat_statements`. This is the **Observer
pattern** applied at the infrastructure level — you observe the system's behavior without
modifying it.

**Architectural principle:** *Use the platform's built-in telemetry.* Postgres tracks
query statistics natively. Building on this means zero application code changes and zero
risk of the observation itself affecting performance.

**Tradeoffs:**

| Decision | Upside | Downside |
|----------|--------|----------|
| Use `pg_stat_statements` | Zero app changes, captures ALL queries (including ORM-generated ones the team didn't write) | Misses bind-parameter values — can't replay with real data, only the query *shape* |
| Rank by `total_exec_time DESC` | Prioritizes queries with the most cumulative impact | A rare but catastrophic query (runs once/day, takes 30s) might rank below a fast frequent one |
| JSON output | Easy to inspect, version-control, diff, pipe to other tools | Gets large for high-limit captures; no streaming |
| DSN redaction in errors | Prevents credential leakage in CI logs | Makes debugging connection issues harder (you can't see *which* host failed) |

---

## Step 2: Replay — "How does each query perform on the new version?"

### Plain English

**For the PM:** We take that playlist of queries and play each one against *both*
databases — the current Postgres 15 and the candidate Postgres 16. For each query, we
measure how long it takes and what execution plan the database chose. Crucially, we do
this inside a transaction that we immediately roll back, so **no data is changed**.

**For the engineer:** Each query is wrapped in `BEGIN` → `EXPLAIN (ANALYZE, BUFFERS,
FORMAT JSON) <query>` → `ROLLBACK`. This gives us real execution timing and the full
query plan tree without mutating any data. We run this against both instances to produce
a "before" and "after" report.

### What Happens at Acme Fintech

```bash
# Replay against the CURRENT version (baseline)
pgctl upgrade replay \
  --dsn "postgres://pgctl:pgctl@localhost:5432/appdb" \
  --queries queries.json \
  --out before.json \
  --label before

# Replay against the CANDIDATE version
pgctl upgrade replay \
  --dsn "postgres://pgctl:pgctl@localhost:5433/appdb" \
  --queries queries.json \
  --out after.json \
  --label after
```

Each report contains per-query results:

```json
{
  "label": "after",
  "generated_at": "2026-09-29T14:30:00Z",
  "results": [
    {
      "query": "SELECT balance FROM accounts WHERE id = $1",
      "latency_ms": 0.38,
      "plan_node_type": "Index Scan",
      "plan_total_cost": 8.17,
      "error": ""
    }
  ]
}
```

### High-Level Diagram

```mermaid
graph TB
    Q["queries.json<br/>(captured traffic)"]

    Q --> R1["Replay against<br/>Postgres 15 (pg-old)"]
    Q --> R2["Replay against<br/>Postgres 16 (pg-new)"]

    R1 --> B["before.json<br/>(baseline report)"]
    R2 --> A["after.json<br/>(candidate report)"]

    subgraph "Safety: Transaction Wrapping"
        direction TB
        S1["BEGIN"] --> S2["EXPLAIN ANALYZE query"]
        S2 --> S3["Record timing + plan"]
        S3 --> S4["ROLLBACK ← no data changed"]
    end

    style Q fill:#3498DB,color:#fff
    style B fill:#2ECC71,color:#fff
    style A fill:#E67E22,color:#fff
```

### Low-Level Diagram

```mermaid
sequenceDiagram
    participant CLI as upgrade.go:newUpgradeReplayCmd()
    participant Rep as upgrade.Replay()
    participant PG as Target Postgres

    CLI->>Rep: Replay(ctx, pool, queries, "after")

    loop For each QueryStat
        Rep->>PG: BEGIN
        PG-->>Rep: ok
        Rep->>PG: EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)<br/>SELECT balance FROM accounts WHERE id = $1
        PG-->>Rep: JSON plan + execution time
        Rep->>Rep: Parse explainRow: NodeType, TotalCost, ActualTotalTime
        Rep->>PG: ROLLBACK
        PG-->>Rep: ok
        Rep->>Rep: Append QueryResult to Report
    end

    Rep-->>CLI: Report{Label:"after", Results:[...]}
    CLI->>CLI: writeJSON("after.json", report)
```

### Repo Files

| File | What it does |
|------|-------------|
| [`internal/upgrade/replay.go:14-21`](../internal/upgrade/replay.go) | `QueryResult` struct — one replayed query's outcome |
| [`internal/upgrade/replay.go:23-30`](../internal/upgrade/replay.go) | `Report` struct — labeled collection of results |
| [`internal/upgrade/replay.go:32-50`](../internal/upgrade/replay.go) | `explainRow` struct — mirrors Postgres EXPLAIN JSON format |
| [`internal/upgrade/replay.go:52-97`](../internal/upgrade/replay.go) | `Replay()` — the transaction-wrapped replay loop |
| [`internal/cli/upgrade.go:84-120`](../internal/cli/upgrade.go) | `newUpgradeReplayCmd()` — CLI wiring for `replay` |
| [`docs/adr/0001-why-transaction-wrapped-replay.md`](../docs/adr/0001-why-transaction-wrapped-replay.md) | ADR explaining why transactions are always rolled back |

### Design Pattern: Transaction-Wrapped Safe Execution (Sandbox Pattern)

**Pattern:** Every replayed query runs inside a transaction that is *always* rolled back.
This is the **Sandbox pattern** — you execute real code in a controlled environment where
side effects are discarded.

**Architectural principle:** *Make dangerous operations safe by bounding their blast
radius.* The same principle behind database savepoints, Docker containers, and VM
snapshots: let real work happen inside a boundary that prevents it from escaping.

**This is the core safety mechanism of pgctl.** Without it, replaying an
`INSERT INTO transactions` captured from production would create fake transactions in the
candidate database.

**Tradeoffs:**

| Decision | Upside | Downside |
|----------|--------|----------|
| `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` | Gets *real* execution timing AND the full plan tree | `ANALYZE` actually executes the query (it's not just planning), so it takes real time |
| Always `ROLLBACK` | Writes never persist — safe to replay `INSERT`/`UPDATE`/`DELETE` | Side effects like `NOTIFY`, sequence increments, and triggered webhooks still fire during the transaction window |
| Errors recorded, not fatal | One bad query doesn't abort the whole replay run | A cascade of errors might mask a systemic issue (e.g., missing extension on the candidate) |
| Sequential replay (one query at a time) | Predictable, easy to debug, good for the TUI's visual progress | Doesn't test concurrency effects; real production runs queries simultaneously |
| No bind parameters | Works with `pg_stat_statements`' normalized queries out of the box | Plan choices may differ when the planner doesn't know the actual parameter values |

### Why This Matters at Acme Fintech

Acme's most critical query is:

```sql
INSERT INTO transactions (from_id, to_id, amount) VALUES ($1, $2, $3)
```

Without transaction wrapping, replaying this against the Postgres 16 candidate would
insert garbage rows. With transaction wrapping, the query executes (so we get real
timing), but the `ROLLBACK` ensures nothing persists.

---

## Step 3: Compare — "Did anything get worse?"

### Plain English

**For the PM:** We compare the "before" and "after" reports side by side. For each query,
we check two things:
1. **Did it get slower?** If latency increased by more than 20% (configurable), it's
   flagged as a regression.
2. **Did the database change its strategy?** If Postgres switched from an efficient
   "Index Scan" to a slow "Sequential Scan", it's flagged even if latency looks okay —
   because it *will* get worse at scale.

If any query is flagged, the upgrade is blocked. The team gets a report of exactly which
queries regressed and by how much.

**For the engineer:** The comparison logic builds a lookup map from the "after" report,
iterates the "before" report, computes `DeltaPct = (after - before) / before`, and flags
regressions based on the threshold OR plan-shape changes.

### What Happens at Acme Fintech

```bash
pgctl upgrade compare \
  --before before.json \
  --after after.json \
  --threshold 0.20
```

Output:

```
[ok]         SELECT balance FROM accounts ...          0.42ms → 0.38ms  (-9.5%)
[REGRESSED]  SELECT t.* FROM transactions t JOIN ...   2.10ms → 8.40ms  (+300.0%)  plan: Index Scan → Seq Scan
[ok]         INSERT INTO transactions ...              1.80ms → 1.75ms  (-2.8%)
[MISSING]    SELECT COUNT(*) FROM audit_log ...        3.20ms → error

2 regressions detected. Upgrade NOT safe.
Exit code: 1
```

**The exit code is the key.** Exit code 0 means safe to proceed. Exit code 1 means stop.
This integrates directly into CI/CD pipelines — a non-zero exit blocks the deployment.

### High-Level Diagram

```mermaid
graph TB
    B["before.json<br/>(Postgres 15 baseline)"] --> CMP["pgctl upgrade compare<br/>--threshold 0.20"]
    A["after.json<br/>(Postgres 16 candidate)"] --> CMP

    CMP --> |"exit 0"| SAFE["✓ Safe to upgrade"]
    CMP --> |"exit 1"| BLOCKED["✗ Regressions found"]

    SAFE --> CUT["pgctl upgrade cutover"]
    BLOCKED --> INV["Investigate flagged queries"]

    style SAFE fill:#2ECC71,color:#fff
    style BLOCKED fill:#E74C3C,color:#fff
```

### Low-Level Diagram

```mermaid
sequenceDiagram
    participant CLI as upgrade.go:newUpgradeCompareCmd()
    participant Cmp as upgrade.Compare()
    participant Out as printComparisonSummary()

    CLI->>CLI: readJSON("before.json") → beforeReport
    CLI->>CLI: readJSON("after.json") → afterReport
    CLI->>Cmp: Compare(beforeReport, afterReport, 0.20)

    Note over Cmp: Build map: afterByQuery[query] = QueryResult

    loop For each result in beforeReport
        alt Query found in afterByQuery
            Cmp->>Cmp: deltaPct = (after.Latency - before.Latency) / before.Latency
            alt deltaPct > threshold OR planNodeType changed
                Cmp->>Cmp: Mark Regressed = true
            else Within threshold, same plan
                Cmp->>Cmp: Mark Regressed = false
            end
        else Query missing or errored
            Cmp->>Cmp: Mark MissingAfter = true
        end
    end

    Cmp-->>CLI: ComparisonReport{AnyRegression: true, Comparisons: [...]}
    CLI->>Out: Print each comparison as [ok] / [REGRESSED] / [MISSING]
    CLI-->>CLI: os.Exit(1) if any regression
```

### Repo Files

| File | What it does |
|------|-------------|
| [`internal/upgrade/compare.go:6-17`](../internal/upgrade/compare.go) | `Comparison` struct — per-query verdict with delta and plan info |
| [`internal/upgrade/compare.go:19-33`](../internal/upgrade/compare.go) | `ComparisonReport` struct — full triage output |
| [`internal/upgrade/compare.go:35-77`](../internal/upgrade/compare.go) | `Compare()` — the core diffing logic |
| [`internal/cli/upgrade.go:122-190`](../internal/cli/upgrade.go) | `newUpgradeCompareCmd()` + `printComparisonSummary()` — CLI wiring and output formatting |
| [`internal/upgrade/compare_test.go`](../internal/upgrade/compare_test.go) | Unit tests: latency regression, plan-shape change, within-threshold, missing-after |
| [`docs/adr/0002-naive-threshold-vs-statistical-gate.md`](../docs/adr/0002-naive-threshold-vs-statistical-gate.md) | ADR explaining why a simple threshold was chosen over statistical analysis |

### Design Pattern: Gate / Quality Gate Pattern

**Pattern:** The comparison step acts as a **quality gate** — a binary pass/fail check
that must succeed before the pipeline can proceed. This is the same pattern used by:
- CI/CD test suites (tests pass → deploy; tests fail → block)
- Code coverage thresholds
- Security scanners in pull request checks

**Architectural principle:** *Fail closed, not open.* If a query is missing from the
"after" report (it errored during replay), pgctl flags it as a regression rather than
silently skipping it. This is the conservative choice — false positives are annoying but
safe; false negatives cause outages.

**Tradeoffs:**

| Decision | Upside | Downside |
|----------|--------|----------|
| Naive percentage threshold (20% default) | Simple to understand, easy to configure | A query that goes from 0.001ms to 0.002ms is a "100% regression" but meaningless in practice |
| Plan-shape change detection | Catches the most dangerous regressions (Index Scan → Seq Scan looks fast on small test data but explodes at scale) | Plan node types are coarse — "Index Scan" vs "Index Only Scan" is a meaningful difference that's not caught |
| Missing = regressed | Conservative — won't miss a problem | An extension missing on the candidate (not a real regression) blocks the upgrade |
| Exit code as API | Integrates with any CI system, shell scripts, Makefiles | Loses nuance — you get "failed" but not "which queries" unless you parse stdout |
| Single-run comparison | Fast, deterministic | Latency is noisy — a single run can't distinguish a real regression from random variance |

### Why This Matters at Acme Fintech

Acme's `SELECT t.* FROM transactions t JOIN accounts a ON ...` query regressed because
Postgres 16's planner chose a sequential scan on the `transactions` table instead of an
index scan. On the test dataset (10K rows) the difference is 2ms → 8ms. On production
(50M rows), this would be 2ms → **45 seconds**. The plan-shape flag caught this before
it hit production.

---

## Step 4: Watch — "Show me the replay live"

### Plain English

**For the PM:** Instead of running capture/replay/compare as separate steps, the
engineer can use a live dashboard that replays queries one by one and shows results in
real time. Green means OK, red means regression, yellow means the query failed. This is
the "demo mode" — great for showing stakeholders that the upgrade is safe.

**For the engineer:** The `watch` command runs a Bubble Tea terminal UI that connects to
both instances simultaneously, replays each query against both, computes the comparison
inline, and renders results in a live-updating table with a progress bar.

### What Happens at Acme Fintech

The platform engineer runs:

```bash
pgctl upgrade watch \
  --source-dsn "postgres://pgctl:pgctl@localhost:5432/appdb" \
  --target-dsn "postgres://pgctl:pgctl@localhost:5433/appdb" \
  --queries queries.json \
  --threshold 0.20
```

A live terminal UI appears:

```
─── pgctl upgrade watch ──────────────────────
Replaying query 23/50...
[====================            ] 46%

┌─────────────────────────────┬──────┬──────┬───────┬──────────┬──────────┐
│ Query                       │ Before│ After│ Δ%    │ Plan Δ   │ Status   │
├─────────────────────────────┼──────┼──────┼───────┼──────────┼──────────┤
│ SELECT balance FROM acco... │ 0.42 │ 0.38 │ -9.5% │ same     │ ok       │
│ SELECT t.* FROM transact... │ 2.10 │ 8.40 │+300.0%│ changed  │REGRESSED │
│ INSERT INTO transactions... │ 1.80 │ 1.75 │ -2.8% │ same     │ ok       │
└─────────────────────────────┴──────┴──────┴───────┴──────────┴──────────┘
  press q to quit │ 1 regression so far
```

### High-Level Diagram

```mermaid
graph TB
    Q["queries.json"] --> W["pgctl upgrade watch"]

    W --> S["Source Pool<br/>(Postgres 15)"]
    W --> T["Target Pool<br/>(Postgres 16)"]

    S --> CMP["Compare each query<br/>in real time"]
    T --> CMP

    CMP --> TUI["Bubble Tea TUI<br/>Live table + progress bar"]

    TUI --> |"q to quit"| EXIT["Exit code 0 or 1"]

    style TUI fill:#9B59B6,color:#fff
```

### Low-Level Diagram

```mermaid
sequenceDiagram
    participant Eng as Engineer
    participant CLI as watch.go:newUpgradeWatchCmd()
    participant TUI as tui.Model
    participant PG15 as Source Pool (pg-old)
    participant PG16 as Target Pool (pg-new)
    participant Cmp as upgrade.Compare()

    Eng->>CLI: pgctl upgrade watch --source-dsn ... --target-dsn ...
    CLI->>CLI: Load queries.json
    CLI->>PG15: db.Connect(sourceDSN)
    CLI->>PG16: db.Connect(targetDSN)
    CLI->>TUI: NewModel(ctx, queries, sourcePool, targetPool, threshold)
    CLI->>TUI: tea.NewProgram(model).Run()

    loop For each query (sequential)
        TUI->>TUI: replayOneQueryCmd(index)
        TUI->>PG15: BEGIN → EXPLAIN ANALYZE query → ROLLBACK
        PG15-->>TUI: QueryResult (before)
        TUI->>PG16: BEGIN → EXPLAIN ANALYZE query → ROLLBACK
        PG16-->>TUI: QueryResult (after)
        TUI->>Cmp: Compare(beforeReport, afterReport, threshold)
        Cmp-->>TUI: Comparison
        TUI->>TUI: Update table with new row
        TUI->>TUI: Render progress bar
    end

    TUI-->>CLI: Model with all results
    CLI->>CLI: m.AnyRegression() → exit code
    CLI-->>Eng: Exit 0 (safe) or Exit 1 (regressions)
```

### Repo Files

| File | What it does |
|------|-------------|
| [`internal/tui/watch.go:56-63`](../internal/tui/watch.go) | `queryResultMsg` — Bubble Tea message carrying one comparison |
| [`internal/tui/watch.go:65-83`](../internal/tui/watch.go) | `Model` struct — the TUI state |
| [`internal/tui/watch.go:85-123`](../internal/tui/watch.go) | `NewModel()` — constructs the model with a styled 6-column table |
| [`internal/tui/watch.go:125-136`](../internal/tui/watch.go) | `Init()` — kicks off replay of the first query |
| [`internal/tui/watch.go:138-170`](../internal/tui/watch.go) | `replayOneQueryCmd()` — replays one query against both pools |
| [`internal/tui/watch.go:172-233`](../internal/tui/watch.go) | `Update()` — handles key presses, window resize, and incoming results |
| [`internal/tui/watch.go:289-319`](../internal/tui/watch.go) | `View()` — renders the full TUI with progress bar and table |
| [`internal/tui/watch.go:321-330`](../internal/tui/watch.go) | `progressBar()` — ASCII progress bar rendering |
| [`internal/cli/watch.go:19-82`](../internal/cli/watch.go) | `newUpgradeWatchCmd()` — CLI wiring, connects both pools, runs TUI |

### Design Pattern: Elm Architecture (Model-View-Update)

**Pattern:** The TUI follows the **Elm Architecture** (also called MVU or TEA — The Elm
Architecture):

```
Model (state) → View (render) → Update (handle events) → Model (new state) → ...
```

This is the architecture that Bubble Tea implements. Each incoming event (key press,
window resize, query result) produces a new model, which produces a new view. No
mutations, no callbacks, no event listeners — just a pure loop.

**Architectural principle:** *Unidirectional data flow.* State changes flow in one
direction: event → update → model → view. This makes the UI predictable and easy to
reason about, even with concurrent database operations.

**Tradeoffs:**

| Decision | Upside | Downside |
|----------|--------|----------|
| Sequential query replay (one at a time) | Great visual effect — user sees progress; easy to debug | Much slower than parallel replay; doesn't test concurrency |
| Bubble Tea + lipgloss | Production-quality TUI with colors, borders, responsive layout | Adds 3 dependencies (bubbles, bubbletea, lipgloss) for a feature that's optional |
| `AnyRegression()` sets exit code | Same CI integration as the `compare` command | The TUI is interactive — running it in CI is awkward (use `compare` instead) |
| Alt-screen mode (`tea.WithAltScreen()`) | Clean full-screen experience, restores terminal on exit | Can't scroll back to see output after exiting |

---

## Step 5: Cutover — "Switch to the new version"

### Plain English

**For the PM:** Once the comparison passes (exit code 0), the engineer flips a switch to
mark the new Postgres 16 as the "active" instance. If anything goes wrong in the next
few hours, they can flip it back with a single command.

**For the engineer:** The cutover mechanism writes a JSON state file recording which
instance is active and what was active before. In a real production setup, this step
would also reconfigure your connection proxy (PgBouncer, HAProxy) or update your app's
connection string.

### What Happens at Acme Fintech

```bash
# Comparison passed — safe to cut over
pgctl upgrade cutover --to new

# Oh no, users are reporting slowness after cutover
pgctl upgrade rollback
```

### High-Level Diagram

```mermaid
graph LR
    CMP["Compare passes<br/>(exit 0)"] --> CUT["pgctl upgrade cutover --to new"]
    CUT --> STATE["cutover-state.json<br/>active: new<br/>previous: old"]

    STATE --> |"Problem detected"| ROLL["pgctl upgrade rollback"]
    ROLL --> STATE2["cutover-state.json<br/>active: old<br/>previous: new"]

    style CMP fill:#2ECC71,color:#fff
    style CUT fill:#3498DB,color:#fff
    style ROLL fill:#E74C3C,color:#fff
```

### Low-Level Diagram — State Machine

```mermaid
stateDiagram-v2
    [*] --> OldActive: Initial state<br/>(file doesn't exist)

    OldActive: active = "old"<br/>previous = ""
    NewActive: active = "new"<br/>previous = "old"
    RolledBack: active = "old"<br/>previous = "new"

    OldActive --> NewActive: cutover --to new
    NewActive --> RolledBack: rollback
    RolledBack --> NewActive: cutover --to new
```

### Repo Files

| File | What it does |
|------|-------------|
| [`internal/upgrade/cutover.go:20-26`](../internal/upgrade/cutover.go) | `CutoverState` struct — active, previous, timestamp |
| [`internal/upgrade/cutover.go:28-41`](../internal/upgrade/cutover.go) | `ReadState()` — loads state from JSON, defaults to "old" if file missing |
| [`internal/upgrade/cutover.go:56-70`](../internal/upgrade/cutover.go) | `Cutover()` — validates target, records previous, writes new state |
| [`internal/upgrade/cutover.go:72-85`](../internal/upgrade/cutover.go) | `Rollback()` — swaps active and previous, errors if no history |
| [`internal/upgrade/cutover_test.go`](../internal/upgrade/cutover_test.go) | Tests: full lifecycle, invalid target rejection, rollback-without-history error |
| [`internal/cli/upgrade.go:194-232`](../internal/cli/upgrade.go) | `newUpgradeCutoverCmd()` + `newUpgradeRollbackCmd()` — CLI wiring |

### Design Pattern: State Machine with Single-Level Undo

**Pattern:** The cutover state is a **finite state machine** with exactly one level of
undo. The state file records `active` and `previous`, allowing exactly one rollback.

This is deliberately minimal. A real production system would need:
- Multi-level undo (rollback the rollback)
- Distributed consensus (multiple nodes need to agree on who's active)
- Connection draining (stop sending queries to the old instance gracefully)

**Architectural principle:** *Explicit state over implicit state.* The active instance is
recorded in a file, not inferred from "which container is running." This makes the state
inspectable, versionable, and testable.

**Tradeoffs:**

| Decision | Upside | Downside |
|----------|--------|----------|
| JSON state file | Simple, inspectable, works without any external system | Not a real proxy configuration — doesn't actually route connections |
| Single-level undo | Covers the 99% case (bad cutover → rollback) | Can't undo a rollback; can't handle "cutover to new, rollback, cutover again" cleanly |
| Validates target is "old" or "new" | Prevents typos like `--to production` | Hard-codes exactly two instances; can't extend to blue/green/canary with 3+ targets |
| Errors if no previous state on rollback | Prevents accidental rollback on a fresh cluster | Confusing error message if the state file was deleted |

---

## Step 6: Decommission — "Tear it all down"

### Plain English

**For the PM:** After the upgrade is confirmed successful and stable, the engineer tears
down the test environment. This removes all containers and their data volumes. It
requires an explicit `--yes` flag to prevent accidental destruction.

**For the engineer:** This runs `docker compose down -v`, which stops all containers and
removes named volumes. The `--yes` flag is a safety interlock.

### What Happens at Acme Fintech

```bash
# After a week of running Postgres 16 in production with no issues
pgctl decommission --yes
```

### Repo Files

| File | What it does |
|------|-------------|
| [`internal/cli/decommission.go:11-35`](../internal/cli/decommission.go) | `newDecommissionCmd()` — requires `--yes`, calls `dockerutil.Down()` |
| [`internal/dockerutil/compose.go:37-41`](../internal/dockerutil/compose.go) | `Down()` — runs `docker compose down -v` (removes volumes) |

### Design Pattern: Destructive Action Guard

**Pattern:** Irreversible operations require explicit confirmation (`--yes`). This is the
same pattern used by:
- `terraform destroy` (requires `-auto-approve` or interactive confirmation)
- `aws s3 rb --force`
- `kubectl delete namespace --force`

**Architectural principle:** *Make dangerous operations hard to do accidentally.*
The friction of typing `--yes` is small, but it prevents the catastrophic `↑ Enter`
(re-running the last command, which might have been `decommission`).

---

## End-to-End Flow: The Complete Pipeline

### For the PM: One Slide

```mermaid
graph TB
    subgraph "Day 1: Setup"
        A["1. Create cluster<br/>Spin up PG15 + PG16 side by side"]
    end

    subgraph "Day 1: Validate"
        B["2. Capture queries<br/>Snapshot top 50 from production"]
        C["3. Replay on both<br/>Get timing + plans from PG15 and PG16"]
        D["4. Compare<br/>Flag regressions > 20% or plan changes"]
    end

    subgraph "Day 1-2: Decide"
        E{"Any regressions?"}
        F["5a. Fix and re-test<br/>Add indexes, rewrite queries"]
        G["5b. Cutover<br/>Switch to PG16"]
    end

    subgraph "Day 2-7: Monitor"
        H["6. Monitor production<br/>Watch for issues"]
        I{"Issues found?"}
        J["7a. Rollback<br/>One command back to PG15"]
        K["7b. Decommission<br/>Remove test containers"]
    end

    A --> B --> C --> D --> E
    E -->|"Yes"| F
    F --> B
    E -->|"No"| G
    G --> H --> I
    I -->|"Yes"| J
    J --> B
    I -->|"No (after 1 week)"| K

    style A fill:#3498DB,color:#fff
    style G fill:#2ECC71,color:#fff
    style J fill:#E74C3C,color:#fff
    style K fill:#95A5A6,color:#fff
```

### For the Engineer: Architecture Layers

```mermaid
graph TB
    subgraph "Entry Point"
        MAIN["cmd/pgctl/main.go"]
    end

    subgraph "CLI Layer — Thin Adapters"
        ROOT["cli/root.go<br/>Command tree + flags"]
        CREATE["cli/create.go"]
        CONFIGURE["cli/configure.go"]
        SCALE["cli/scale.go"]
        DECOM["cli/decommission.go"]
        UPG["cli/upgrade.go<br/>capture | replay | compare | cutover | rollback"]
        WATCH["cli/watch.go"]
    end

    subgraph "Domain Layer — Core Logic"
        CAP["upgrade/capture.go<br/>Read pg_stat_statements"]
        REP["upgrade/replay.go<br/>EXPLAIN ANALYZE in rollback txns"]
        CMP["upgrade/compare.go<br/>Diff reports, flag regressions"]
        CUT["upgrade/cutover.go<br/>State machine for active instance"]
    end

    subgraph "Infrastructure Layer — External Systems"
        DB["db/db.go<br/>pgx connection pool"]
        DOCKER["dockerutil/compose.go<br/>Shell out to docker compose"]
        CFG["config/config.go<br/>YAML load/save/validate"]
    end

    subgraph "Presentation Layer"
        TUI["tui/watch.go<br/>Bubble Tea live dashboard"]
    end

    MAIN --> ROOT
    ROOT --> CREATE & CONFIGURE & SCALE & DECOM & UPG & WATCH

    CREATE --> CFG & DOCKER
    CONFIGURE --> CFG
    SCALE --> CFG & DOCKER
    DECOM --> DOCKER
    UPG --> CAP & REP & CMP & CUT & DB
    WATCH --> TUI & DB & REP & CMP
```

### For the Engineer: Package Dependency Diagram

```mermaid
graph TD
    CLI["internal/cli"] --> CONFIG["internal/config"]
    CLI --> DOCKER["internal/dockerutil"]
    CLI --> DB["internal/db"]
    CLI --> UPGRADE["internal/upgrade"]
    CLI --> TUI["internal/tui"]

    TUI --> UPGRADE
    TUI --> DB

    UPGRADE --> DB

    DB --> PGX["pgx/v5 (external)"]
    CLI --> COBRA["cobra (external)"]
    CONFIG --> YAML["yaml.v3 (external)"]
    TUI --> BTEA["bubbletea (external)"]
    TUI --> LIPGLOSS["lipgloss (external)"]
    TUI --> BUBBLES["bubbles (external)"]
    DOCKER --> EXEC["os/exec (stdlib)"]

    style CLI fill:#E67E22,color:#fff
    style UPGRADE fill:#2ECC71,color:#fff
    style DB fill:#3498DB,color:#fff
    style CONFIG fill:#9B59B6,color:#fff
    style TUI fill:#E74C3C,color:#fff
    style DOCKER fill:#1ABC9C,color:#fff
```

---

## Summary of Architectural Principles

| # | Principle | Where it's applied | Why it matters |
|---|-----------|-------------------|----------------|
| 1 | **Separation of description from execution** | `cluster.yaml` describes; CLI commands execute | Config can be reviewed, versioned, diffed without running anything |
| 2 | **Observer pattern** | Capture reads `pg_stat_statements` | Zero app changes needed to observe behavior |
| 3 | **Sandbox pattern** | Replay wraps queries in `BEGIN ... ROLLBACK` | Safely execute writes without mutating data |
| 4 | **Quality gate** | Compare returns exit code 0 or 1 | Binary pass/fail integrates with any CI/CD system |
| 5 | **Fail closed** | Missing queries are flagged as regressions | False positives are safe; false negatives cause outages |
| 6 | **Elm Architecture (MVU)** | TUI uses Bubble Tea's Model/Update/View | Unidirectional data flow makes UI predictable |
| 7 | **State machine with undo** | Cutover records active + previous | Explicit state enables reliable rollback |
| 8 | **Destructive action guard** | Decommission requires `--yes` | Prevents accidental data loss |
| 9 | **Thin adapter pattern** | CLI commands are thin wrappers over domain logic | Testable logic lives in packages, not in CLI wiring |
| 10 | **Layered architecture** | cmd → cli → domain → infrastructure | Each layer has a single responsibility; dependencies flow inward |

---

## Real-World Companies That Need This

| Company | Database Scale | Upgrade Pain Point | How pgctl Helps |
|---------|---------------|-------------------|-----------------|
| **Stripe** | Thousands of PG instances | Payment queries must never regress — even 10ms matters | Capture payment-critical queries, gate upgrade on latency + plan stability |
| **Shopify** | Multi-TB databases per merchant shard | Upgrade one shard at a time; need to know if it's safe before the next | Run capture/replay per shard, compare results, cut over shard by shard |
| **Robinhood** | Real-time financial data | SEC requires data accuracy; plan changes can cause stale reads | Plan-shape detection catches optimizer changes before they affect data freshness |
| **Instacart** | Geospatial queries (PostGIS) | PostGIS + new PG version = new spatial planner behavior | Replay spatial queries, detect plan changes that break delivery time estimates |
| **GitLab** | 14TB+ primary database | Upgrade affects every feature (issues, MRs, CI, packages) | Capture the top 500 queries, replay against candidate, gate on the full report |

---

## What's Not Built Yet (Known Gaps)

| Gap | Current State | What production needs |
|-----|--------------|----------------------|
| Real data replay | Queries replayed without bind parameters | Need parameter sniffing or synthetic data generation |
| Statistical significance | Single-run comparison, noise-prone | Multi-run replay with confidence intervals (see companion project BenchGate) |
| Connection routing | JSON state file, not a real proxy | Integrate with PgBouncer/HAProxy for actual traffic switching |
| Streaming replication | `pg-replica` is a standalone container | Real streaming replication to test replica promotion |
| Concurrent replay | Sequential, one query at a time | Parallel replay to test contention and locking behavior |
| Schema migration | Assumes identical schemas on both instances | Need `pg_dump`/`pg_restore` or logical replication to sync schemas |

---

*Document generated for the pgctl project. See the [architecture doc](architecture.md)
and [ADRs](adr/) for additional context.*
