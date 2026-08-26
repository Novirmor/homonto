# Diagnose A Refusal

Homonto stops when it cannot establish safe authority or fresh evidence. Read
the refusal, make the named condition true, then request the next action again.

## A Write Is Outside Its Scope

**Symptom:** a report says the write boundary rejected a changed path.

**Why it stopped:** the assignment did not authorize that path, or the host did
not present the assignment credentials.

**Safe next action:** restore the out-of-scope change or wait for an assignment
that includes the required path. Do not widen scope by editing `.homonto/` or
workflow-owned data directly.

## A Report Is Stale

**Symptom:** a report refers to an unknown, completed, or stale action token.

**Why it stopped:** the workspace state changed after Homonto issued the action.

**Safe next action:** request `next --json` and complete the newly issued
action. Do not resend the old report.

## A Check Does Not Start

**Symptom:** the check says a command is unavailable because `PATH`
is absent.

**Why it stopped:** verification runs with only the environment names in the
manifest.

**Safe next action:** add the required name to the check's `environment` list,
then rerun the workflow step. See [Configure a workspace](configure-a-workspace.md).

## A Finding Blocks Completion

**Symptom:** reviewer or skeptic findings prevent a workflow transition.

**Why it stopped:** critical and high findings require repair or an explicit
human decision.

**Safe next action:** repair the finding, or record the requested rationale when
the workflow offers an acceptance decision. Accepted findings do not make a
failing verification command pass.

## Host Files Drifted

**Symptom:** installation reports an existing file without a matching Homonto
ownership marker.

**Why it stopped:** Homonto will not overwrite an edited or unowned file.

**Safe next action:** keep the file, restore the managed version yourself, or
use `host install --adopt` only when replacing it is intentional. Run
`homonto doctor` to inspect the full installation.

## Work Is Ambiguous Or Already Active

**Symptom:** `next` cannot identify one work, or a new Task or Change is
refused because active work exists.

**Why it stopped:** top-level work shares member state and Homonto cannot choose
which work should control it.

**Safe next action:** inspect `homonto status` and `homonto doctor`, then
finish or abandon the unwanted work. Use the [CLI reference](../reference/cli.md)
and [host protocol](../reference/host-protocol.md) for exact command forms.
