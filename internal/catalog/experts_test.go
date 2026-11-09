package catalog

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	embedded "github.com/Novirmor/homonto/catalog"
)

func TestBuiltinExpertContracts(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"architecture", "ui-ux", "database", "security"} {
		fw, ok := c.Framework(name)
		if !ok || fw.Kind != "expert" || fw.Expert == nil {
			t.Fatalf("invalid builtin %s", name)
		}
		agents, err := c.ExpandSubagents([]string{name})
		if err != nil || len(agents) != 2 {
			t.Fatalf("expert agents: %+v, %v", agents, err)
		}
		for _, p := range fw.Commands {
			data, err := fs.ReadFile(embedded.FS, p)
			if err != nil || parseEmbeddedFrontmatter(t, p, data)["agent"] != "homonto" {
				t.Fatalf("expert command %s lacks coordinator: %v", p, err)
			}
		}
	}
}

func TestExpertManifestRejectsBrokenOrPrivilegedRoles(t *testing.T) {
	manifest := `name='company'
kind='expert'
version='0.1.0'
description='Company expertise'
[expert]
skill='company'
analyst='company-analyst'
reviewer='company-reviewer'
[skills]
company='skills/company'
[subagents]
company-analyst='agents/analyst.md'
company-reviewer='agents/reviewer.md'
`
	worker := "---\ndescription: Advisor\nhomonto:\n  read_only: true\n  bash: false\n  spawn: []\n  dialogs: false\n---\nReview evidence.\n"
	for _, tc := range []struct{ name, manifest, worker string }{
		{"missing skill", strings.Replace(manifest, "skill='company'", "skill='missing'", 1), worker},
		{"same reviewer", strings.Replace(manifest, "reviewer='company-reviewer'", "reviewer='company-analyst'", 1), worker},
		{"wildcard agent", strings.ReplaceAll(manifest, "company-analyst", "company-*"), worker},
		{"writer", manifest, strings.Replace(worker, "read_only: true", "read_only: false", 1)},
		{"shell", manifest, strings.Replace(worker, "bash: false", "bash: true", 1)},
		{"spawn", manifest, strings.Replace(worker, "spawn: []", "spawn: [general]", 1)},
		{"native permissions", manifest, strings.Replace(worker, "homonto:", "permissions: []\nhomonto:", 1)},
		{"engine dependency", manifest + "\n[dependencies]\nframeworks=['onto']\n", worker},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := fstest.MapFS{
				"framework.toml":          {Data: []byte(tc.manifest)},
				"skills/company/SKILL.md": {Data: []byte("company skill")},
				"agents/analyst.md":       {Data: []byte(tc.worker)},
				"agents/reviewer.md":      {Data: []byte(worker)},
			}
			if _, err := NewWithLocal(map[string]fs.FS{"company": local}); err == nil {
				t.Fatal("invalid expert accepted")
			}
		})
	}
}
