# Bind migration recovery to durable payloads

- **Status:** Accepted
- **Date:** 2026-09-11
- **Source:** `edb33b7a01da55f84a997270e48f41c49b76e11f`

## Context

Operators need to migrate legacy records into a schema-2 workspace while
preserving source branches, workflow decisions, and abandoned changes.
Atomic file replacement alone leaves partial temporary files after a process
crash. Recovery cannot establish ownership from a filename or file mode.

## Decision

We will require a reviewed plan hash and journal the migration through the CLI.
Before private payload writes, we will persist a run identity and exact payload
descriptors using opaque symlinks as atomic metadata, without following them.
We will store payload blobs in the permission-restricted recovery directory and
validate their digests before replaying interrupted writes.

For incomplete initial payload preparation, we will regenerate expected bytes
from revalidated inputs. We will derive worktree ownership tokens with
domain-separated HMAC from the private run seed so replay preserves token
identity. We will reject altered bytes rather than delete matching filenames.

We considered requiring manual recovery for partial private writes. That would
leave the migration's resume/restore contract incomplete. We also rejected
replacing an operator's records with freshly created workflow states because
that would discard identity and historical decisions.

## Consequences

Operators must preserve the private recovery directory and use the migration
CLI after interruption. We retain recovery evidence through preparation
retirement and verify commit boundaries before recording completion.

This protocol adds private storage and validation code. We bound opaque-link
sizes and refuse unsupported layouts and platforms before mutation. We test
process-kill, partial-write, restoration, and tamper cases; those tests do not
establish durability under host power loss.
