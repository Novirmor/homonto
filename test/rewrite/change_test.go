package rewrite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/adr"
	"github.com/noviopenworks/homonto/internal/app"
	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// changeWorkspace is the hybrid fixture configured for the Change
// workflow.
func changeWorkspace(t *testing.T) *workspace {
	t.Helper()
	w := newWorkspace(t)
	w.cfg.Workspace.Workflow = workspacecfg.WorkflowChange
	manifest, err := workspacecfg.Marshal(w.cfg)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	writeFile(t, filepath.Join(w.root, app.ControlDir, app.ManifestName), string(manifest))
	return w
}

// changeBody is what the host writes into each Change document.
func changeBody(kind artifact.Kind) string {
	switch kind {
	case artifact.KindTasks:
		return "## Tasks\n\n- [ ] make login return true\n"
	case artifact.KindFix:
		return "## Fix\n\nreproduce: /bin/sh check.sh\n\n" +
			"Expected: login returns true. Actual: it returns false. Root cause: the constant.\n"
	case artifact.KindTweak:
		return "## Tweak\n\nFlip the login constant.\n"
	case artifact.KindProposal:
		return "## Proposal\n\nMake login work.\n"
	case artifact.KindDesign:
		return "## Design\n\nFlip the constant; no alternatives are viable.\n"
	case artifact.KindPlan:
		return "## Plan\n\nOne implementer edits src/login.go.\n"
	}
	return "## " + string(kind) + "\n\nWritten by the host.\n"
}

// answerChange dispatches one Change action to its canned response.
func (w *workspace) answerChange(t *testing.T, act protocol.Action, choices map[protocol.DecisionKind]string) {
	t.Helper()
	switch act.Kind {
	case protocol.KindEdit:
		w.editDocument(t, act, changeBody(artifact.Kind(act.Edit.Kind)))
		return
	case protocol.KindDecision:
		choice := changeChoice(act, choices)
		args := []string{"decide",
			"--action", string(act.ID), "--token", string(act.FreshnessToken),
			"--choice", choice, "--rationale", "fixture decision"}
		if act.Decision.QuestionID != "" {
			args = append(args, "--answer", "yes")
		}
		out, err := w.run(t, args...)
		if err != nil {
			t.Fatalf("decide(%s=%s): %v\n%s", act.Decision.Kind, choice, err, out)
		}
		return
	}
	if act.Role != protocol.RoleImplementer {
		w.report(t, act, readOnlyReport(act.Role))
		return
	}
	if strings.HasPrefix(act.Reason, "write the decision record") {
		w.writeADR(t, act)
		return
	}
	if strings.HasPrefix(act.Reason, "integrate") {
		w.report(t, act, w.integrate(t, act))
		return
	}
	w.report(t, act, w.implement(t, act))
}

// changeChoice picks the answer for one decision gate.
func changeChoice(act protocol.Action, choices map[protocol.DecisionKind]string) string {
	if c, ok := choices[act.Decision.Kind]; ok {
		return c
	}
	switch act.Decision.Kind {
	case protocol.DecisionKind(decision.KindConfirmClassification):
		return string("tweak")
	case protocol.DecisionKind(decision.KindPresetTripwire):
		return "continue"
	case protocol.DecisionKind(decision.KindReproductionException):
		return "accept"
	case protocol.DecisionKind(decision.KindRepairLimit):
		return "continue"
	}
	return "approve"
}

// editDocument writes a document body and finishes the edit through the
// CLI.
func (w *workspace) editDocument(t *testing.T, act protocol.Action, body string) {
	t.Helper()
	abs := filepath.Join(w.root, filepath.FromSlash(act.Edit.Document))
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", act.Edit.Document, err)
	}
	doc, err := artifact.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", act.Edit.Document, err)
	}
	for i := range doc.Regions {
		if doc.Regions[i].Region == artifact.RegionWholeDocument {
			doc.Regions[i].Content = []byte(body)
		}
	}
	rendered, err := artifact.Render(doc)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := os.WriteFile(abs, rendered, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := w.run(t, "accept-edit",
		"--action", string(act.ID), "--token", string(act.Edit.GrantToken))
	if err != nil {
		t.Fatalf("accept-edit(%s): %v\n%s", act.Edit.Kind, err, out)
	}
}

// writeADR fills in the allocated decision record and reports it.
func (w *workspace) writeADR(t *testing.T, act protocol.Action) {
	t.Helper()
	if len(act.WriteScope.Paths) != 1 {
		t.Fatalf("the ADR assignment scope = %v, want one allocated path", act.WriteScope.Paths)
	}
	rel := act.WriteScope.Paths[0]
	// The prompt carries the skeleton; a real agent would edit it. Writing
	// it verbatim is enough to prove the gate accepts a real record and
	// refused the empty reservation.
	skeleton := act.Prompt[strings.Index(act.Prompt, "# "):]
	writeFile(t, filepath.Join(w.root, filepath.FromSlash(rel)), skeleton)
	w.report(t, act, w.implement(t, act))
}

// driveChange runs a change through the CLI until it finishes.
func (w *workspace) driveChange(t *testing.T, choices map[protocol.DecisionKind]string) map[protocol.Role]bool {
	t.Helper()
	roles := map[protocol.Role]bool{}
	for i := 0; i < 60; i++ {
		resp := w.next(t)
		if resp.State == protocol.NextComplete {
			return roles
		}
		for _, act := range resp.Actions {
			if act.Role != "" {
				roles[act.Role] = true
			}
			w.answerChange(t, act, choices)
		}
	}
	t.Fatal("the change did not finish")
	return roles
}

// archivedChange returns the single archived change directory.
func (w *workspace) archivedChange(t *testing.T) string {
	t.Helper()
	base := filepath.Join(w.root, filepath.FromSlash(artifact.ChangesArchiveDir))
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("read the change archive: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("the change archive holds %d entries, want 1", len(entries))
	}
	return filepath.Join(base, entries[0].Name())
}

// requireFiles asserts a directory holds exactly the given documents.
func requireFiles(t *testing.T, dir string, want, absent []string) {
	t.Helper()
	for _, file := range want {
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			t.Errorf("%s is missing: %v", file, err)
		}
	}
	for _, file := range absent {
		if _, err := os.Stat(filepath.Join(dir, file)); err == nil {
			t.Errorf("%s should not exist", file)
		}
	}
}

// TestChangePreflightCreatesNothingUntilConfirmed proves the whole point
// of the classification candidate.
func TestChangePreflightCreatesNothingUntilConfirmed(t *testing.T) {
	w := changeWorkspace(t)
	out, err := w.run(t, "change", "start", "fix-login",
		"--request", "Login returns false after a restart.")
	if err != nil {
		t.Fatalf("change start: %v\n%s", err, out)
	}
	if !strings.Contains(out, "nothing is created until you confirm") {
		t.Fatalf("change start printed %q", out)
	}
	changes := filepath.Join(w.root, filepath.FromSlash(artifact.ChangesDir))
	entries, err := os.ReadDir(changes)
	if err != nil {
		t.Fatalf("read the changes directory: %v", err)
	}
	for _, e := range entries {
		if e.Name() != artifact.ArchiveName {
			t.Fatalf("preflight created %q before anything was confirmed", e.Name())
		}
	}

	// Abandoning the candidate removes nothing, and frees the name.
	out, err = w.run(t, "change", "abandon")
	if err != nil {
		t.Fatalf("change abandon: %v\n%s", err, out)
	}
	if !strings.Contains(out, "nothing was removed") {
		t.Fatalf("change abandon printed %q", out)
	}
	if out, err := w.run(t, "change", "start", "fix-login",
		"--request", "Try again."); err != nil {
		t.Fatalf("the name was not released: %v\n%s", err, out)
	}
}

// TestTweakChangeReachesTheArchive walks a Tweak end to end through the
// CLI over a real hybrid workspace.
func TestTweakChangeReachesTheArchive(t *testing.T) {
	w := changeWorkspace(t)
	if out, err := w.run(t, "change", "start", "fix-login",
		"--request", "Make login return true."); err != nil {
		t.Fatalf("change start: %v\n%s", err, out)
	}
	roles := w.driveChange(t, nil)
	for _, role := range []protocol.Role{
		protocol.RoleExplorer, protocol.RoleImplementer,
		protocol.RoleReviewer, protocol.RoleSkeptic,
	} {
		if !roles[role] {
			t.Errorf("the %s role was never assigned", role)
		}
	}
	status, err := w.run(t, "change", "status")
	if err != nil {
		t.Fatalf("change status: %v\n%s", err, status)
	}
	if !strings.Contains(status, "archived") || !strings.Contains(status, "tweak") {
		t.Fatalf("change status = %q, want an archived tweak", status)
	}
	dir := w.archivedChange(t)
	requireFiles(t, dir,
		[]string{"tweak.md", "tasks.md", "verification.md", "record.md"},
		[]string{"proposal.md", "design.md", "plan.md"})

	// Ready but never merged.
	api := filepath.Join(w.root, "services", "api")
	if !strings.Contains(git(t, api, "branch", "--list", "--format=%(refname:short)"),
		"homonto/integration/") {
		t.Fatal("no integration branch was left ready")
	}
	if strings.Contains(git(t, api, "show", "main:src/login.go"), "return true") {
		t.Fatal("the integration was merged into the member's own branch")
	}
}

// TestFullChangeReachesTheArchive walks a Full change through the CLI,
// including both approvals.
func TestFullChangeReachesTheArchive(t *testing.T) {
	w := changeWorkspace(t)
	if out, err := w.run(t, "change", "start", "rework-login",
		"--request", "Replace the login constant with a real check."); err != nil {
		t.Fatalf("change start: %v\n%s", err, out)
	}
	choices := map[protocol.DecisionKind]string{
		protocol.DecisionKind(decision.KindConfirmClassification): "full",
	}
	w.driveChange(t, choices)
	dir := w.archivedChange(t)
	requireFiles(t, dir,
		[]string{"proposal.md", "design.md", "tasks.md", "plan.md", "verification.md", "record.md"},
		nil)
	record, err := os.ReadFile(filepath.Join(dir, "record.md"))
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if !strings.Contains(string(record), "full change") {
		t.Fatalf("the record does not name the path:\n%s", record)
	}
}

// TestFixChangeReachesTheArchive walks a Fix, whose reproduction the fix
// document supplies.
func TestFixChangeReachesTheArchive(t *testing.T) {
	w := changeWorkspace(t)
	if out, err := w.run(t, "change", "start", "fix-login",
		"--request", "Login returns false; it should return true."); err != nil {
		t.Fatalf("change start: %v\n%s", err, out)
	}
	choices := map[protocol.DecisionKind]string{
		protocol.DecisionKind(decision.KindConfirmClassification): "fix",
	}
	w.driveChange(t, choices)
	dir := w.archivedChange(t)
	requireFiles(t, dir,
		[]string{"fix.md", "tasks.md", "verification.md", "record.md"},
		[]string{"proposal.md", "design.md", "plan.md"})
	body, err := os.ReadFile(filepath.Join(dir, "fix.md"))
	if err != nil {
		t.Fatalf("read fix.md: %v", err)
	}
	if !strings.Contains(string(body), "reproduce:") {
		t.Fatalf("the archived fix records no reproduction:\n%s", body)
	}
}

// TestUpgradedPresetReachesTheArchive walks a Tweak that trips its scope
// assessment and is upgraded to Full by the human.
func TestUpgradedPresetReachesTheArchive(t *testing.T) {
	w := changeWorkspace(t)
	if out, err := w.run(t, "change", "start", "rename-flag",
		"--request", "Rename the login flag."); err != nil {
		t.Fatalf("change start: %v\n%s", err, out)
	}
	choices := map[protocol.DecisionKind]string{
		protocol.DecisionKind(decision.KindPresetTripwire): "upgrade",
	}
	// The Open skeptic reports a public-API signal, so the preset trips.
	upgraded := false
	for i := 0; i < 60; i++ {
		resp := w.next(t)
		if resp.State == protocol.NextComplete {
			break
		}
		for _, act := range resp.Actions {
			if act.Decision != nil &&
				act.Decision.Kind == protocol.DecisionKind(decision.KindPresetTripwire) {
				upgraded = true
			}
			if act.Role == protocol.RoleSkeptic && !upgraded {
				w.report(t, act, protocol.SkepticReport{
					Assumptions: []string{"the rename is internal"},
					Findings: []protocol.Finding{{
						ID: "public_api", Severity: protocol.SeverityHigh,
						Summary: "the flag is documented", Evidence: []string{"README.md"},
						Recommendation: "treat it as public",
					}},
				})
				continue
			}
			w.answerChange(t, act, choices)
		}
	}
	if !upgraded {
		t.Fatal("the preset never tripped its scope assessment")
	}
	status, err := w.run(t, "change", "status")
	if err != nil {
		t.Fatalf("change status: %v\n%s", err, status)
	}
	if !strings.Contains(status, "full") {
		t.Fatalf("change status = %q, want an upgraded full change", status)
	}
	dir := w.archivedChange(t)
	// The upgrade keeps the preset input and freezes its task list.
	requireFiles(t, dir, []string{
		"tweak.md", "preset-tasks.md", "proposal.md", "design.md",
		"tasks.md", "verification.md", "record.md",
	}, nil)
	proposal, err := os.ReadFile(filepath.Join(dir, "proposal.md"))
	if err != nil {
		t.Fatalf("read proposal: %v", err)
	}
	if !strings.Contains(string(proposal), "Why this became a full change") {
		t.Fatalf("the proposal does not record why it was upgraded:\n%s", proposal)
	}
	record, err := os.ReadFile(filepath.Join(dir, "record.md"))
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if !strings.Contains(string(record), "began as a tweak preset") {
		t.Fatalf("the record does not say the change was upgraded:\n%s", record)
	}
	if _, err := os.Stat(filepath.Join(w.root, filepath.FromSlash(adr.Dir))); err == nil {
		// An upgraded preset defers its decision to Design, so it writes
		// no ADR for the tripwire itself.
		entries, _ := os.ReadDir(filepath.Join(w.root, filepath.FromSlash(adr.Dir)))
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				t.Errorf("an upgraded preset wrote a decision record: %s", e.Name())
			}
		}
	}
}
