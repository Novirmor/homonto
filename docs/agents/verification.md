# Verifying changes

The rule is evidence before assertion. Run the command, read the output, report
what it actually said. "Should pass" is not a result.

## Pick the narrowest command that proves the change

Routine work does not need the full gate.

| Scope | Command |
|---|---|
| One package | `go test ./internal/<pkg>/...` |
| Concurrency-sensitive | add `-race` |
| Compile-wide effect | `go build ./...` and `go vet ./...` |
| Formatting | `gofmt -l .` (empty output means clean) |
| Everything, pre-tag | `./scripts/gate.sh` |

## The full gate

`./scripts/gate.sh` is the single gate shared by local rehearsal, CI
(`.github/workflows/ci.yml`), and release publication
(`.github/workflows/release.yml`), so no path runs a weaker check set than
another. It needs a Go toolchain **and a running Docker daemon**, and it takes
a while — do not reach for it to check a one-package edit.

In order: `gofmt -l`, `go mod tidy -diff`, `go vet`, `go build`, `go test`,
`go test -race`, version-stamp smoke for all three binaries, a CLI plan smoke,
the workflow-skills shell-out check, `govulncheck`, and the triple-binary
Docker E2E suites, including a release-packaging smoke.

Because CI runs this exact script, a green local gate is real evidence about
CI — one of the few places where that inference is sound.

## Traps

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
