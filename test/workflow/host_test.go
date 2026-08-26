package workflow

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/app"
	"github.com/noviopenworks/homonto/internal/host"
	"github.com/noviopenworks/homonto/internal/host/claude"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/task"
)

// TestHostInstallProducesADrivableIntegration installs both hosts through
// the CLI and checks what landed.
func TestHostInstallProducesADrivableIntegration(t *testing.T) {
	w := newWorkspace(t)
	// Both tools are "in use" here.
	for _, dir := range []string{".claude", ".opencode"} {
		if err := os.MkdirAll(filepath.Join(w.root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	out, err := w.run(t, "host", "install")
	if err != nil {
		t.Fatalf("host install: %v\n%s", err, out)
	}
	for _, rel := range []string{
		".claude/skills/homonto-task/SKILL.md",
		".claude/commands/homonto-task.md",
		".claude/settings.json",
		".opencode/skill/homonto-task/SKILL.md",
		".opencode/command/homonto-task.md",
		".opencode/plugin/homonto.js",
	} {
		if _, err := os.Stat(filepath.Join(w.root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s was not installed: %v", rel, err)
		}
	}
	// The task workspace gets no change entry point.
	if _, err := os.Stat(filepath.Join(w.root, ".claude", "skills", "homonto-change")); err == nil {
		t.Error("a task workspace was given a change entry point")
	}
	// Re-running changes nothing.
	out, err = w.run(t, "host", "install", "--dry-run")
	if err != nil {
		t.Fatalf("host install --dry-run: %v\n%s", err, out)
	}
	if strings.Contains(out, "create ") || strings.Contains(out, "update ") {
		t.Fatalf("re-installing would change something:\n%s", out)
	}
}

// TestProbeIsReadOnly is the contract a host depends on: it runs this on
// every session start, so starting a session must change nothing.
func TestProbeIsReadOnly(t *testing.T) {
	w := newWorkspace(t)
	before := snapshotTree(t, w.root)

	out, err := w.run(t, "host", "probe", "--host", "opencode")
	if err != nil {
		t.Fatalf("host probe: %v\n%s", err, out)
	}
	var resp protocol.ProbeResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode probe: %v\n%s", err, out)
	}
	if resp.State != protocol.ProbeIdle {
		t.Fatalf("probe state = %s, want idle in a fresh workspace", resp.State)
	}
	if diff := treeDiff(before, snapshotTree(t, w.root)); diff != "" {
		t.Fatalf("the probe changed the workspace: %s", diff)
	}
}

// TestProbeReportsOneResumableWork.
func TestProbeReportsOneResumableWork(t *testing.T) {
	w := newWorkspace(t)
	if out, err := w.run(t, "task", "start", "fix-login"); err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}
	out, err := w.run(t, "host", "probe", "--host", "opencode")
	if err != nil {
		t.Fatalf("host probe: %v\n%s", err, out)
	}
	var resp protocol.ProbeResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode probe: %v\n%s", err, out)
	}
	if resp.State != protocol.ProbeResumable || resp.Work == nil {
		t.Fatalf("probe = %+v, want one resumable work", resp)
	}
	if resp.Work.Name != "fix-login" {
		t.Fatalf("probe named %q", resp.Work.Name)
	}
	if !strings.Contains(resp.Message, "unrelated") {
		t.Fatalf("the message does not tell the host to leave unrelated work alone: %q", resp.Message)
	}
}

// TestProbeRefusesToChooseBetweenTwoWorks.
//
// Starting a second work is refused, so this state cannot be reached
// through the commands — which is exactly why the probe must still handle
// it. A workspace can arrive here from a version that allowed it, or from
// state repaired by hand, and the answer must be "you choose", never a
// guess. The second work is therefore planted as a legacy task_states row
// written straight into the runtime database, bypassing the guard the
// command applies.
func TestProbeRefusesToChooseBetweenTwoWorks(t *testing.T) {
	w := newWorkspace(t)
	if out, err := w.run(t, "task", "start", "fix-login"); err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}
	db, err := store.Open(context.Background(), filepath.Join(w.root, app.ControlDir, "runtime.db"), store.OpenOptions{})
	if err != nil {
		t.Fatalf("open runtime database: %v", err)
	}
	second, err := identity.NewWorkID()
	if err != nil {
		db.Close()
		t.Fatalf("NewWorkID: %v", err)
	}
	err = db.Update(context.Background(), func(tx *store.Tx) error {
		_, err := tx.ExecContext(context.Background(), `
			INSERT INTO task_states (work_id, name, step, generation, baseline, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			second, "fix-cache", task.StepPlanExplore, 1, `{}`, time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
	db.Close()
	if err != nil {
		t.Fatalf("plant legacy second task: %v", err)
	}

	out, err := w.run(t, "host", "probe", "--host", "opencode")
	if err != nil {
		t.Fatalf("host probe: %v\n%s", err, out)
	}
	var resp protocol.ProbeResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode probe: %v\n%s", err, out)
	}
	if resp.State != protocol.ProbeAmbiguous || len(resp.Candidates) != 2 {
		t.Fatalf("probe = %+v, want two candidates", resp)
	}
	if !strings.Contains(resp.Message, "do not choose for them") {
		t.Fatalf("the message does not refuse to choose: %q", resp.Message)
	}
}

// TestProbeAnswersOutsideAWorkspace proves a host running this everywhere
// does not get an error on every unrelated session.
func TestProbeAnswersOutsideAWorkspace(t *testing.T) {
	w := newWorkspace(t)
	w.root = t.TempDir() // a plain directory, not a workspace
	out, err := w.run(t, "host", "probe", "--host", "opencode")
	if err != nil {
		t.Fatalf("host probe outside a workspace: %v\n%s", err, out)
	}
	var resp protocol.ProbeResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode probe: %v\n%s", err, out)
	}
	if resp.State != protocol.ProbeUnavailable {
		t.Fatalf("probe state = %s, want unavailable", resp.State)
	}
}

// TestClaudeProbeRendersSessionContext proves the Claude shape.
func TestClaudeProbeRendersSessionContext(t *testing.T) {
	w := newWorkspace(t)
	// Idle: Claude gets nothing at all.
	out, err := w.run(t, "host", "probe", "--host", "claude")
	if err != nil {
		t.Fatalf("host probe: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("an idle probe added session context:\n%s", out)
	}
	if out, err := w.run(t, "task", "start", "fix-login"); err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}
	out, err = w.run(t, "host", "probe", "--host", "claude")
	if err != nil {
		t.Fatalf("host probe: %v\n%s", err, out)
	}
	var ctx claude.SessionContext
	if err := json.Unmarshal([]byte(out), &ctx); err != nil {
		t.Fatalf("decode session context: %v\n%s", err, out)
	}
	if ctx.HookSpecificOutput.HookEventName != claude.EventSessionStart {
		t.Fatalf("the response names %q", ctx.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(ctx.HookSpecificOutput.AdditionalContext, "fix-login") {
		t.Fatalf("the context does not name the work:\n%s", out)
	}
}

// TestHostGuardRefusesInTheHostsOwnShape drives the real hook path: a
// Claude PreToolUse event in, a Claude permission decision out.
func TestHostGuardRefusesInTheHostsOwnShape(t *testing.T) {
	w := newWorkspace(t)
	if out, err := w.run(t, "task", "start", "fix-login"); err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}
	event := map[string]any{
		"session_id":      "s1",
		"hook_event_name": claude.EventPreToolUse,
		"cwd":             w.root,
		"tool_name":       "Write",
		"tool_input":      map[string]any{"file_path": filepath.Join(w.root, ".homonto", "checkpoint.json")},
	}
	// No assignment and no grant: the session has no permission to write.
	out, err := w.runInput(t, event, "host", "guard", "--host", "claude")
	if err == nil {
		t.Fatalf("guarding a control-state write succeeded:\n%s", out)
	}
	var decision claude.Decision
	if err := json.Unmarshal([]byte(out), &decision); err != nil {
		t.Fatalf("decode hook decision: %v\n%s", err, out)
	}
	if decision.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("decision = %+v, want deny", decision.HookSpecificOutput)
	}
	if !strings.Contains(decision.HookSpecificOutput.PermissionDecisionReason, "no_permission") {
		t.Fatalf("the refusal does not carry the code: %q",
			decision.HookSpecificOutput.PermissionDecisionReason)
	}

	// A read writes nothing and is allowed without any permission.
	event["tool_name"] = "Read"
	event["tool_input"] = map[string]any{"file_path": "README.md"}
	out, err = w.runInput(t, event, "host", "guard", "--host", "claude")
	if err != nil {
		t.Fatalf("guarding a read: %v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &decision); err != nil {
		t.Fatalf("decode hook decision: %v\n%s", err, out)
	}
	if decision.HookSpecificOutput.PermissionDecision != "allow" {
		t.Fatalf("a read was refused: %+v", decision.HookSpecificOutput)
	}
}

// TestHostGuardAllowsAnAssignmentWrite drives the whole loop: an issued
// implementer's tokens come back through the environment and the write is
// allowed.
func TestHostGuardAllowsAnAssignmentWrite(t *testing.T) {
	w := newWorkspace(t)
	if out, err := w.run(t, "task", "start", "fix-login"); err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}
	var impl protocol.Action
	for i := 0; i < 20 && impl.ID == ""; i++ {
		resp := w.next(t)
		if resp.State == protocol.NextComplete {
			break
		}
		for _, act := range resp.Actions {
			if act.Role == protocol.RoleImplementer {
				impl = act
			}
		}
		if impl.ID != "" {
			break
		}
		for _, act := range resp.Actions {
			w.answer(t, act)
		}
	}
	if impl.ID == "" {
		t.Fatal("the fixture never reached an implementer assignment")
	}
	t.Setenv("HOMONTO_ACTION_ID", string(impl.ID))
	t.Setenv("HOMONTO_ACTION_TOKEN", string(impl.FreshnessToken))

	event := map[string]any{
		"session_id":      "s1",
		"hook_event_name": claude.EventPreToolUse,
		"cwd":             filepath.Join(w.root, filepath.FromSlash(impl.WorkingDirectory)),
		"tool_name":       "Write",
		"tool_input": map[string]any{
			"file_path": filepath.Join(w.root, filepath.FromSlash(impl.WorkingDirectory),
				impl.WriteScope.Paths[0]),
		},
	}
	out, err := w.runInput(t, event, "host", "guard", "--host", "claude")
	if err != nil {
		t.Fatalf("guarding an in-scope write: %v\n%s", err, out)
	}
	var decision claude.Decision
	if err := json.Unmarshal([]byte(out), &decision); err != nil {
		t.Fatalf("decode hook decision: %v\n%s", err, out)
	}
	if decision.HookSpecificOutput.PermissionDecision != "allow" {
		t.Fatalf("an in-scope write was refused: %+v", decision.HookSpecificOutput)
	}
}

// TestInstalledWrappersStayThin re-checks the installed files on disk,
// not just the templates.
func TestInstalledWrappersStayThin(t *testing.T) {
	w := newWorkspace(t)
	if err := os.MkdirAll(filepath.Join(w.root, ".opencode"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := w.run(t, "host", "install", "--tool", "opencode"); err != nil {
		t.Fatalf("host install: %v\n%s", err, out)
	}
	for _, rel := range []string{
		".opencode/skill/homonto-task/SKILL.md",
		".opencode/plugin/homonto.js",
	} {
		body, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !host.Owned(rel, body) {
			t.Errorf("%s carries no matching ownership marker", rel)
		}
	}
}

// snapshotTree records every file under root with its size and mode.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out[rel] = info.Mode().String() + ":" + itoa(info.Size())
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// treeDiff describes the first difference between two tree snapshots.
func treeDiff(before, after map[string]string) string {
	for path, state := range after {
		if was, ok := before[path]; !ok {
			return "created " + path
		} else if was != state {
			return "changed " + path
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			return "removed " + path
		}
	}
	return ""
}

// TestGeneratedHostAssetsAreLoadableByTheirHost checks the generated files
// against the tools that have to read them.
//
// Every other host test asserts what Homonto wrote. None of them asserts
// that the host can load it — and a settings file with one stray comma, or
// a plugin with a syntax error, fails silently inside the host: the hooks
// simply never fire, and the write boundary is gone with no error anywhere.
//
// The JavaScript half needs node, which is what OpenCode runs the plugin
// with. Where node is absent the check is skipped rather than faked.
func TestGeneratedHostAssetsAreLoadableByTheirHost(t *testing.T) {
	w := newWorkspace(t)
	for _, dir := range []string{".claude", ".opencode"} {
		if err := os.MkdirAll(filepath.Join(w.root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	for _, tool := range []string{"claude", "opencode"} {
		if out, err := w.run(t, "host", "install", "--tool", tool); err != nil {
			t.Fatalf("host install %s: %v\n%s", tool, err, out)
		}
	}

	// Claude Code reads settings.json. Invalid JSON there is not a partial
	// failure: the whole file is ignored, hooks included.
	settings := filepath.Join(w.root, ".claude", "settings.json")
	body, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("the generated settings.json is not valid JSON: %v", err)
	}
	if _, ok := parsed["hooks"]; !ok {
		t.Error("the generated settings.json declares no hooks")
	}

	// OpenCode loads the plugin as an ES module.
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; skipping the plugin load check")
	}
	plugin := filepath.Join(w.root, ".opencode", "plugin", "homonto.js")
	cmd := exec.Command(node, "--input-type=module", "-e",
		"await import("+strconvQuote("file://"+plugin)+")")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node could not load the generated plugin: %v\n%s", err, out)
	}
}

// strconvQuote renders a JavaScript string literal.
func strconvQuote(s string) string {
	quoted, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(quoted)
}
