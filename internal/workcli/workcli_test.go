package workcli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/applylock"
)

// onto and to mirror the Framework values each workflow CLI constructs, so the
// shared-contract tests exercise the exact configuration shipped.
var (
	onto = Framework{
		Name:          "onto",
		SkillsDir:     "skills/onto",
		GatePrefix:    "onto init",
		NamePrefix:    "onto new",
		ReservedNames: nil,
		GateAliases:   []string{"h"},
	}
	to = Framework{
		Name:          "to",
		SkillsDir:     "skills/to",
		GatePrefix:    "to",
		NamePrefix:    "to",
		ReservedNames: []string{"archive"},
		GateAliases:   []string{"h"},
	}
)

// TestGate_OrderedFailures verifies the three failure steps and the all-present
// pass for both framework configurations: a regression here would break the
// mutating-command precondition for every onto/to command.
func TestGate_OrderedFailures(t *testing.T) {
	for _, f := range []Framework{onto, to} {
		t.Run(f.Name, func(t *testing.T) {
			// 1. no homonto.toml.
			dir := t.TempDir()
			err := f.Gate(dir)
			if err == nil || !strings.Contains(err.Error(), "homonto init") {
				t.Fatalf("gate(no toml) = %v, want mention of homonto init", err)
			}
			// 2. homonto.toml without the framework's table.
			if err := os.WriteFile(
				filepath.Join(dir, "homonto.toml"),
				[]byte("[frameworks.other]\nsource=\"x\"\n"),
				0o644,
			); err != nil {
				t.Fatal(err)
			}
			err = f.Gate(dir)
			if err == nil || !strings.Contains(err.Error(), "[frameworks."+f.Name+"]") {
				t.Fatalf("gate(no table) = %v, want mention of [frameworks.%s]", err, f.Name)
			}
			// 3. declared but not applied.
			if err := os.WriteFile(
				filepath.Join(dir, "homonto.toml"),
				[]byte("[frameworks."+f.Name+"]\nsource=\"builtin:"+f.Name+"\"\n"),
				0o644,
			); err != nil {
				t.Fatal(err)
			}
			err = f.Gate(dir)
			if err == nil || !strings.Contains(err.Error(), "homonto apply") {
				t.Fatalf("gate(unapplied) = %v, want mention of homonto apply", err)
			}
			// 4. all present.
			if err := os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", f.SkillsDir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := f.Gate(dir); err != nil {
				t.Fatalf("gate(all present) = %v, want nil", err)
			}
		})
	}
}

// TestGate_GatePrefixInErrors locks the per-framework error prefix so the
// refactor preserves the exact diagnostics each CLI shipped (onto init / to).
func TestGate_GatePrefixInErrors(t *testing.T) {
	dir := t.TempDir()
	if err := onto.Gate(dir); err == nil || !strings.HasPrefix(err.Error(), "onto init: ") {
		t.Fatalf("onto gate error = %v, want prefix %q", err, "onto init: ")
	}
	if err := to.Gate(dir); err == nil || !strings.HasPrefix(err.Error(), "to: ") {
		t.Fatalf("to gate error = %v, want prefix %q", err, "to: ")
	}
}

// TestGateAcceptsAppliedHDeclaration: the h companion hard-depends on onto
// and to in the catalog, so an applied [frameworks.h] materializes both
// frameworks' skill directories. The gates must accept the h table as an
// alternative declaration — declared-but-unapplied h still fails (the
// catalog dir is the proof, not the table), and the missing-table error
// names both routes.
func TestGateAcceptsAppliedHDeclaration(t *testing.T) {
	for _, f := range []Framework{onto, to} {
		t.Run(f.Name, func(t *testing.T) {
			dir := t.TempDir()
			// Declared via h but not applied: table alone proves nothing.
			if err := os.WriteFile(
				filepath.Join(dir, "homonto.toml"),
				[]byte("[frameworks.h]\nsource=\"builtin:h\"\nscope=\"project\"\n"),
				0o644,
			); err != nil {
				t.Fatal(err)
			}
			if err := f.Gate(dir); err == nil || !strings.Contains(err.Error(), "homonto apply") {
				t.Fatalf("gate(h declared, unapplied) = %v, want mention of homonto apply", err)
			}
			// Applied h materializes both frameworks' skill dirs.
			if err := os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", f.SkillsDir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := f.Gate(dir); err != nil {
				t.Fatalf("gate(applied h) = %v, want nil", err)
			}
			// The missing-table error names the h alternative.
			empty := t.TempDir()
			if err := os.WriteFile(
				filepath.Join(empty, "homonto.toml"),
				[]byte("[frameworks.other]\nsource=\"x\"\n"),
				0o644,
			); err != nil {
				t.Fatal(err)
			}
			err := f.Gate(empty)
			if err == nil || !strings.Contains(err.Error(), "[frameworks."+f.Name+"]") || !strings.Contains(err.Error(), "[frameworks.h]") {
				t.Fatalf("gate(no table) = %v, want it to name [frameworks.%s] and the h alternative", err, f.Name)
			}
		})
	}
}

func TestGateRejectsWorkflowRootChangeWithState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "homonto.toml"), []byte("[workflow]\nroot=\"workflow\"\n[frameworks.onto]\nsource=\"builtin:onto\"\nscope=\"project\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", "skills", "onto"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "workflow", "changes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".homonto", "workflow-root"), []byte("workflow\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "homonto.toml"), []byte("[workflow]\nroot=\"other\"\n[frameworks.onto]\nsource=\"builtin:onto\"\nscope=\"project\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := onto.Gate(dir); err == nil || !strings.Contains(err.Error(), "while workflow state exists") {
		t.Fatalf("gate = %v, want fail-closed root-change error", err)
	}
}

func TestWorkflowRootRejectsEscapingSymlinks(t *testing.T) {
	for _, tc := range []struct {
		name string
		root string
		link string
	}{
		{name: "root", root: "workflow", link: "workflow"},
		{name: "parent", root: "workflow/records", link: "workflow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			outside := t.TempDir()
			if err := os.Symlink(outside, filepath.Join(dir, tc.link)); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "homonto.toml"), []byte("[workflow]\nroot=\""+tc.root+"\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := WorkflowRoot(dir); err == nil || !strings.Contains(err.Error(), "resolves outside") {
				t.Fatalf("WorkflowRoot = %v, want escaping symlink rejection", err)
			}
		})
	}
}

func TestWorkflowRootRejectsEscapingDefaultSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "docs")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := WorkflowRoot(dir); err == nil || !strings.Contains(err.Error(), "resolves outside") {
		t.Fatalf("WorkflowRoot = %v, want escaping default-root rejection", err)
	}
}

// TestValidChangeName_AcceptedShape covers the lowercase-hyphenated shape both
// frameworks share.
func TestValidChangeName_AcceptedShape(t *testing.T) {
	for _, f := range []Framework{onto, to} {
		for _, name := range []string{"a", "feature-x", "fix-42", "a-b-c"} {
			if err := f.ValidChangeName(name); err != nil {
				t.Errorf("%s.ValidChangeName(%q) = %v, want nil", f.Name, name, err)
			}
		}
	}
}

// TestValidChangeName_RejectedShape covers the names every framework refuses:
// empty, path traversal, path separators, embedded "..", uppercase, and a
// leading/double hyphen.
func TestValidChangeName_RejectedShape(t *testing.T) {
	for _, f := range []Framework{onto, to} {
		for _, name := range []string{"", "..", "../evil", "a/b", "a\\b", "a..b", "Foo", "-x", "a--b"} {
			if err := f.ValidChangeName(name); err == nil {
				t.Errorf("%s.ValidChangeName(%q) = nil, want error", f.Name, name)
			}
		}
	}
}

// TestValidChangeName_ReservedNamesIsFrameworkSpecific is the invariant the
// audit called out: to rejects "archive" (its archive directory), onto does
// not. The two frameworks must not drift on which names they reserve.
func TestValidChangeName_ReservedNamesIsFrameworkSpecific(t *testing.T) {
	if err := to.ValidChangeName("archive"); err == nil {
		t.Errorf("to.ValidChangeName(%q) = nil, want reserved error", "archive")
	}
	if err := onto.ValidChangeName("archive"); err != nil {
		t.Errorf("onto.ValidChangeName(%q) = %v, want nil (archive is onto's archive subdir, not a reserved change name)", "archive", err)
	}
}

// TestValidChangeName_NamePrefixInErrors locks the per-framework validation
// error prefix (onto new / to).
func TestValidChangeName_NamePrefixInErrors(t *testing.T) {
	if err := onto.ValidChangeName(""); err == nil || !strings.HasPrefix(err.Error(), "onto new: ") {
		t.Fatalf("onto ValidChangeName(\"\") = %v, want prefix %q", err, "onto new: ")
	}
	if err := to.ValidChangeName(""); err == nil || !strings.HasPrefix(err.Error(), "to: ") {
		t.Fatalf("to ValidChangeName(\"\") = %v, want prefix %q", err, "to: ")
	}
}

// TestHomontoAppliedVersion exercises the boundary cases: missing file, invalid
// JSON, and a present homontoVersion field.
func TestHomontoAppliedVersion(t *testing.T) {
	dir := t.TempDir()
	if got := HomontoAppliedVersion(dir); got != "" {
		t.Errorf("HomontoAppliedVersion(missing dir) = %q, want \"\"", got)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".homonto"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".homonto", "state.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := HomontoAppliedVersion(dir); got != "" {
		t.Errorf("HomontoAppliedVersion(bad json) = %q, want \"\"", got)
	}
	if err := os.WriteFile(filepath.Join(dir, ".homonto", "state.json"), []byte(`{"homontoVersion":"v1.2.3"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := HomontoAppliedVersion(dir); got != "v1.2.3" {
		t.Errorf("HomontoAppliedVersion(present) = %q, want %q", got, "v1.2.3")
	}
}

// TestNormalizeVersion covers the leading-v strip and build-metadata strip used
// by both doctors' skew check.
func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"v1.2.3":        "1.2.3",
		"1.2.3":         "1.2.3",
		"v0.1.0-dev":    "0.1.0-dev",
		"v1.2.3+dirty":  "1.2.3",
		"1.2.3+abc.123": "1.2.3",
		"":              "",
	}
	for in, want := range cases {
		if got := NormalizeVersion(in); got != want {
			t.Errorf("NormalizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestErrQuietFindingsIsSentinel guarantees the sentinel is identity-comparable
// (the contract errors.Is relies on): both workflow CLIs alias this exact value
// and their mains check errors.Is(err, <pkg>.ErrQuietFindings). A text-equal but
// identity-distinct error must NOT match.
func TestErrQuietFindingsIsSentinel(t *testing.T) {
	if ErrQuietFindings == nil {
		t.Fatal("ErrQuietFindings is nil")
	}
	if !errors.Is(ErrQuietFindings, ErrQuietFindings) {
		t.Error("errors.Is(ErrQuietFindings, ErrQuietFindings) = false, want true")
	}
	clone := errors.New("doctor: findings (quiet)")
	if errors.Is(clone, ErrQuietFindings) {
		t.Error("a text-equal but identity-distinct error matched ErrQuietFindings; sentinel contract is identity, not text")
	}
}

// TestLockWorkspace_ExcludesConcurrentAndRejectsLegacy verifies that current
// callers share a process-released guardian while an old O_EXCL artifact stays
// a visible blocker rather than being guessed stale from a pid.
func TestLockWorkspace_ExcludesConcurrentAndRejectsLegacy(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", ".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	unlock, err := LockWorkspace("to", root, path)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if _, err := LockWorkspace("to", root, path); err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Errorf("second lock while held = %v, want an in-progress error", err)
	}
	unlock()
	if err := os.WriteFile(path, []byte("pid=legacy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LockWorkspace("to", root, path); err == nil || !strings.Contains(err.Error(), "legacy O_EXCL") {
		t.Errorf("legacy lock must require hand cleanup: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	unlock2, err := LockWorkspace("to", root, path)
	if err != nil {
		t.Fatalf("lock after legacy cleanup: %v", err)
	}
	unlock2()
}

func TestLockWorkspaceKeepsGuardiansInOwnedMetadata(t *testing.T) {
	root := t.TempDir()
	workflowRoot := filepath.Join(root, "docs")
	legacyPath := filepath.Join(workflowRoot, "tasks", ".to.lock")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	unlock, err := LockWorkspace("to", root, legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	guardianPath, err := applylock.GuardianPath(filepath.Join(root, ".homonto"), legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(filepath.Join(root, ".homonto"), guardianPath); err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("guardian path = %q, want owned metadata below %q", guardianPath, filepath.Join(root, ".homonto"))
	}
	unlock()
	entries, err := os.ReadDir(filepath.Dir(legacyPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("records directory retains lock artifacts: %v", entries)
	}
}

func TestLockWorkspaceUsesGitPrivateGuardian(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	legacyPath := filepath.Join(root, "docs", "tasks", ".to.lock")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	unlock, err := LockWorkspace("to", root, legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	unlock()

	guardianPath, err := applylock.GuardianPath(filepath.Join(root, ".git"), legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(guardianPath); err != nil {
		t.Fatalf("Git-private guardian = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "docs", ".homonto")); !os.IsNotExist(err) {
		t.Fatalf("workflow records metadata artifact = %v", err)
	}
}

func TestLockChangeNames_ExcludesBothWorkflowCreators(t *testing.T) {
	dir := t.TempDir()
	first, err := LockChangeNames(dir)
	if err != nil {
		t.Fatalf("first name lock: %v", err)
	}
	if _, err := LockChangeNames(dir); err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("second name lock = %v, want in-progress error", err)
	}
	first()
	second, err := LockChangeNames(dir)
	if err != nil {
		t.Fatalf("name lock after release: %v", err)
	}
	second()
}
