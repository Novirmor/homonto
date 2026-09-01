# homonto CLI reference

Every command, flag, and exit-code contract of the `homonto` binary. For the
workflow binaries see the [onto reference](onto-reference.md) and the
[to reference](to-reference.md).

Global behavior:

- `--config <path>` (persistent flag, default `homonto.toml`) selects the
  config file for `plan`, `apply`, `status`, and `doctor`. `init`
  instead takes an optional target directory argument.
- All output goes to **stderr** (cobra's default). Redirect with `2>&1` when
  capturing output in scripts.
- Unless `--exit-code` is noted below, commands exit `0` on success and
  non-zero on error.

## `homonto init [dir]`

Scaffold a starter repo: `homonto.toml`, `.gitignore` (excluding `.homonto/`),
`.env.example`, and `homonto/skills/`. Writes into `dir` (default: the current
directory) and **never overwrites** an existing file.

```console
$ homonto init
$ homonto init ~/dotfiles/ai     # scaffold somewhere else
```

## `homonto plan`

Print the diff between the desired state (`homonto.toml`) and what is on
disk. `plan` writes nothing and **never resolves or prints a secret**;
references stay `${…}` tokens.

| Flag | Effect |
|---|---|
| `--output text\|json` | output format (default `text`) |
| `--exit-code` | opt-in exit taxonomy: exit `2` when changes are pending |

The diff is Terraform-style: `+` create, `~` update, `-` delete. Unchanged
keys stay silent.

```console
$ homonto plan
opencode:
  ~ setting.model: "anthropic/claude-opus-4-8" -> "anthropic/claude-sonnet-5"

$ homonto plan --output json | jq .        # machine-readable
$ homonto plan --exit-code && echo clean   # CI: fail when an apply is pending
```

## `homonto apply`

Project the config into the tools: print the plan, confirm (`[y/N]`), then
write.

| Flag | Effect |
|---|---|
| `--yes` | skip the confirmation prompt |

Guarantees (details in [projection & state](projection-and-state.md) and
[secrets](secrets.md)):

- **Two-phase.** Every secret resolves up front, before any file is written.
  One failed resolution aborts the whole apply untouched.
- **Atomic writes.** Each file is written via temp + rename, so an
  interrupted run never leaves a half-written file.
- **Surgical.** Only managed keys are written; unmanaged keys survive. An
  unparseable tool file makes that adapter abort and report rather than
  overwrite.
- **State per adapter.** State is saved after each successful adapter, so a
  failure partway through never loses an already-applied adapter's records.
- **Adoption.** A declared resource that already exists on disk exactly as
  homonto would write it is recorded into state with no file write.

## `homonto status`

Compare managed values on disk against the last-applied snapshot and report
two independent things:

- **Drift** — a managed value changed on disk *outside homonto*, or was
  deleted: `opencode setting.model drifted (will reset on apply)`.
- **Pending** — unapplied `homonto.toml` edits, reported as a count:
  `1 config change(s) awaiting apply (run `homonto apply`)`.

When neither is present it prints `No drift.`

| Flag | Effect |
|---|---|
| `--output text\|json` | output format (default `text`) |
| `--exit-code` | opt-in taxonomy: exit `2` on pending, `3` on drift |

## `homonto doctor`

Environment health check: is `pass` on `PATH`, do the tool config locations
exist, and does each owned skill have intact content plus both tool links?

| Flag | Effect |
|---|---|
| `--output text\|json` | output format (default `text`) |

## `homonto update`

Re-materialize this binary's embedded catalog (frameworks, skills, commands,
subagents) and re-project it, bringing installed content up to the running
version. Prints the version transition (binary, catalog, per-framework) and
shares apply's plan → confirm → apply flow.

| Flag | Effect |
|---|---|
| `--yes` | skip the confirmation prompt |

`update` does **not** download or replace the binaries themselves. Install
those the usual way (`go install …@latest` or the release archives), then run
`homonto update`. State records the versions behind each apply, and
`onto doctor` / `to doctor` warn when a workflow binary and the homonto that
installed its framework have drifted apart.

## `homonto cache gc`

Reclaim entries in the content-addressed remote cache
(`.homonto/cache/remote/`) that no `.homonto/remote.lock.json` entry
references. Kept out of `apply` on purpose, so reverting a `digest` pin can
still roll back from cache. See
[remote source trust](remote-source-trust.md).

## `homonto version`

Print the release-stamped build version (`homonto --version` works too).

## `homonto completion <shell>`

Generate a shell autocompletion script (bash, zsh, fish, powershell), a
standard cobra facility:

```console
$ homonto completion zsh > "${fpath[1]}/_homonto"
```
