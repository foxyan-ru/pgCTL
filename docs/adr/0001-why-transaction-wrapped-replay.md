# ADR 0001: Replay via `EXPLAIN (ANALYZE, ...)` inside a rolled-back transaction

## Context

To validate a major-version upgrade, pgctl needs to replay captured
production-shaped traffic — including writes — against a candidate instance
without corrupting its state or requiring a disposable dataset per run.

## Decision

Every captured query is replayed as `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
<query>` inside a transaction that is **always** rolled back, regardless of
success or failure. `EXPLAIN ANALYZE` actually executes the query and reports
real timings and the real plan Postgres chose, but wrapping it in a
transaction we never commit means an `UPDATE`/`INSERT`/`DELETE` captured from
production can be safely replayed as many times as we want, against the same
target, without accumulating side effects.

## Consequences

- We get real execution timings and real plan shapes, not estimates.
- Replay is idempotent and safe to re-run.
- **Limitation:** anything that isn't rolled back by a `ROLLBACK` — a
  `NOTIFY`, a call out to an external webhook from a trigger, a
  sequence's `nextval()` advancing — is *not* undone. For a portfolio-scoped
  demo this is an accepted and documented gap, not something pgctl claims to
  solve.
- **Limitation:** multi-statement transactions from the source application
  are captured by `pg_stat_statements` as individual statements, so
  cross-statement consistency (e.g. a read that depends on an earlier write
  in the same transaction) isn't replayed as a unit. A more complete
  implementation would capture at the transaction level, e.g. via
  `pg_stat_activity` sampling or a proxy — out of scope here.

## Alternatives considered

- **Replay against a disposable copy of the database per run.** More
  faithful, but expensive to set up for every replay and out of proportion
  for a local demo tool. A "real" version of pgctl would likely do this via
  a fast filesystem snapshot (e.g. ZFS/Btrfs) rather than a full `pg_dump`/
  `pg_restore` cycle.
- **Replay against a read replica.** Doesn't work for captured writes at all
  without a promotable copy, which is effectively the same cost as the
  option above.
