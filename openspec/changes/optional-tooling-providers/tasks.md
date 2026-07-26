## 1. Config surface

- [x] 1.1 `internal/config`: add the `Tooling` struct with `ShellProxy` and
      `CodeIntel`, decoded from a `[tooling]` table, with unknown keys rejected.
- [x] 1.2 `internal/config/validate.go`: validate both keys against their
      closed value sets, failing with an error that names the offending key and
      lists the accepted values.
- [x] 1.3 Resolve an absent table or an omitted key to `none`; add tests
      covering absent, partial, full, and invalid configs.

## 2. Provider content

- [x] 2.1 Create `catalog/tooling/` fragments: `rtk.md`, `graphify.md`,
      `okf.md`, and the two `none` fallbacks (shell-proxy and code-intel say
      different things, so they are separate fragments).
- [x] 2.2 Copy the existing rtk and graphify preflight prose from
      `catalog/skills/onto/SKILL.md` into the fragments verbatim, so no
      instruction is lost. Deleting it from SKILL.md is task 5.1, kept separate
      so this reviews as a pure move.
- [x] 2.3 Write the okf fragment (index/query/staleness guidance mirroring the
      graphify fragment's shape).
- [x] 2.4 Wire `catalog/tooling/` into the embedded catalog FS. The fragment
      bytes are digested by `ToolingFingerprint`, **not** `ContentFingerprint`
      — the latter digests per named resource and fragments are not a declared
      resource; digesting only the selected pair also avoids churn when an
      unselected fragment is edited.

## 3. Sidecar generation

- [x] 3.1 `internal/catalog`: add the renderer that selects the two declared
      fragments and writes `references/tooling.md` under a generated header.
- [x] 3.2 Write the sidecar for dispatcher skills during `Materialize`,
      inside the existing stage-then-swap so a crash cannot leave it partial.
      Dispatchers are identified by `IsDispatcher` (skill named after its own
      framework), so nothing has to be threaded in from the engine.
- [x] 3.3 Assertion-based tests over all six combinations, each asserting the
      undeclared provider's name never appears, plus determinism. Preferred
      over golden files: goldens over prose fragments break on every wording
      edit without catching anything these assertions miss.

## 4. Materialize gating

- [x] 4.1 `internal/engine`: compute `toolingFP` and append it to the
      composite fingerprint.
- [x] 4.2 Extend the presence check so a deleted `references/tooling.md`
      forces re-materialization.
- [x] 4.3 Tests: editing `[tooling]` re-renders; deleting the sidecar restores
      it; an absent `[tooling]` table still writes a provider-free reference.

## 5. Neutralize shipped catalog prose

- [x] 5.1 `catalog/skills/onto/SKILL.md`: replace the inline rtk and graphify
      preflight steps with a pointer to `references/tooling.md`.
- [x] 5.2 `catalog/skills/onto-open/SKILL.md` and its `references/notes.md`
      and `references/proposal.md`: provider-neutral grounding wording.
- [x] 5.3 `catalog/skills/onto-design/SKILL.md` and its
      `references/brainstorm-protocol.md` and `references/design.md`: same.
- [x] 5.4 `catalog/skills/onto-close/references/lint-checklist.md`,
      `catalog/commands/onto.md`, `catalog/subagents/onto-explorer.md`,
      `catalog/subagents/to-explorer.md`: same.
- [x] 5.5 The `to` dispatcher gains the same preflight pointer.
- [x] 5.6 Add a test asserting no provider name appears in shipped catalog
      content outside `catalog/tooling/`.

## 6. Docs and versioning

- [x] 6.1 `docs/guides/configuration.md`: document the `[tooling]` table, the
      closed value sets, and the `none` defaults.
- [x] 6.2 `docs/guides/onto-workflow.md` and `docs/guides/to-workflow.md`:
      replace the named-tool preflight prose with the provider model.
- [x] 6.3 New ADR recording provider-neutrality and the referenced-not-vendored
      rule, superseding the tool-naming parts of ADR 0005 and ADR 0008. Staged
      unnumbered at `adr/` inside this change with `Status: Proposed`, per the
      staging rule in `docs/adr/README.md`; the number is assigned at archive.
- [x] 6.4 `docs/release-notes.md`: the behavior change plus the config snippet
      that restores rtk and graphify.
- [x] 6.5 Bump `catalog/version.txt` and both framework versions.

## 7. Verification

> Runs last, after group 8. Groups are never renumbered, so 8 was appended
> rather than inserted.

- [x] 7.1 `go build ./... && go vet ./...` clean; `go test ./...` green.
- [x] 7.2 Extend the onto and to Docker E2E suites to assert the sidecar is
      written and matches the declared providers.
- [x] 7.3 `./scripts/gate.sh` green.

## 8. Doctor findings

Appended after the design gate resolved open question 1 ("should doctor report
a declared-but-absent provider?") as yes, warning-level. Design section 7.

- [x] 8.1 `doctor`: warning-level finding when `[tooling]` declares a provider
      that is not present. Detection is `exec.LookPath` and file existence
      only — nothing is executed, so the v0.7.0 exec-timeout concerns do not
      apply.
- [x] 8.2 Tests: declared-and-absent yields a finding; declared-and-present
      and `none` yield none; the finding never fails a projection.
