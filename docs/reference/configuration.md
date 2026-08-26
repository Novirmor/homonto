# Configuration Reference

`.homonto/config.toml` describes a workspace. `init` writes it; operators can
edit it. Homonto validates the manifest before it uses it.

```toml
schema_version = 1

[workspace]
id = "6ba7b810-9dad-41d4-80b4-00c04fd430c8"
workflow = "task"

[control]
id = "11111111-2222-4333-8444-555555555555"
path = "."

[[members]]
id = "22222222-3333-4444-8555-666666666666"
path = "services/api"
kind = "git"
```

## Workspace, Control, And Members

`workspace.id` is the portable workspace identity. `workflow` is `task` or
`change`. `control` identifies the repository that stores Homonto's portable
record. Each `members` entry names a workspace-relative Git or non-Git work
directory.

Git members use worktrees for isolation. Non-Git members use content-addressed
snapshots and a staged integration directory. The control repository can also
be a member.

## Verification

Add checks beneath their member:

```toml
[[members.verification]]
name = "unit"
command = ["go", "test", "./..."]
working_dir = "."
environment = ["PATH", "HOME"]
timeout = "5m"
```

| Field | Meaning |
|---|---|
| `name` | Unique member-local check name. |
| `command` | Argument vector, not a shell string. |
| `working_dir` | Member-relative directory; defaults to the member root. |
| `environment` | Names of ambient variables to forward. |
| `timeout` | Duration from one second through 24 hours; defaults to ten minutes. |

Homonto runs checks itself. A failing, timed-out, or unstarted check does not
become passing evidence.

## Path Classes

```toml
[members.paths]
tests = ["**/*_test.go"]
generated = ["gen/**"]
vendored = ["vendor/**"]
```

Path classes identify tests, generated output, and vendored files. They affect
Change preset scope assessment and assignment write scope. Unclassified paths
are source paths.

The manifest validates each glob's lexical safety when it opens: no empty,
absolute, backslash, NUL, or parent-directory pattern. Cross-class duplicate
patterns are detected later when Homonto constructs a path matcher for the
operation that needs one. A duplicate pattern in two classes is refused.

`*` matches within one path segment. `**` is a whole segment and matches zero
or more segments except at the end: `vendor/**` matches content below `vendor`,
not `vendor` itself. The bare `**` matches every path.

## Routes, Updates, And Integrations

`[routes]` may declare host and role model settings. The current runtime
validates and fingerprints those values but does not use them to route agents.

`[update]` may declare `stable` or `prerelease`. The current runtime validates
and fingerprints the setting; it does not expose a fetch or install operation.

`[integrations].commit_generated` is likewise validated and fingerprinted. Use
the `host install --commit` flag for current generated-file behavior.

Do not put secrets in the manifest. Verification entries name environment
variables; Homonto reads their values from the process environment when a check
runs.
