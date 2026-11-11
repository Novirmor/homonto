package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Novirmor/homonto/internal/config"
)

func TestNoSkepticInheritsReviewerModelAndVariant(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "homonto.toml")
	body := `[engines.no]
source = "builtin:no"
scope = "project"
[subagents.no-implementer.opencode]
model = "provider/implementer"
[subagents.no-reviewer.opencode]
model = "provider/reviewer"
variant = "high"
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{Cfg: cfg, ProjectRoot: root, Home: t.TempDir()}
	ctx := mustSubagentRenderContext(t, e)
	got := ctx["opencode"].Overrides["no-skeptic"]
	if got.Model != "provider/reviewer" || got.Variant != "high" {
		t.Fatalf("got %+v", got)
	}
}
