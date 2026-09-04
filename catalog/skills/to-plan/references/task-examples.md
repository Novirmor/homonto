# Task contract examples

Use these examples when drafting a new `plan.md` or repairing a task that an
implementer could not execute from cold context.

## Complete shape

```markdown
# Goal

Reject duplicate catalog resource names during expansion instead of allowing
the later declaration to win. Keep the change inside catalog validation; do not
alter adapter merge behavior.

- [ ] Reject duplicate expanded skill names
  - Files: `internal/catalog/expand.go`, `internal/catalog/expand_test.go` (`Expand`)
  - Change: return an error naming both the duplicate skill and its framework; preserve expansion order for unique names
  - Verify: `go test ./internal/catalog -run TestExpandRejectsDuplicateSkills` — passes with the duplicate-name assertion

- [ ] Document the validation error
   - Files: `<workflow-root>/guides/configuration.md` (framework validation)
  - Change: state that duplicate expanded names fail before projection and include the emitted error shape
   - Verify: `git diff --check -- <workflow-root>/guides/configuration.md` — exits 0

Final Verify: `go test ./internal/catalog` — all catalog tests pass
```

## Vague task

Bad:

```markdown
- [ ] Improve validation and handle edge cases
```

It names neither the behavior nor the proof. The implementer must invent the
scope.

Better:

```markdown
- [ ] Reject an empty remote archive after download
  - Files: `internal/remote/fetch.go`, `internal/remote/fetch_test.go` (`Fetch`)
  - Change: return `remote: archive is empty` before extraction; preserve the cache on this failure
  - Verify: `go test ./internal/remote -run TestFetchRejectsEmptyArchive` — passes and asserts the cache remains unchanged
```

## Code and tests split apart

Bad:

```markdown
- [ ] Implement empty-archive validation
- [ ] Add tests
```

The first task can land unproved, and the second does not name a behavior.

Better: keep implementation and its focused regression test in the same task,
as in the empty-archive example above.

## Investigation left for `do`

Bad:

```markdown
- [ ] Investigate why status is wrong
```

Resolve the question during `plan`, record the grounded conclusion under
`## Notes`, and only then write the implementation task. Do not make the
implementer settle an upstream design question:

```markdown
- [ ] Classify pending state from desired-versus-applied hashes
  - Files: `internal/engine/status.go`, `internal/engine/status_test.go` (`Status`)
  - Change: report pending only when desired differs from recorded applied state; preserve disk-versus-applied drift classification
  - Verify: `go test ./internal/engine -run TestStatusSeparatesPendingFromDrift` — passes for pending-only and drift-only cases
```

## Design memory

Add a documentation task only when behavior changes a durable promise.

```markdown
- [ ] Record the new adapter boundary
   - Files: `<workflow-root>/adr/0014-adapter-contract.md`, `<workflow-root>/guides/projection-and-state.md`
  - Change: update the durable adapter responsibility and the user-visible consequence; do not restate transient implementation details
   - Verify: `git diff --check -- <workflow-root>/adr/0014-adapter-contract.md <workflow-root>/guides/projection-and-state.md` — exits 0
```

Do not add an ADR task for a local rename, test refactor, or implementation
detail that leaves existing contracts unchanged.
