# Default to trusted workspace shell

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

ADR 0051 trusted repository execution but retained a finite routine-command
allowlist. That still interrupted inspection, setup, cloning, Python/Node, and
shell composition while already allowing scripts that can do arbitrary work.
The user explicitly rejected extending that finite allowlist and approved a
loose trusted-workspace shell default for the coordinator and implementers.

## Decision

Use neutral `bash_default: allow` for those writable roles. The renderer accepts
`allow|ask` and a `bash_ask` list; allow omits generic composition asks. Absent
baselines retain custom definitions' previous guarded behavior. Known `git push`,
GitHub publication, and raw `gh api` patterns ask for the coordinator but are denied
for implementers, including read-only API payloads. Destructive patterns ask for
both writable roles. Protected asks follow base and exact additions; all denies,
including direct workflow bypass patterns, remain last. `bash_allow_add` cannot
override these protected exceptions. Known RTK wrapper asks and trusted-default
denies apply even without the proxy configured.
No model-route configuration knob is added; stricter users can select guarded
custom agent definitions.

The general Bash baseline deliberately overrides inherited Bash policy, including
asks/denies. Edit permissions, directory grants, delegation, and assigned write
scope do not change; concurrent reviewers and other read-only workers still deny
shell and edits. Implementers may perform task-authorized Git/gh setup and reads
within scope. The coordinator retains authoritative GitHub intake, workflow state,
registered worktree lifecycle, integration, and publication. Isolated authorized
task-local Git fixtures are allowed, not alternate workflow execution bindings or
recreations of the live control plane. Dirty-work and exact cleanup consent remain.

Shell execution permission is not publishing permission or a workflow-policy
waiver. Invocation scope, verification gates, role ownership, and explicit approval
of shown review drafts remain binding, including inside scripts and API payloads.
A tool prompt cannot override role ownership or publication approval.
Past accepted commands and private command history are not grants; redacted
placeholders are not valid exact additions. Shared policy remains usable by
standalone onto/to without requiring h or a new optional dependency.

Required-binary failures trigger PATH/known-compatible-installation inspection,
then task-authorized setup repair from trusted sources/versions into workspace-local
destinations. No silent global binary overwrite, shell-profile edits, or untrusted
installation. Missing authority or failed repair yields a factual setup blocker or
specific decision request; version and framework-install gates still precede all
workflow mutations, with no handwritten bookkeeping fallback.

## Consequences

Routine trusted work proceeds without expanding an allowlist or repeatedly
approving composition. Checked-out contributor code can use the process's files,
credentials, and network. Finite protected patterns cover known command forms,
not every possible risky tool, flag ordering, script, or wrapper. Host command
parsing and directory matching cannot sandbox arbitrary shell execution or
guarantee a prompt for hidden publication/destruction. This is an accepted trust
tradeoff, not an injection boundary. Agents must still honor actual prompts and
denials, inspect relevance, and refuse unauthorized effects; an allowed command
cannot authorize bypassing workflow policy.
