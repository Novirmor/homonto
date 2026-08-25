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
another. It needs a Go toolchain **and nothing else** — the product is one
static binary with no daemon and no services, so a gate that needed Docker
would be testing the harness rather than the program. It takes a while — do
not reach for it to check a one-package edit.

In order: `gofmt -l`, `go mod tidy -diff`, `go vet`, `go build`, shell syntax
for every `scripts/*.sh`, `go test`, `go test -race`, a version-stamp smoke, a
read-only smoke (a plain directory must stay plain), release packaging
end-to-end (built, signed, verified), the documentation suite, the
whole-program security suite, 30 seconds of fuzzing per target, and
`govulncheck`. `--quick` skips the last two for a fast local loop and prints
that it is not a release gate.

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

## Reporting

State the command and its real outcome. If something was not run — the race
detector, the fuzzer, a platform you cannot reach — say which and why. A
verification gap that is named costs nothing; one that is discovered later
costs trust in every other claim.
