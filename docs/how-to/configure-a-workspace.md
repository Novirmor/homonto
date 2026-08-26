# Configure A Workspace

Configure a workspace after `init` writes `.homonto/config.toml`. The manifest
identifies the control repository, its members, and the checks Homonto runs.

## Choose the Control Repository and Members

The control repository holds the portable configuration and workflow record.
Members are the directories where assignments run. A workspace can use the
control repository as its only member or coordinate several Git and non-Git
members.

Run discovery before initialization when you need to inspect candidate members:

```bash
homonto init --discover
```

Then initialize the confirmed directories:

```bash
homonto init --workflow task --member services/api --member assets
```

Use repository-relative member paths. Do not share a member between active
workspaces.

## Add Verification Commands

Add one or more checks under the member that owns them:

```toml
[[members.verification]]
name = "unit"
command = ["go", "test", "./..."]
working_dir = "."
environment = ["PATH", "HOME"]
timeout = "5m"
```

`command` is an argument vector, not a shell command. Homonto forwards only
the environment names listed in `environment`. Include `PATH` when the command
uses a bare executable name such as `go`.

## Classify Files

Use `[members.paths]` to identify tests, generated files, and vendored files:

```toml
[members.paths]
tests = ["**/*_test.go"]
generated = ["gen/**"]
vendored = ["vendor/**"]
```

These classes affect Change preset scope assessment and the write scope issued
to assignments. See the [configuration reference](../reference/configuration.md)
for glob semantics and validation timing.

## Check the Result

Open the workspace with a normal command after editing the manifest:

```bash
homonto status
```

Homonto validates the manifest before it uses it. Correct the reported field
or path rather than continuing with an assumed configuration.
