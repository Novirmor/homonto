# Run Your First Task

This tutorial builds Homonto, initializes a Task workspace, installs one host
integration, and starts a Task. It assumes Git and either Claude Code or
OpenCode are available.

## Build Homonto

```bash
git clone https://github.com/noviopenworks/homonto
cd homonto
go build -o homonto .
./homonto version
```

Build this repository until the workflow product has its first tagged release.
Published tags through `v0.11.0` contain the retired configuration projector.

## Initialize a Workspace

A workspace has a control repository, which holds Homonto's records, and one
or more member directories where work occurs. Start in the directory that will
contain them.

```bash
mkdir -p demo/services/api
cd demo/services/api
git init
go mod init example.com/demo-api
printf 'package api\n' > api.go
git add go.mod api.go
git commit -m "Initial commit"
cd ../..
../homonto init --discover
```

`--discover` lists candidate members and makes no changes. Confirm the members
that belong to the workspace and select its workflow:

```bash
../homonto init --workflow task --member services/api
```

`init` creates the control repository when needed and writes
`.homonto/config.toml`. The first later command that opens the workspace for
writing creates the required runtime and document directories.

Task uses `plan -> do -> done`. Choose Change when the work needs an approved
proposal and design before implementation. See [Workflows](../concepts/workflows.md)
for the difference.

## Configure Verification

Before starting work, add the checks Homonto must run to
`.homonto/config.toml`. Commands are argument vectors and the environment is
an explicit allowlist:

```toml
[[members.verification]]
name = "unit"
command = ["go", "test", "./..."]
environment = ["PATH", "HOME"]
timeout = "5m"
```

Use the [workspace configuration procedure](../how-to/configure-a-workspace.md)
and [configuration reference](../reference/configuration.md) to complete the
manifest.

## Install a Host

Install the integration for the host you use. This example installs Claude
Code; use `--tool opencode` for OpenCode.

```bash
../homonto host install --tool claude
```

The command writes project-local integration files. See [Install a host](../how-to/install-a-host.md)
before choosing `--commit` or repairing an existing installation.

## Start the Task

```bash
../homonto task start fix-login --goal "Login fails after a restart."
```

In the host, run `/homonto-task`. The host asks Homonto for an action with
`next --json`, completes that action, and reports the result. You answer the
decisions that change scope, design, or risk.

You can inspect the next action from a terminal:

```bash
../homonto next --json
```

When the Task completes, Homonto archives its checked-off record under
`docs/homonto/tasks/archive/`. Any integration branch remains for you to
review and merge under the member repository's normal policy.
