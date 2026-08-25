package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/guard"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

func mustRepoID(t *testing.T) identity.RepositoryID {
	t.Helper()
	id, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("NewRepositoryID: %v", err)
	}
	return id
}

func mustWorkspaceID(t *testing.T) identity.WorkspaceID {
	t.Helper()
	id, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("NewWorkspaceID: %v", err)
	}
	return id
}

// fixture builds an environment over a directory tree without needing git
// or a database: everything tested here is derived from the manifest and
// the filesystem.
func fixture(t *testing.T) (*Environment, workspacecfg.Config, string) {
	t.Helper()
	root := t.TempDir()
	controlID := mustRepoID(t)
	apiID := mustRepoID(t)
	assetsID := mustRepoID(t)
	for _, dir := range []string{
		"services/api/src", "services/api/vendor/github.com/x",
		"services/api/gen", "services/api/.git", "services/api/.homonto",
		"assets",
	} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "services", "api", "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "logo.txt"), []byte("logo\n"), 0o644); err != nil {
		t.Fatalf("write logo: %v", err)
	}
	cfg := workspacecfg.Config{
		SchemaVersion: workspacecfg.CurrentSchemaVersion,
		Workspace:     workspacecfg.Workspace{ID: mustWorkspaceID(t), Workflow: workspacecfg.WorkflowTask},
		Control:       workspacecfg.Control{ID: controlID, Path: "."},
		Members: []workspacecfg.Member{
			{
				ID: apiID, Path: "services/api", Kind: workspacecfg.KindGit,
				Paths: &workspacecfg.PathClasses{
					Vendored:  []string{"vendor/**"},
					Generated: []string{"gen/**"},
				},
			},
			{ID: assetsID, Path: "assets", Kind: workspacecfg.KindNonGit},
		},
	}
	env := &Environment{root: root, cfg: cfg, lookup: func(string) (string, bool) { return "", false }}
	return env, cfg, root
}

func TestNewEnvironmentRequiresAnAbsoluteRoot(t *testing.T) {
	if _, err := NewEnvironment("relative", workspacecfg.Config{}, nil, nil, nil, ""); err == nil {
		t.Fatal("NewEnvironment(relative) = nil error, want rejection")
	}
}

func TestMembersIncludeTheControlRepository(t *testing.T) {
	env, cfg, _ := fixture(t)
	members, err := env.Members(context.Background())
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("Members = %d, want the control plus two configured members", len(members))
	}
	if members[0].ID != cfg.Control.ID || members[0].Path != "." {
		t.Fatalf("the control repository is not first: %+v", members[0])
	}
	if members[1].Git != true || members[2].Git != false {
		t.Fatalf("member kinds are wrong: %+v", members)
	}
}

// TestWorkMembersExcludeTheControlRepository proves implementation work is
// not issued into the tree Homonto writes the record into.
func TestWorkMembersExcludeTheControlRepository(t *testing.T) {
	env, cfg, _ := fixture(t)
	work, err := env.workMembers(context.Background())
	if err != nil {
		t.Fatalf("workMembers: %v", err)
	}
	for _, m := range work {
		if m.ID == cfg.Control.ID {
			t.Fatal("implementation work was issued into the control repository")
		}
	}
	if len(work) != 2 {
		t.Fatalf("workMembers = %d, want the two non-control members", len(work))
	}
	// A workspace whose only member IS the control repository is the
	// exception: there, the code lives in it.
	solo := *env
	solo.cfg.Members = nil
	work, err = solo.workMembers(context.Background())
	if err != nil {
		t.Fatalf("workMembers(solo): %v", err)
	}
	if len(work) != 1 || work[0].ID != cfg.Control.ID {
		t.Fatalf("workMembers(solo) = %+v, want the control repository", work)
	}
}

// TestScopeExcludesStateAndClassifiedPaths proves the scope an assignment
// gets is explicit and never includes Git's or Homonto's own state.
func TestScopeExcludesStateAndClassifiedPaths(t *testing.T) {
	env, cfg, _ := fixture(t)
	members, err := env.Members(context.Background())
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	var api = members[1]
	if api.ID != cfg.Members[0].ID {
		t.Fatalf("expected the api member second, got %+v", api)
	}
	scope, err := env.scopeFor(api)
	if err != nil {
		t.Fatalf("scopeFor: %v", err)
	}
	got := map[string]bool{}
	for _, s := range scope {
		got[s] = true
	}
	for _, want := range []string{"src", "go.mod"} {
		if !got[want] {
			t.Errorf("scope %v does not include %q", scope, want)
		}
	}
	for _, forbidden := range []string{".git", ".homonto", "vendor", "gen"} {
		if got[forbidden] {
			t.Errorf("scope %v includes %q", scope, forbidden)
		}
	}
	if len(scope) == 0 {
		t.Fatal("an empty scope reads as unrestricted to the guard and must never be issued")
	}
}

// TestPartitionLeavesTheIsolationAreaToIsolate proves the ordering the
// worktree naming depends on.
func TestPartitionLeavesTheIsolationAreaToIsolate(t *testing.T) {
	env, _, _ := fixture(t)
	workID, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("NewWorkID: %v", err)
	}
	units, err := env.Partition(context.Background(), workID, []artifact.Item{
		{Index: 1, Text: "make login work"},
	})
	if err != nil {
		t.Fatalf("Partition: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("Partition = %d units, want one per work member", len(units))
	}
	for _, u := range units {
		if u.Root != "" {
			t.Fatalf("unit %q already carries an isolation area: %q", u.Label, u.Root)
		}
		if err := u.Validate(); err == nil {
			t.Fatalf("unit %q validated without a root; the engine must fill it in first", u.Label)
		}
		if len(u.Items) != 1 || u.Items[0] != 1 {
			t.Fatalf("unit %q addresses %v, want item 1", u.Label, u.Items)
		}
	}
	if units, err := env.Partition(context.Background(), workID, nil); err != nil || units != nil {
		t.Fatalf("Partition(no items) = %+v, %v, want nothing", units, err)
	}
}

// TestMemberKindComesFromTheManifest proves the diff observer is chosen by
// configuration, never by probing: a non-Git isolation area lives under
// the control repository's tree, where git would claim it.
func TestMemberKindComesFromTheManifest(t *testing.T) {
	env, cfg, _ := fixture(t)
	if !env.memberIsGit(cfg.Members[0].ID) {
		t.Error("the git member was not recognized")
	}
	if env.memberIsGit(cfg.Members[1].ID) {
		t.Error("the non-git member was treated as git")
	}
	if !env.memberIsGit(cfg.Control.ID) {
		t.Error("the control repository must be git-backed")
	}
	if env.memberIsGit(mustRepoID(t)) {
		t.Error("an unknown repository was treated as git")
	}
}

func TestFingerprintsMoveWithEachInput(t *testing.T) {
	env, _, _ := fixture(t)
	base, err := env.Fingerprints(context.Background())
	if err != nil {
		t.Fatalf("Fingerprints: %v", err)
	}
	if base.Membership == "" || base.PathClass == "" || base.CheckConfig == "" {
		t.Fatalf("baseline = %+v, want every digest recorded", base)
	}

	withMember := *env
	withMember.cfg.Members = append(append([]workspacecfg.Member(nil), env.cfg.Members...),
		workspacecfg.Member{ID: mustRepoID(t), Path: "services/web", Kind: workspacecfg.KindGit})
	moved, err := withMember.Fingerprints(context.Background())
	if err != nil {
		t.Fatalf("Fingerprints: %v", err)
	}
	if moved.Membership == base.Membership {
		t.Error("adding a member did not move the membership fingerprint")
	}

	withChecks := *env
	withChecks.cfg.Members = append([]workspacecfg.Member(nil), env.cfg.Members...)
	withChecks.cfg.Members[0].Verification = []workspacecfg.Check{
		{Name: "unit", Command: []string{"/bin/true"}},
	}
	moved, err = withChecks.Fingerprints(context.Background())
	if err != nil {
		t.Fatalf("Fingerprints: %v", err)
	}
	if moved.CheckConfig == base.CheckConfig {
		t.Error("adding a check did not move the check-configuration fingerprint")
	}
	if moved.Membership != base.Membership {
		t.Error("adding a check moved the membership fingerprint")
	}

	withPaths := *env
	withPaths.cfg.Members = append([]workspacecfg.Member(nil), env.cfg.Members...)
	withPaths.cfg.Members[0].Paths = &workspacecfg.PathClasses{Tests: []string{"**/*_test.go"}}
	moved, err = withPaths.Fingerprints(context.Background())
	if err != nil {
		t.Fatalf("Fingerprints: %v", err)
	}
	if moved.PathClass == base.PathClass {
		t.Error("changing the path classes did not move the path-class fingerprint")
	}
}

func TestParsePorcelainAndNameStatus(t *testing.T) {
	changes := parsePorcelain(" M src/login.go\x00?? src/new.go\x00 D src/gone.go\x00")
	if len(changes) != 3 {
		t.Fatalf("parsePorcelain = %+v, want three changes", changes)
	}
	want := map[string]guard.ChangeKind{
		"src/login.go": guard.ChangeModified,
		"src/new.go":   guard.ChangeAdded,
		"src/gone.go":  guard.ChangeDeleted,
	}
	for _, c := range changes {
		if want[c.Path] != c.Kind {
			t.Errorf("%s = %s, want %s", c.Path, c.Kind, want[c.Path])
		}
	}
	changes = parseNameStatus("M\x00src/login.go\x00A\x00src/new.go\x00D\x00src/gone.go\x00")
	if len(changes) != 3 {
		t.Fatalf("parseNameStatus = %+v, want three changes", changes)
	}
	for _, c := range changes {
		if want[c.Path] != c.Kind {
			t.Errorf("%s = %s, want %s", c.Path, c.Kind, want[c.Path])
		}
	}
}

func TestNormalizePath(t *testing.T) {
	for in, want := range map[string]string{
		"":               ".",
		".":              ".",
		"services/api":   "services/api",
		"services/api/":  "services/api",
		"./services/api": "services/api",
	} {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}
