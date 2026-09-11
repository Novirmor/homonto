# Deny open-web and compound-shell capability around GitHub intake

- **Status:** Partially superseded by 0049 (evidence setters), 0051 (web and workspace execution), 0054 (default shell baseline and generic composition asks; coordinator publication asks and implementer publication denies retained), and 0055 (direct-bypass confirmation)
- **Date:** 2026-09-06

## Context

The h workflows read untrusted GitHub content. The coordinator's bash
allowlist used prefix globs (`git status*`), and OpenCode matches the final
rule against globs over the whole command string, so a chained command
(`git status; curl …`) matched an allow and ran silently. The coordinator
also kept open-web fetch/search and script-executing commands (test runners,
package managers, make, go build/vet) allowed, so injected PR or issue text
could reach the network or execute contributor code with no prompt.

## Decision

The coordinator and both h workers declare `network: false`; GitHub access
flows only through `gh`. The agentfm renderer emits composition guards after
every bash allowlist: patterns for `;`, `&&`, `||`, `|`, `$(`, backticks,
redirection, and newlines re-ask, so no allow can widen into arbitrary
execution. A rendered `bash_deny` list denies the coordinator's
gate-skipping subcommands (`onto|to bypass`, the onto evidence-token writes)
even inside the broad `onto *`/`to *` allows — they are single commands, so
the guards cannot see them. Script-executing commands carry no allows at
all, and homonto's mutating subcommands (apply, init, update, snapshot)
prompt; only its read-only surface is allowed. Push, merge, PR
creation/comment, and raw `gh api graphql` invocations prompt too: each is a
consent checkpoint for a mutating or arbitrary-API surface, and a denied
prompt is a blocker report — the branch and archive stay intact. MCP tools
a config enables are outside the rendered permission map: enabling an MCP
server next to h grants its tools to the coordinator despite
`network: false`, and that remains the config owner's trust decision.

## Consequences

Compound, script-executing, publishing, and raw-API commands prompt
everywhere, including trusted everyday work. That friction is the price of
a boundary that holds under prompt injection. An allowlist can no longer
silently widen into arbitrary execution, and the workflow's own gate-skipping
commands are unreachable rather than audited-after-the-fact. The MCP
exposure is documented rather than denied; a future renderer change could
deny per-server tool keys if OpenCode documents them.
