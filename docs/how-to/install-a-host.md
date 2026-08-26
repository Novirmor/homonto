# Install A Host

Install a project-local integration after the workspace has a selected
workflow. Homonto supports Claude Code and OpenCode on Linux and macOS.

## Install One Host

Name the host when installing into a project that does not already contain its
configuration directory:

```bash
homonto host install --tool claude
homonto host install --tool opencode
```

Without `--tool`, Homonto looks for an existing `.claude/` or `.opencode/`
directory and installs only for detected hosts. Use `--dry-run` to inspect the
planned changes before writing files.

The integration adds the selected workflow command, a skill, a read-only resume
probe, and a write guard. It does not embed workflow transitions or assignment
prompts in the host files.

## Commit Generated Files

Generated host files are ignored by default. Use `--commit` when the repository
should track files created by this installation:

```bash
homonto host install --tool claude --commit
```

`--commit` prevents this install from adding new ignore entries. It does not
remove entries added by an earlier installation. Remove those entries yourself
if the repository should begin tracking existing generated files.

## Repair Drift

Run the doctor when an integration does not resume work or its files differ
from the installed version:

```bash
homonto doctor
```

Homonto reports missing and changed files. It does not overwrite an unowned or
edited generated file unless you explicitly install with `--adopt`.

See the [host integration reference](../reference/host-integration.md) for file
ownership and shared Claude settings behavior.
