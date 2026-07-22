package ontostate

import (
	"os"
	"path/filepath"
	"testing"
)

// deriveFixture writes the named files (path → content) into a temp change
// dir and returns it.
func deriveFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestDeriveWorkingPhase_EvidenceTable exercises the dispatcher's evidence
// table row by row — first match from the top wins.
func TestDeriveWorkingPhase_EvidenceTable(t *testing.T) {
	cases := []struct {
		name  string
		st    State
		files map[string]string
		want  string
	}{
		{"archived wins over everything", State{Archived: true, Phase: "close", Workflow: "full"},
			map[string]string{"design.md": "Status: Under revision\n"}, "done"},
		{"under revision wins over confirmed evidence", State{Phase: "build", Workflow: "full"},
			map[string]string{"proposal.md": "p", "design.md": "Status: Under revision\n", "tasks.md": "- [x] a\n", "verification.md": "Result: pass\n"}, "design"},
		{"verification pass derives close", State{Phase: "verify", Workflow: "full"},
			map[string]string{"proposal.md": "p", "design.md": "Status: Confirmed\n", "tasks.md": "- [x] a\n", "verification.md": "Result: pass\n"}, "close"},
		{"all tasks checked derives verify", State{Phase: "build", Workflow: "full"},
			map[string]string{"proposal.md": "p", "design.md": "Status: Confirmed\n", "tasks.md": "- [x] a\n- [x] b\n"}, "verify"},
		{"confirmed design derives build", State{Phase: "design", Workflow: "full"},
			map[string]string{"proposal.md": "p", "design.md": "Status: Confirmed\n", "tasks.md": "- [ ] a\n"}, "build"},
		{"preset workspace derives build", State{Phase: "open", Workflow: "fix"},
			map[string]string{"proposal.md": "p", "tasks.md": "- [ ] a\n"}, "build"},
		{"full incomplete tasks derive design", State{Phase: "build", Workflow: "full"},
			map[string]string{"proposal.md": "p", "tasks.md": "- [ ] a\n"}, "design"},
		{"full unconfirmed design draft derives design", State{Phase: "build", Workflow: "full"},
			map[string]string{"proposal.md": "p", "design.md": "Status: Draft\n"}, "design"},
		{"full proposal-only trusts claimed open", State{Phase: "open", Workflow: "full"},
			map[string]string{"proposal.md": "p"}, "open"},
		{"full proposal-only trusts claimed design", State{Phase: "design", Workflow: "full"},
			map[string]string{"proposal.md": "p"}, "design"},
		{"full proposal-only with late claim falls to open", State{Phase: "verify", Workflow: "full"},
			map[string]string{"proposal.md": "p"}, "open"},
		{"missing proposal derives open", State{Phase: "design", Workflow: "full"},
			map[string]string{}, "open"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := deriveFixture(t, tc.files)
			if got := DeriveWorkingPhase(dir, tc.st); got != tc.want {
				t.Errorf("DeriveWorkingPhase = %q, want %q", got, tc.want)
			}
		})
	}
}
