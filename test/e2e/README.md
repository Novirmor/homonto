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
scripts/e2e-matrix.sh --skip-live        # only the cells that need no credentials
scripts/e2e-matrix.sh onto               # a single cell
scripts/e2e-matrix.sh projection/opencode live/opencode
```

Per-cell logs and the internals analysis land in `test/e2e/.out/` (gitignored).

## The matrix

| suite \ tool | opencode | claude | what it proves |
|---|:---:|:---:|---|
| **projection** | ✓ | ✓ | `homonto apply` projects next to the user's own config and the real tool reads it back. On Claude Code: native agents, policies, hooks, permissions and session agent; the MCP server stays pending in an untrusted folder and connects once trusted; `doctor` reports the tier and accepted limitations. |
| **live** | ✓ | | OpenCode authenticates with the mounted account and answers a trivial prompt (`PONG`) using a free model. |
| **recovery** | ✓ | ✓ | An active onto change reaches the coordinator through homonto's runtime integration: on Claude Code at startup, after `--resume` and after `/compact` (the post-compaction `SessionStart` hook output carries the policy and the snapshot), plus a `homonto_status` call over MCP; on OpenCode in a new and a continued session, and after a real compaction (`opencode api session.compact`): the context then starts at the summary, yet the next request names a change created after it and `homonto_status` reports it. |
| **process** | ✓ | ✓ | The model runs a builtin process inside the real session through the projected `/to` command (OpenCode: `session.command`, which expands `.opencode/command/to.md`; Claude Code: `claude -p "/to …"`): `to new` → `to phase` (plan → do), checked against the `to` shim log, `to status` and the workflow snapshot. On Claude Code the run goes through homonto's hooks: the coordinator starts with the policy injected and the policy hook allows each `to` command. |
| **interrupt** | ✓ | ✓ | The same process killed at a defined state boundary: the `to` shim holds the harness's `to new` call open once the change is recorded, then the whole harness is SIGKILLed (the `claude` process group, or OpenCode's background service). A new process resumes the session with a prompt that names no change; the resume hook (Claude Code) or the context hook's snapshot (OpenCode) carries the change at plan, and the resumed run advances it to do. |
| **plumbing** | shared | | Offline (`--network none`) and without an account: the machinery the cells above assert with. The observation shims log and pass through, the boundary hold blocks only the first `to new` of its change, homonto's real hook output parses with the suites' filters, and OpenCode 2 expands the projected `/to` command through `session.command` and keeps the session across a killed service. |
| **onto** | shared | | `onto` drives a change through the full `open → design → build → verify → close` lifecycle and archives it. |
| **reference** | shared | | The [reference extension set](../../examples/reference-set/) installed from digest-pinned `remote:` archives onto both harnesses, run through its engine's native lifecycle with the real CLI. |
| **analyze** | — | — | Not pass/fail: dumps the container's internal state for inspection. |

`policy` (Claude Code only) checks the worker policy and the permission
handback from the real hooks; the
[release checklist](../../docs/release-checklist.md#2-live-host-checks) lists
every cell with what it proves.

## Evidence

Each credentialed suite names its scenarios with stable IDs
(`<suite>/<tool>/<scenario>`, listed at the top of each suite) and prints one
`E2E-EVIDENCE` line per outcome (`scenario` / `scenario_pass` in
`suites/lib.sh`). The orchestrator writes them, plus one line per cell, to
`test/e2e/.out/evidence.tsv`, headed by the commit under test and whether the
tree was clean. A cell's release evidence key is `e2e/<cell>`, declared in
`internal/starter` (`HarnessEvidence`); record it with
`go run ./scripts/releaseevidence record <manifest> e2e/<cell> pass --log test/e2e/.out/<cell>.log`.
A skipped cell is a `skip` line with its reason, never a pass.

Assertions read observation shims, not the model's prose, wherever the
harness allows: `install_shims` puts a `homonto` and a `to` wrapper ahead of
the real binaries and logs every hook call (input and output), every workflow
snapshot the OpenCode plugin takes (with its output) and every `to` call
(argv and exit code) to JSON lines.

## Budget

The credentialed cells are opt-in by credentials and `--skip-live` skips them
all. OpenCode runs a free model; Claude Code runs haiku with `--max-turns`.
Every harness call has a hard `timeout` (300 s for single answers, 900 s for
a process run, `E2E_PROCESS_TIMEOUT`), and every cell's container is killed
after `E2E_CELL_TIMEOUT` (2400 s).

Claude Code loads a project's hooks and approves its `.mcp.json` servers only
in a trusted folder. The suites accept trust the way a user does on first
launch (`claude_trust` in `suites/lib.sh`); homonto never trusts a folder
itself.

## Credentials (reused, never stored)

The orchestrator mounts `~/.local/share/opencode/auth.json` (override with
`OPENCODE_AUTH`, model with `E2E_OPENCODE_MODEL`) and
`~/.claude/.credentials.json` (override with `CLAUDE_AUTH`; the cells use
haiku) read-only for the live, recovery, policy, process and interrupt cells.
A missing credential file skips its cells rather than failing them.

## Layout

- `Dockerfile` — builds both binaries, installs the real OpenCode CLI via its
  official installer, and verifies all three resolve at build time.
- `entrypoint.sh` — selects a suite by `$E2E_SUITE` / `$E2E_TOOL`.
- `suites/` — one script per suite (`projection`, `live`, `recovery`,
  `policy`, `process`, `interrupt`, `plumbing`, `onto`, `reference`,
  `analyze`) and the
  shared `lib.sh` (assertions, scenario evidence, observation shims, OpenCode
  API helpers).
- `../../scripts/e2e-matrix.sh` — host orchestrator (build, matrix, mounts,
  summary).

## Relation to the smoke test

`scripts/docker-test.sh` (`test/docker/`) runs the triple-binary suites that
are part of `scripts/gate.sh`; they assert on files, links, and state without a
real OpenCode. This matrix is the heavier, real-tool counterpart: it is not in
the gate, needs network access to install OpenCode, and its `live` cell needs
an account.
