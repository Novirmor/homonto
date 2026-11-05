# Architecture Decision Records

`docs/adr/` holds accepted and superseded decisions, one per file:
`NNNN-<slug>.md`. It is decision history: what was decided and why, kept
after a decision is superseded.

Workflow artifacts are not committed
([ADR 0017](0017-stop-committing-workflow-artifacts.md)), so an ADR is the
durable record a decision leaves behind. When one is owed, the format, and
numbering: [`../agents/adr.md`](../agents/adr.md).

- The next number is the highest in this directory plus one, assigned when
  the ADR is written. Numbers are never reused.
- A superseded ADR keeps its file and its text. Only its Status changes, to
  `Superseded by NNNN` or `Partially superseded by NNNN (what)`.
- ADRs 0001–0016 carry a `**Change:**` field naming the OpenSpec change that
  produced them. It is kept as history and not used for new ADRs.

## Index

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-plan-confirm-apply-pipeline.md) | Adopt a terraform-style plan/confirm/apply pipeline with tool adapters | Accepted |
| [0002](0002-secrets-referenced-never-stored.md) | Reference secrets by token; never store or print plaintext | Accepted |
| [0003](0003-owned-content-symlinked-surgical-merge.md) | Symlink owned content; merge unowned keys surgically; never clobber | Accepted |
| [0004](0004-atomic-writes-state-last.md) | Write files atomically and persist state after successful adapter writes | Accepted |
| [0005](0005-adopt-onto-workflow.md) | Adopt onto: a self-contained markdown development workflow | Superseded by 0012 |
| [0006](0006-reference-file-skill-architecture.md) | Use bundled reference files for skill payloads (progressive disclosure) | Accepted |
| [0007](0007-adversarial-multi-agent-verification.md) | Verify with adversarial fresh-context skeptic agents | Accepted |
| [0008](0008-preflight-warns-not-halts.md) | Make the tooling preflight warn and proceed, never halt | Accepted |
| [0009](0009-adopt-preexisting-resources-into-state.md) | Adopt pre-existing matching resources into state via a silent apply-time action | Accepted |
| [0010](0010-drift-from-disk-vs-state.md) | Compute drift from disk-vs-state, not from the desired plan | Accepted |
| [0011](0011-adopt-skill-install-scope.md) | Adopt a configurable skill install scope (user vs project) | Superseded |
| [0012](0012-readopt-comet-openspec-workflow.md) | Re-adopt Comet + OpenSpec + Superpowers as the development workflow | Superseded by 0023 |
| [0013](0013-remote-source-trust-boundary.md) | Establish a pinned, fail-closed remote-source trust boundary | Accepted |
| [0014](0014-adapter-contract.md) | Adopt a format-agnostic adapter contract for tool projection | Accepted |
| [0015](0015-ship-only-onto-frameworks.md) | Ship only homonto-native frameworks (onto, and later `to`) | Accepted |
| [0016](0016-remove-model-tiers.md) | Declare subagent models explicitly; drop model tiers | Partially superseded by 0099 |
| [0017](0017-stop-committing-workflow-artifacts.md) | Stop committing workflow artifacts; the ADR is the record | Accepted |
| [0018](0018-tasks-md-is-the-single-checkoff.md) | Make tasks.md onto's single checkoff; plan.md carries detail only | Accepted |
| [0019](0019-parallelism-follows-write-scope.md) | Gate subagent parallelism on write-scope, not on framework | Superseded by 0035 |
| [0020](0020-onto-parallel-implementers-are-supported.md) | Apply write-scope parallelism across onto's phases; support parallel implementers | Accepted |
| [0021](0021-onto-is-for-handoff-and-audit.md) | onto's axis is handoff and audit, not "enterprise" | Accepted |
| [0022](0022-tooling-providers-are-declared-not-shipped.md) | Tooling providers are declared in config, not named in shipped prose | Accepted |
| [0023](0023-develop-directly-without-comet.md) | Develop directly, without Comet | Accepted |
| [0024](0024-multi-repo-designated-state-cross-repo-effect.md) | Multi-repo support — designated state, cross-repo effect | Partially superseded by 0085 |
| [0025](0025-state-provenance-and-history.md) | Record resource provenance and last-change in state | Accepted |
| [0026](0026-relative-project-links.md) | Use relative targets for same-move project links | Accepted |
| [0027](0027-versioned-workflow-sidecars.md) | Keep workflow records in versioned sidecars; JSON handoffs are metadata-only | Accepted |
| [0028](0028-to-promote-into-onto.md) | Promote a `to` change into onto | Accepted |
| [0029](0029-ephemeral-permission-suggestions.md) | Permission suggestions are ephemeral; nothing learned is persisted | Accepted |
| [0030](0030-opt-in-transactional-snapshots.md) | Opt-in transactional snapshots with semantic checkpoints | Accepted |
| [0031](0031-parallel-primary-workflow-agents.md) | Give each workflow its own primary agent | Accepted |
| [0032](0032-audit-explicit-workflow-bypasses.md) | Audit explicit workflow bypasses | Superseded by 0034 |
| [0033](0033-run-workflows-to-completion-by-default.md) | Run workflows to completion by default | Accepted |
| [0034](0034-keep-bypasses-command-only.md) | Keep workflow bypasses command-only | Accepted |
| [0035](0035-deny-shell-access-to-concurrent-specialists.md) | Deny shell access to concurrent specialists | Accepted |
| [0036](0036-separate-the-diff-base-from-the-integration-branch.md) | Separate the diff base from the integration branch | Accepted |
| [0037](0037-receipt-close-boundaries.md) | Receipt destructive close boundaries | Accepted |
| [0038](0038-bind-close-to-the-verified-tree.md) | Bind close to the verified tree | Accepted |
| [0039](0039-grant-workflow-access-only-to-declared-repositories.md) | Grant workflow access only to declared repositories | Accepted |
| [0040](0040-configure-one-workflow-root.md) | Configure one workflow root | Accepted |
| [0041](0041-ship-an-interactive-release-installer.md) | Ship an interactive release installer | Accepted |
| [0042](0042-onto-and-to-are-complementary.md) | Make onto and to complementary | Accepted |
| [0043](0043-the-installer-offers-initialization.md) | The installer offers initialization | Accepted |
| [0044](0044-the-installer-configures-new-projects.md) | The installer configures new projects | Accepted |
| [0045](0045-one-homonto-coordinator-for-both-workflows.md) | One homonto coordinator for both workflows | Accepted |
| [0046](0046-integrate-onto-pr-continuations-into-the-pr-head.md) | Integrate onto PR continuations into the PR head | Accepted |
| [0047](0047-deny-open-web-and-compound-shell-around-github-intake.md) | Deny open-web and compound-shell capability around GitHub intake | Partially superseded by 0049, 0051, 0054, 0055 |
| [0048](0048-one-declared-workspace-tmp-directory.md) | One declared workspace tmp directory | Partially superseded by 0086 |
| [0049](0049-permit-routine-workflow-evidence-recording.md) | Permit routine workflow evidence recording | Partially superseded by 0055 |
| [0050](0050-observe-workflow-state-through-opencode.md) | Observe workflow state through OpenCode | Accepted |
| [0051](0051-trust-workspace-execution-and-automate-h-routing.md) | Trust workspace execution and automate h routing | Partially superseded by 0054, 0059, 0062 |
| [0052](0052-separate-workflow-history-from-source-worktrees.md) | Separate workflow history from source worktrees | Accepted |
| [0053](0053-keep-history-without-blocking-fresh-verification.md) | Keep history without blocking fresh verification | Accepted |
| [0054](0054-default-to-trusted-workspace-shell.md) | Default to trusted workspace shell | Partially superseded by 0055, 0056, 0057, 0059, 0070 |
| [0055](0055-require-confirmation-for-explicit-workflow-bypasses.md) | Require confirmation for explicit workflow bypasses | Accepted |
| [0056](0056-allow-coordinator-local-git-operations.md) | Allow coordinator local Git operations | Partially superseded by 0070 |
| [0057](0057-allow-coordinator-rtk-commands.md) | Allow coordinator RTK commands | Partially superseded by 0070 |
| [0058](0058-bind-migration-recovery-to-durable-payloads.md) | Bind migration recovery to durable payloads | Accepted |
| [0059](0059-authorize-github-drafts-by-session-and-approval.md) | Authorize GitHub drafts by session and approval | Accepted |
| [0060](0060-raise-github-draft-runtime-capacities.md) | Raise GitHub draft runtime capacities | Accepted |
| [0061](0061-return-skeptic-findings-to-implementation.md) | Return skeptic findings to implementation | Accepted |
| [0062](0062-ask-once-before-unselected-workflow-creation.md) | Ask once before creating an unselected workflow change | Accepted |
| [0063](0063-centralize-workflow-policy-and-preserve-preset-repairs.md) | Centralize workflow policy and preserve preset repairs | Accepted |
| [0064](0064-deliver-continuation-policy-through-model-context.md) | Deliver continuation policy through model context | Accepted |
| [0065](0065-resolve-no-delta-scenarios-for-every-workflow.md) | Resolve no-delta scenarios for every workflow | Accepted |
| [0066](0066-ground-development-in-codegraph.md) | Ground development in CodeGraph | Accepted |
| [0067](0067-separate-workflows-from-engines.md) | Separate workflows from engines | Accepted |
| [0068](0068-coordinate-parallel-work-by-repository.md) | Coordinate parallel work by repository | Accepted |
| [0069](0069-drive-onto-to-the-requested-delivery-endpoint.md) | Drive onto to the requested delivery endpoint | Accepted |
| [0070](0070-trust-authorized-coordinator-delivery.md) | Trust authorized coordinator delivery | Accepted |
| [0071](0071-align-entrypoints-and-delivery-recovery.md) | Align entrypoints and delivery recovery | Accepted |
| [0072](0072-release-integrated-task-branches.md) | Release integrated workspace task branches | Accepted |
| [0073](0073-require-approval-for-disproportionate-repairs.md) | Require approval for disproportionate repairs | Accepted |
| [0074](0074-use-explorers-before-guessing.md) | Use explorers before guessing | Accepted |
| [0075](0075-own-terminal-cleanup-in-the-binaries.md) | Own terminal cleanup in the binaries | Partially superseded by 0086 |
| [0076](0076-separate-conflict-only-pr-resolution.md) | Separate conflict-only PR resolution | Accepted |
| [0077](0077-hand-worker-permission-blocks-to-the-coordinator.md) | Hand worker permission blocks to the coordinator | Accepted |
| [0078](0078-merge-onto-presets-into-light.md) | Merge onto fix and tweak into onto light | Accepted |
| [0079](0079-drop-the-opencode-1-workflow-plugin.md) | Drop the OpenCode 1 workflow plugin | Accepted |
| [0080](0080-freeze-workspace-migration.md) | Freeze workspace migration | Superseded by 0081 |
| [0081](0081-support-only-the-live-version.md) | Support only the live version | Accepted |
| [0082](0082-support-harnesses-natively-in-tiers.md) | Support harnesses natively, in tiers | Accepted |
| [0083](0083-pull-the-chosen-base-before-every-change.md) | Pull the chosen base before every change | Accepted |
| [0084](0084-allow-draft-prs-at-any-phase.md) | Allow draft PRs at any phase | Accepted |
| [0085](0085-bring-your-own-mcp-servers-and-skills.md) | Bring your own MCP servers and skills | Accepted |
| [0086](0086-clean-up-at-close.md) | Clean up at close | Accepted |
| [0087](0087-provision-dev-tools-with-mise.md) | Provision dev tools with mise | Accepted |
| [0088](0088-module-path-matches-canonical-repository.md) | Name the Go module after the canonical repository | Accepted |
| [0089](0089-name-hosting-workflows-per-host-with-shared-processes.md) | Name hosting workflows per host, with shared process names | Accepted |
| [0090](0090-move-the-draft-publication-core-into-the-binary.md) | Move the draft/publication core into the binary | Accepted |
| [0091](0091-own-the-bitbucket-cloud-transport.md) | Own the Bitbucket Cloud transport | Accepted |
| [0092](0092-publish-the-package-contract.md) | Publish the package contract | Accepted |
| [0093](0093-describe-engines-instead-of-naming-them.md) | Describe engines instead of naming them | Accepted |
| [0094](0094-package-host-services-separately.md) | Package host services separately | Accepted |
| [0095](0095-declare-package-settings-in-the-manifest.md) | Declare package settings in the manifest | Accepted |
| [0096](0096-render-external-agents-by-intent.md) | Render external agents by intent | Accepted |
| [0097](0097-require-harness-capabilities-by-name.md) | Require harness capabilities by name | Accepted |
| [0098](0098-reserve-routing-and-decision-surfaces.md) | Reserve routing and decision surfaces | Partially superseded by 0099, 0101 |
| [0099](0099-route-models-by-policy.md) | Route models by policy | Accepted |
| [0100](0100-select-models-automatically-at-plan.md) | Select models automatically at plan | Accepted |
| [0101](0101-deliver-typed-decisions-through-the-binary.md) | Deliver typed decisions through the binary | Accepted |
| [0102](0102-classify-and-record-route-and-decision-outcomes.md) | Classify and record route and decision outcomes | Accepted |
| [0103](0103-serve-the-runtime-core-to-every-harness.md) | Serve the runtime core to every harness | Accepted |
| [0104](0104-project-claude-code-natively.md) | Project Claude Code natively | Accepted |
| [0105](0105-accept-limited-claude-code-capabilities-per-guarantee.md) | Accept limited Claude Code capabilities per guarantee | Accepted |
| [0106](0106-let-external-engines-own-lifecycle-records.md) | Let external engines own lifecycle records | Accepted |
