# ADR 0002: A naive percentage threshold for regression detection (and why that's a known gap)

## Context

`pgctl upgrade compare` needs to decide whether a candidate version is safe
to cut over to. The obvious approach — "candidate is more than X% slower than
source, fail" — is also a well-known way to lie to yourself: a single
`EXPLAIN ANALYZE` run is noisy (page cache state, host contention, CPU
throttling), so a naive threshold produces both false positives (a cold-cache
run looks like a regression) and false negatives (a real regression hides
inside run-to-run noise).

## Decision

Ship the naive single-run percentage threshold anyway, but document its
limits explicitly rather than pretend it's rigorous. `Compare()` in
`internal/upgrade/compare.go` also flags a **plan shape change** (e.g. Index
Scan → Seq Scan) as a regression independent of the latency delta, since a
plan change is a much stronger and lower-noise signal than a raw timing
comparison — often it's the actual explanation for a latency shift, not a
coincidence alongside it.

## Consequences

- `pgctl` alone is not a trustworthy regression gate for anything but the
  most obvious upgrade breakages. It proves the upgrade *mechanics* (capture,
  replay, compare, cutover, rollback) are sound end-to-end, which is its
  actual job.
- Real variance-aware regression detection — multiple runs, warmup discard,
  a Mann-Whitney U test or confidence-interval overlap instead of a single
  percentage — is a distinct, harder problem. That problem is deliberately
  factored out into a **separate companion project, BenchGate**, rather than
  bolted onto pgctl. Conflating "can we safely execute an upgrade" with
  "is our statistical confidence in a regression call correct" would have
  doubled this project's scope for a benefit orthogonal to its actual
  purpose.

## Alternatives considered

- **Build the statistical gate into pgctl directly.** Rejected: it's a
  legitimately separate concern (regression *detection* vs. upgrade
  *execution*), and BenchGate already exists as the right home for it.
- **Do nothing beyond raw latency comparison.** Rejected: the plan-shape
  check is nearly free to add and catches a common, high-signal class of
  regression (index not used post-upgrade) that a latency threshold alone
  can miss on a lightly loaded local Docker instance.
