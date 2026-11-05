# Real-harness E2E matrix

End-to-end tests that build `homonto`, `onto` and `to` from source and run
them against the **actual** OpenCode 2 and Claude Code CLIs installed inside a
container — not stub
config files. The `live` cell reuses the invoking user's account (credentials
mounted read-only at run time; never baked into the image), and OpenCode uses a
free model so that check costs nothing.

## Run

```bash
scripts/e2e-matrix.sh                    # build image + full matrix + internals dump
scripts/e2e-matrix.sh --no-build         # reuse the existing image
scripts/e2e-matrix.sh --skip-live        # projection + onto only (no credentials)
scripts/e2e-matrix.sh onto               # a single cell
scripts/e2e-matrix.sh projection/opencode live/opencode
```

Per-cell logs and the internals analysis land in `test/e2e/.out/` (gitignored).

## The matrix

| suite \ tool | opencode | claude | what it proves |
|---|:---:|:---:|---|
| **projection** | ✓ | ✓ | `homonto apply` projects next to the user's own config and the real tool reads it back. On Claude Code: native agents, policies, hooks, permissions and session agent; the MCP server stays pending in an untrusted folder and connects once trusted; `doctor` reports the tier and accepted limitations. |
| **live** | ✓ | | OpenCode authenticates with the mounted account and answers a trivial prompt (`PONG`) using a free model. |
| **recovery** | ✓ | ✓ | An active onto change reaches the coordinator through homonto's runtime integration: on Claude Code at startup, after `--resume` and after `/compact`, plus a `homonto_status` call over MCP; on OpenCode in a new and a continued session. |
| **onto** | shared | | `onto` drives a change through the full `open → design → build → verify → close` lifecycle and archives it. |
| **reference** | shared | | The [reference extension set](../../examples/reference-set/) installed from digest-pinned `remote:` archives onto both harnesses, run through its engine's native lifecycle with the real CLI. |
| **analyze** | — | — | Not pass/fail: dumps the container's internal state for inspection. |

Claude Code loads a project's hooks and approves its `.mcp.json` servers only
in a trusted folder. The suites accept trust the way a user does on first
launch (`claude_trust` in `suites/lib.sh`); homonto never trusts a folder
itself.

## Credentials (reused, never stored)

The orchestrator mounts `~/.local/share/opencode/auth.json` (override with
`OPENCODE_AUTH`, model with `E2E_OPENCODE_MODEL`) and
`~/.claude/.credentials.json` (override with `CLAUDE_AUTH`; the cells use
haiku) read-only for the live and recovery cells. A missing credential file
skips its cells rather than failing them.

## Layout

- `Dockerfile` — builds both binaries, installs the real OpenCode CLI via its
  official installer, and verifies all three resolve at build time.
- `entrypoint.sh` — selects a suite by `$E2E_SUITE` / `$E2E_TOOL`.
- `suites/` — `projection.sh`, `live.sh`, `onto.sh`, `analyze.sh`, and shared
  `lib.sh`.
- `../../scripts/e2e-matrix.sh` — host orchestrator (build, matrix, mounts,
  summary).

## Relation to the smoke test

`scripts/docker-test.sh` (`test/docker/`) runs the triple-binary suites that
are part of `scripts/gate.sh`; they assert on files, links, and state without a
real OpenCode. This matrix is the heavier, real-tool counterpart: it is not in
the gate, needs network access to install OpenCode, and its `live` cell needs
an account.
