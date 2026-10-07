# ADR 0003: Imperative CLI now, Kubernetes operator explicitly out of scope

## Context

Postgres lifecycle automation is commonly built two ways: an imperative tool
you run on demand (a CLI, a script, a runbook), or a Kubernetes operator that
continuously reconciles a desired-state CRD. Both are legitimate; a portfolio
project has to pick one to build deep rather than spread thin across both.

## Decision

pgctl is an imperative CLI wrapping Docker Compose. A Kubernetes operator is
**not** built as part of this project. If a reconciliation-loop demo is
wanted later, it belongs in a separate, narrowly-scoped companion project —
just the `PostgresCluster` CRD and its reconcile loop for create/scale, not a
competitor to Patroni or a full HA platform.

## When each is the right choice

- **Imperative (this project):** you want a human or CI job to trigger a
  specific, auditable action at a specific time — exactly the shape of "run
  this upgrade validation, then decide" that pgctl's flagship feature needs.
  Simpler to reason about, test, and demo end-to-end solo.
- **Operator:** you want the system to continuously enforce desired state
  without a human in the loop — e.g. "always have 3 replicas, self-heal a
  crashed pod." The right model for a multi-tenant Postgres-as-a-service
  platform, but its value only shows up at a scale/duration a local demo
  can't really exercise, and it adds an entire second runtime (a K8s
  controller) to build and explain.

## Consequences

- pgctl's `create`/`scale`/`decommission` commands stay honestly thin — they
  demonstrate the lifecycle vocabulary, not a production orchestration
  system.
- The README states this tradeoff explicitly rather than implying pgctl is a
  scaled-down operator.
