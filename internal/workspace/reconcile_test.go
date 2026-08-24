package workspace

import (
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

const (
	apiID  = identity.RepositoryID("6c6c1a71-8293-4da4-8f01-3456789abcde")
	docsID = identity.RepositoryID("7d7d2b82-93a4-4eb5-9012-456789abcdef")
)

func reconcileCfg() workspacecfg.Config {
	return workspacecfg.Config{
		SchemaVersion: 1,
		Workspace:     workspacecfg.Workspace{ID: wsID, Workflow: workspacecfg.WorkflowTask},
		Control:       workspacecfg.Control{ID: ctlID, Path: "."},
		Members: []workspacecfg.Member{
			{ID: ctlID, Path: ".", Kind: workspacecfg.KindGit},
			{ID: apiID, Path: "services/api", Kind: workspacecfg.KindGit},
			{ID: docsID, Path: "docs", Kind: workspacecfg.KindNonGit},
		},
	}
}

func TestReconcileHappyPath(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	initRepo(t, filepath.Join(root, "services", "api"))
	writeFile(t, filepath.Join(root, "docs", "package.json"), "{}\n")

	findings := Reconcile(root, reconcileCfg(), nil)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none", findings)
	}
}

func TestReconcileTable(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*workspacecfg.Config)
		onDisk   func(t *testing.T, root string)
		discover func(t *testing.T, root string) []Candidate
		want     []FindingKind
	}{
		{
			name: "missing member",
			onDisk: func(t *testing.T, root string) {
				initRepo(t, root)
				initRepo(t, filepath.Join(root, "services", "api"))
				// docs stays absent
			},
			want: []FindingKind{FindingMissingMember},
		},
		{
			name: "declared git but no git on disk",
			onDisk: func(t *testing.T, root string) {
				initRepo(t, root)
				writeFile(t, filepath.Join(root, "services", "api", "go.mod"), "module api\n")
				writeFile(t, filepath.Join(root, "docs", "package.json"), "{}\n")
			},
			want: []FindingKind{FindingKindMismatch},
		},
		{
			name: "declared non_git but git on disk",
			onDisk: func(t *testing.T, root string) {
				initRepo(t, root)
				initRepo(t, filepath.Join(root, "services", "api"))
				initRepo(t, filepath.Join(root, "docs"))
			},
			want: []FindingKind{FindingKindMismatch},
		},
		{
			name: "discovered kind disagrees with config",
			onDisk: func(t *testing.T, root string) {
				initRepo(t, root)
				initRepo(t, filepath.Join(root, "services", "api"))
				writeFile(t, filepath.Join(root, "docs", "package.json"), "{}\n")
			},
			discover: func(t *testing.T, root string) []Candidate {
				return []Candidate{{Path: CanonicalPathOf(t, filepath.Join(root, "docs")), Kind: workspacecfg.KindGit}}
			},
			want: []FindingKind{FindingScanMismatch},
		},
		{
			name: "member at control path with foreign id",
			mutate: func(c *workspacecfg.Config) {
				c.Members[0].ID = apiID
			},
			onDisk: func(t *testing.T, root string) {
				initRepo(t, root)
				initRepo(t, filepath.Join(root, "services", "api"))
				writeFile(t, filepath.Join(root, "docs", "package.json"), "{}\n")
			},
			want: []FindingKind{FindingControlIDMismatch},
		},
		{
			name: "control id at foreign path",
			mutate: func(c *workspacecfg.Config) {
				c.Members[1].ID = ctlID
			},
			onDisk: func(t *testing.T, root string) {
				initRepo(t, root)
				initRepo(t, filepath.Join(root, "services", "api"))
				writeFile(t, filepath.Join(root, "docs", "package.json"), "{}\n")
			},
			want: []FindingKind{FindingControlPathMismatch},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.onDisk(t, root)
			cfg := reconcileCfg()
			if tt.mutate != nil {
				tt.mutate(&cfg)
			}
			var discovered []Candidate
			if tt.discover != nil {
				discovered = tt.discover(t, root)
			}
			findings := Reconcile(root, cfg, discovered)
			got := make([]FindingKind, len(findings))
			for i, f := range findings {
				got[i] = f.Kind
			}
			if len(got) != len(tt.want) {
				t.Fatalf("findings = %+v, want kinds %v", findings, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("findings[%d] kind = %q, want %q (%+v)", i, got[i], tt.want[i], findings[i])
				}
			}
		})
	}
}
