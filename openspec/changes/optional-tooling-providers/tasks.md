## 1. Config surface

- [ ] 1.1 `internal/config`: add the `Tooling` struct with `ShellProxy` and
      `CodeIntel`, decoded from a `[tooling]` table, with unknown keys rejected.
- [ ] 1.2 `internal/config/validate.go`: validate both keys against their
      closed value sets, failing with an error that names the offending key and
      lists the accepted values.
- [ ] 1.3 Resolve an absent table or an omitted key to `none`; add tests
      covering absent, partial, full, and invalid configs.

## 2. Provider content

- [ ] 2.1 Create `catalog/tooling/` fragments: `rtk.md`, `graphify.md`,
      `okf.md`, and the `none` fallback text.
- [ ] 2.2 Move the existing rtk and graphify preflight prose out of
      `catalog/skills/onto/SKILL.md` into the fragments verbatim, so no
      instruction is lost in the move.
- [ ] 2.3 Write the okf fragment (index/query/staleness guidance mirroring the
      graphify fragment's shape).
- [ ] 2.4 Wire `catalog/tooling/` into the embedded catalog FS and into
      `ContentFingerprint`'s digested set.

## 3. Sidecar generation

- [ ] 3.1 `internal/catalog`: add the renderer that selects the two declared
      fragments and writes `references/tooling.md` under a generated header.
- [ ] 3.2 Write the sidecar for dispatcher skills during `Materialize`,
      inside the existing stage-then-swap so a crash cannot leave it partial.
- [ ] 3.3 Golden-file tests: both-none, rtk+graphify, rtk+okf, none+okf —
      asserting the undeclared provider's name never appears.

## 4. Materialize gating

- [ ] 4.1 `internal/engine`: compute `toolingFP` and append it to the
      composite fingerprint.
- [ ] 4.2 Extend the presence check so a deleted `references/tooling.md`
      forces re-materialization.
- [ ] 4.3 Tests: editing `[tooling]` re-renders; an unchanged config is a
      no-op; deleting the sidecar restores it.

## 5. Neutralize shipped catalog prose

- [ ] 5.1 `catalog/skills/onto/SKILL.md`: replace the inline rtk and graphify
      preflight steps with a pointer to `references/tooling.md`.
- [ ] 5.2 `catalog/skills/onto-open/SKILL.md` and its `references/notes.md`
      and `references/proposal.md`: provider-neutral grounding wording.
- [ ] 5.3 `catalog/skills/onto-design/SKILL.md` and its
      `references/brainstorm-protocol.md` and `references/design.md`: same.
- [ ] 5.4 `catalog/skills/onto-close/references/lint-checklist.md`,
      `catalog/commands/onto.md`, `catalog/subagents/onto-explorer.md`,
      `catalog/subagents/to-explorer.md`: same.
- [ ] 5.5 The `to` dispatcher gains the same preflight pointer.
- [ ] 5.6 Add a test asserting no provider name appears in shipped catalog
      content outside `catalog/tooling/`.

## 6. Docs and versioning

- [ ] 6.1 `docs/guides/configuration.md`: document the `[tooling]` table, the
      closed value sets, and the `none` defaults.
- [ ] 6.2 `docs/guides/onto-workflow.md` and `docs/guides/to-workflow.md`:
      replace the named-tool preflight prose with the provider model.
- [ ] 6.3 New ADR recording provider-neutrality and the referenced-not-vendored
      rule, superseding the tool-naming parts of ADR 0005 and ADR 0008.
- [ ] 6.4 `docs/release-notes.md`: the behavior change plus the config snippet
      that restores rtk and graphify.
- [ ] 6.5 Bump `catalog/version.txt` and both framework versions.

## 7. Verification

- [ ] 7.1 `go build ./... && go vet ./...` clean; `go test ./...` green.
- [ ] 7.2 Extend the onto and to Docker E2E suites to assert the sidecar is
      written and matches the declared providers.
- [ ] 7.3 `./scripts/gate.sh` green.
