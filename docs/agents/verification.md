# Verifying changes

The rule is evidence before assertion. Run the command, read the output, report
what it actually said. "Should pass" is not a result.

## Pick the narrowest command that proves the change

Routine work does not need the full gate. `task` lists every task.

| Scope | Command |
|---|---|
| One package | `go test ./internal/<pkg>/...` |
| Packages with Go changes vs `main` (committed, uncommitted, untracked) | `task test-changed` (`task test-changed BASE=<ref>` to compare elsewhere) |
| Tests CodeGraph links to your changes | `task affected` (lists them; [codegraph.md](codegraph.md)) |
| Concurrency-sensitive | add `-race`, or `task race` |
| Compile-wide effect | `task vet` and `task lint` |
| Formatting | `gofmt -l .` (empty output means clean), `task fmt` to fix |
| Before pushing | `task check`: gofmt, vet, lint, doc references, all tests |
| Everything, pre-tag | `task gate` (`./scripts/gate.sh`) |

## The full gate

`./scripts/gate.sh` is the single gate shared by local rehearsal, CI
(`.github/workflows/ci.yml`), and release publication
(`.github/workflows/release.yml`), so no path runs a weaker check set than
another. It needs a Go toolchain **and a running Docker daemon**, and it takes
a while — do not reach for it to check a one-package edit.

In order: `gofmt -l`, `go mod tidy -diff`, `go vet`, `golangci-lint`,
`go build`, `go test`, `go test -race`, version-stamp smoke for all three
binaries, a CLI plan smoke, the agent-docs reference check, the workflow-skills
shell-out check, the OpenCode permission-telemetry contract (needs Node 24),
the installer's mocked-network tests, `govulncheck`, and the triple-binary
Docker E2E suites, including a release-packaging smoke.

Because CI runs this exact script, a green local gate is real evidence about
CI — one of the few places where that inference is sound.

## Traps

**The lint config is part of the gate.** `.golangci.yml` is checked by
`task lint`, the pre-commit hook, and the gate. The golangci-lint version is
pinned once, as `GOLANGCI_LINT_VERSION` in `scripts/gate.sh`; `Taskfile.yml`
reads it from there, and `task tools` installs that version. `task lint` and
the pre-commit hook run whatever `golangci-lint` is on `PATH`, so run
`task tools` after a bump. Prefer fixing a finding over excluding it. An
exclusion carries a comment saying why.

**The Go toolchain is pinned in three places.** `go.mod`'s `toolchain` line is
what builds, tests, the gate, and `task tools` use. `.github/workflows/ci.yml`
and `release.yml` provision Go with `setup-go`, whose `go-version` must match
it. An older provisioned Go lets `govulncheck@latest` downgrade and fail the
gate before it starts, so bump all three together.

**No base, no answer.** `task test-changed` and `task affected` diff against
`main`, falling back to `origin/main`. If neither resolves (a shallow CI
clone, a fork without `main`), they fail instead of reporting "no changed Go
packages". Fetch the base, or pass one: `task test-changed BASE=<ref>`.

**The eof-fixer aborts your commit.** `.pre-commit-config.yaml` runs
`end-of-file-fixer` and `trailing-whitespace`. When either modifies a staged
file, the commit **fails** and the fix lands in the working tree. Re-stage and
commit again. Never `--amend` to recover — the amend folds the new work into
the previous commit, which has silently merged unrelated commits before.

**A check that finds nothing may be finding nothing because its input is
gone.** A gate step that skips its work and exits 0 reports as coverage while
proving nothing. When a check's input moves or is deleted, retire the check
deliberately rather than leaving a permanently-green no-op. This is why the
old `spec-command-check.sh` was removed when specs stopped being tracked.

**Docker E2E failures are usually environmental first.** Confirm the daemon is
up and the image builds before reading a suite failure as a code defect.

## Reporting

State the command and its real outcome. If something was not run — the Docker
suites, the race detector, a platform you cannot reach — say which and why. A
verification gap that is named costs nothing; one that is discovered later
costs trust in every other claim.
