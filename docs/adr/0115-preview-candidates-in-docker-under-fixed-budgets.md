# Preview candidates in Docker under fixed budgets

- **Status:** Accepted
- **Date:** 2026-11-06

## Context

Users compare prototypes by running them. Three candidates on one machine
need CPU, memory, port and time limits, and teardown that holds when the
starting command is interrupted or killed. A bare host process cannot be held
to a CPU or memory limit without cgroups, and its grandchildren escape a
process-group kill once the parent is gone.

## Decision

- A preview is a `[preview.<name>]` declaration: a repository, either a
  Dockerfile built from the candidate's checkout or a stock image with the
  checkout mounted read-only at `/workspace`, the container port it serves
  HTTP on, and budgets. Docker is the only runtime.
- Every container runs with `--cpus`, `--memory` (swap capped at the same
  value), `--pids-limit`, `no-new-privileges`, and one port Docker publishes
  on a free loopback port. Defaults are 1 CPU, 512m, 256 processes, 30
  minutes; config cannot exceed 8 CPUs, 8g, 4096 processes or 24 hours.
- Start records intent (container name, generation, deadline) before Docker
  runs, labels the container with the swarm id, candidate, generation and
  deadline, and tears it down on build, run, readiness or interrupt failure.
- The time budget is enforced three ways: a detached watchdog (`swarm preview
  reap --until`), every swarm command reconciling the record first, and
  cleanup. Reconciliation removes expired previews, previews whose start was
  interrupted, and any labelled container the record does not own.
- Each preview's watchdog is armed before Docker starts; if it cannot start,
  the preview fails closed. Startup is bounded by its own deadline and by any
  earlier running preview's deadline, so the startup lock cannot delay reaping.
- Cleanup refuses to remove worktrees while a recorded preview cannot be torn
  down (Docker unreachable). A swarm with no recorded preview never needs
  Docker.

We rejected a host-process preview kind: it could honour port and time
budgets but not CPU or memory ones.

## Consequences

Previews need Docker, and each candidate needs a Dockerfile or a runnable
stock image. The image build is bounded by the startup budget, not by CPU or
memory limits. A preview can outlive its deadline when the watchdog dies and
no swarm command runs; the deadline label still marks it for the next one.
Built images are removed with their container, so a restart rebuilds.
