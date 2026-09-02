package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/resourcepath"
	"github.com/noviopenworks/homonto/internal/state"
)

// TestProjectScopeLinksUnderProjectRoot: with scope=project, skill links land
// under <projectRoot>/.opencode/skills and nothing is created under home's
// ~/.config/opencode/skills.
func TestProjectScopeLinksUnderProjectRoot(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755)

	proj := t.TempDir()
	content := filepath.Join(proj, "content")
	os.MkdirAll(filepath.Join(content, "skills", "onto"), 0o755)

	a := New(home, content).WithProjectRoot(proj)
	st, _ := state.Load(t.TempDir())
	c := cfgWithSkills("project", "onto")

	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}

	projDst := filepath.Join(proj, ".opencode", "skills", "onto")
	// ADR 0026: a project link whose source moves with the repository carries
	// a RELATIVE target; assert the link resolves to the content, not its
	// spelling.
	if got, err := os.Readlink(projDst); err != nil {
		t.Fatalf("project link not created: %v", err)
	} else if resolved, err := filepath.EvalSymlinks(projDst); err != nil || resolved != filepath.Join(content, "skills", "onto") {
		t.Fatalf("project link resolves to %q (%v), want %q", resolved, err, filepath.Join(content, "skills", "onto"))
	} else if filepath.IsAbs(got) {
		t.Fatalf("same-domain project link must be relative, got %q", got)
	}
	if _, err := os.Lstat(filepath.Join(home, ".config", "opencode", "skills", "onto")); err == nil {
		t.Fatal("project scope must not create a link under home")
	}

	cs2, _ := a.Plan(c, st)
	for _, ch := range cs2.Changes {
		if ch.Action != "noop" {
			t.Fatalf("second plan must be all noop, got %s %s", ch.Action, ch.Key)
		}
	}
}

// TestScopeSwitchRelocatesLink: user -> project relocates the OpenCode link
// (home ~/.config/opencode/skills -> <proj>/.opencode/skills), pruning the old
// one; a foreign file at the inactive path is left untouched.
func TestScopeSwitchRelocatesLink(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755)
	proj := t.TempDir()
	content := filepath.Join(proj, "content")
	os.MkdirAll(filepath.Join(content, "skills", "onto"), 0o755)
	st, _ := state.Load(t.TempDir())

	// 1. Apply at user scope.
	aUser := New(home, content).WithProjectRoot(proj)
	cUser := cfgWithSkills("user", "onto")
	cs, err := aUser.Plan(cUser, st)
	if err != nil {
		t.Fatalf("user plan: %v", err)
	}
	if err := aUser.Apply(cUser, cs, noSecret(), st); err != nil {
		t.Fatalf("user apply: %v", err)
	}
	homeDst := filepath.Join(home, ".config", "opencode", "skills", "onto")
	if _, err := os.Readlink(homeDst); err != nil {
		t.Fatalf("user link missing: %v", err)
	}

	// 2. Switch to project scope; plan shows a relocate referencing home.
	aProj := New(home, content).WithProjectRoot(proj)
	cProj := cfgWithSkills("project", "onto")
	cs2, err := aProj.Plan(cProj, st)
	if err != nil {
		t.Fatalf("project plan: %v", err)
	}
	var reloc *adapter.Change
	for i := range cs2.Changes {
		if cs2.Changes[i].Key == "skill.onto" {
			reloc = &cs2.Changes[i]
		}
	}
	if reloc == nil || reloc.Action != "update" || !strings.Contains(reloc.Old, homeDst) {
		t.Fatalf("expected relocate (update) referencing %q, got %+v", homeDst, reloc)
	}

	// 3. Apply: project link created, home link pruned.
	if err := aProj.Apply(cProj, cs2, noSecret(), st); err != nil {
		t.Fatalf("project apply: %v", err)
	}
	if _, err := os.Readlink(filepath.Join(proj, ".opencode", "skills", "onto")); err != nil {
		t.Fatalf("project link not created: %v", err)
	}
	if _, err := os.Lstat(homeDst); err == nil {
		t.Fatal("home link must be pruned after switch — orphan left behind")
	}
}

// TestRemoveAndSwitchLeavesNoOrphan (verify round 1, FINDING 1): remove + scope
// switch in one apply must prune the link at its actual (inactive-scope) location.
func TestRemoveAndSwitchLeavesNoOrphan(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755)
	proj := t.TempDir()
	content := filepath.Join(proj, "content")
	os.MkdirAll(filepath.Join(content, "skills", "foo"), 0o755)
	st, _ := state.Load(t.TempDir())

	aU := New(home, content).WithProjectRoot(proj)
	cU := cfgWithSkills("user", "foo")
	cs, _ := aU.Plan(cU, st)
	if err := aU.Apply(cU, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	homeDst := filepath.Join(home, ".config", "opencode", "skills", "foo")
	if _, err := os.Lstat(homeDst); err != nil {
		t.Fatalf("setup: user link missing: %v", err)
	}

	aP := New(home, content).WithProjectRoot(proj)
	cP := cfgWithSkills("project")
	cs2, _ := aP.Plan(cP, st)
	if err := aP.Apply(cP, cs2, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(homeDst); err == nil {
		t.Fatal("orphan: user-location link survived remove+switch")
	}
}

// TestSkillAdoptRebuildsState (verify round 1, FINDING 2): a correct-but-unrecorded
// skill link is adopted into state on apply without touching the link.
func TestSkillAdoptRebuildsState(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755)
	content := t.TempDir()
	os.MkdirAll(filepath.Join(content, "skills", "foo"), 0o755)
	src := filepath.Join(content, "skills", "foo")
	dst := filepath.Join(home, ".config", "opencode", "skills", "foo")
	os.MkdirAll(filepath.Dir(dst), 0o755)
	if err := os.Symlink(src, dst); err != nil {
		t.Fatal(err)
	}

	st, _ := state.Load(t.TempDir()) // empty state — simulates lost state.json
	a := New(home, content)
	c := cfgWithSkills("user", "foo")

	cs, _ := a.Plan(c, st)
	var found *adapter.Change
	for i := range cs.Changes {
		if cs.Changes[i].Key == "skill.foo" {
			found = &cs.Changes[i]
		}
	}
	if found == nil || found.Action != "adopt" {
		t.Fatalf("expected adopt for skill.foo, got %+v", found)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Get("opencode", "skill.foo"); !ok {
		t.Fatal("skill.foo not recorded after adopt")
	}
	if tgt, _ := os.Readlink(dst); tgt != src {
		t.Fatal("adopt must not change the on-disk link")
	}
}

// TestRelocationPruneLeavesForeignFile: a real file at the inactive path is not
// removed and does not error the apply.
func TestRelocationPruneLeavesForeignFile(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755)
	proj := t.TempDir()
	content := filepath.Join(proj, "content")
	os.MkdirAll(filepath.Join(content, "skills", "onto"), 0o755)
	// A user's own real file sits where a user-scope link would be — the prune
	// (this apply is project-scope) must leave it alone.
	homeSkills := filepath.Join(home, ".config", "opencode", "skills")
	os.MkdirAll(homeSkills, 0o755)
	foreign := filepath.Join(homeSkills, "onto")
	os.WriteFile(foreign, []byte("mine"), 0o644)

	st, _ := state.Load(t.TempDir())
	a := New(home, content).WithProjectRoot(proj)
	c := cfgWithSkills("project", "onto")
	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatalf("apply must not error on a foreign file at the inactive path: %v", err)
	}
	if b, err := os.ReadFile(foreign); err != nil || string(b) != "mine" {
		t.Fatalf("foreign file was modified or removed: %v %q", err, string(b))
	}
}

// TestMixedScopesProjectIndependently locks the headline per-resource-scope
// capability: two skills declared with DIFFERENT scopes in a single apply (one
// user, one project) must each link into their own scope's directory, with
// neither clobbering the other's location. This is the regression-prone path —
// a global per-config scope flag would route both to one place.
func TestMixedScopesProjectIndependently(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755)

	proj := t.TempDir()
	content := filepath.Join(proj, "content")
	os.MkdirAll(filepath.Join(content, "skills", "a"), 0o755)
	os.MkdirAll(filepath.Join(content, "skills", "b"), 0o755)

	a := New(home, content).WithProjectRoot(proj)
	st, _ := state.Load(t.TempDir())
	c := &config.Config{Skills: map[string]config.Resource{
		"a": {Source: "local:a", Scope: "user"},
		"b": {Source: "local:b", Scope: "project"},
	}}

	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}

	userDir := resourcepath.Dir(resourcepath.Skill, "opencode", "user", home, proj)
	projDir := resourcepath.Dir(resourcepath.Skill, "opencode", "project", home, proj)
	linkA := filepath.Join(userDir, "a")
	linkB := filepath.Join(projDir, "b")

	// User scope stays absolute (ADR 0026: $HOME does not move with the
	// repo); project scope in the same domain goes relative and must resolve.
	if got, err := os.Readlink(linkA); err != nil || got != filepath.Join(content, "skills", "a") {
		t.Fatalf("user-scope skill a not linked at %s: %v %s", linkA, err, got)
	}
	if got, err := os.Readlink(linkB); err != nil || filepath.IsAbs(got) {
		t.Fatalf("project-scope skill b must carry a relative target at %s: %v %s", linkB, err, got)
	}
	if resolved, err := filepath.EvalSymlinks(linkB); err != nil || resolved != filepath.Join(content, "skills", "b") {
		t.Fatalf("project-scope skill b does not resolve to content: %q (%v)", resolved, err)
	}
	if _, err := os.Lstat(filepath.Join(userDir, "b")); err == nil {
		t.Fatal("user dir must NOT contain project-scope skill b")
	}
	if _, err := os.Lstat(filepath.Join(projDir, "a")); err == nil {
		t.Fatal("project dir must NOT contain user-scope skill a")
	}

	// Idempotent.
	cs2, _ := a.Plan(c, st)
	for _, ch := range cs2.Changes {
		if ch.Action != "noop" {
			t.Fatalf("second plan must be all noop, got %s %s", ch.Action, ch.Key)
		}
	}
}
