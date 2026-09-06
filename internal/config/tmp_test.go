package config

import (
	"strings"
	"testing"
)

// [tmp] is opt-in: absent table means the feature is off.
func TestTmpAbsent(t *testing.T) {
	c := loadTOML(t, `[frameworks.onto]
source = "builtin:onto"
scope = "project"
`+ontoFrameworkModels())
	if enabled, dir := c.ResolvedTmp(); enabled || dir != "" {
		t.Fatalf("absent [tmp] must resolve disabled, got (%v, %q)", enabled, dir)
	}
}

// A bare [tmp] enables with the default dir — presence, not a key, is the switch.
func TestTmpBareTableEnablesDefault(t *testing.T) {
	c := loadTOML(t, `[frameworks.onto]
source = "builtin:onto"
scope = "project"

[tmp]
`+ontoFrameworkModels())
	enabled, dir := c.ResolvedTmp()
	if !enabled || dir != DefaultTmpDir {
		t.Fatalf("bare [tmp] must enable with %q, got (%v, %q)", DefaultTmpDir, enabled, dir)
	}
}

func TestTmpCustomDirCleaned(t *testing.T) {
	c := loadTOML(t, `[tmp]
dir = "./scratch/tmp/"
`)
	if enabled, dir := c.ResolvedTmp(); !enabled || dir != "scratch/tmp" {
		t.Fatalf("dir must clean to scratch/tmp, got (%v, %q)", enabled, dir)
	}
}

func TestTmpValidation(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"unknown key", "[tmp]\npath = \".tmp\"\n", "unknown key"},
		{"absolute", "[tmp]\ndir = \"/tmp/scratch\"\n", "relative"},
		{"escape", "[tmp]\ndir = \"../scratch\"\n", "below the workspace root"},
		{"git", "[tmp]\ndir = \".git/scratch\"\n", ".git"},
		{"root", "[tmp]\ndir = \".\"\n", "dedicated subdirectory"},
		// Collisions with owned trees: a scratch dir here would be gitignored
		// or rebuilt over, taking real content with it (workflow root = docs
		// by default in these fixtures).
		{"workflow root", "[tmp]\ndir = \"docs\"\n", "workflow records root"},
		{"inside workflow root", "[tmp]\ndir = \"docs/tmp\"\n", "workflow records root"},
		{"custom workflow root", "[workflow]\nroot = \"records\"\n\n[tmp]\ndir = \"records\"\n", "workflow records root"},
		{"projection target", "[tmp]\ndir = \".opencode/scratch\"\n", "homonto owns"},
		{"local skills root", "[tmp]\ndir = \"homonto\"\n", "homonto owns"},
		{"materialized catalog", "[tmp]\ndir = \".homonto/catalog/x\"\n", "homonto owns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadDoc(t, tc.doc)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got: %v", tc.want, err)
			}
		})
	}
	// .homonto/tmp is legitimate: already covered by the scaffolded ignore.
	if err := loadDoc(t, "[tmp]\ndir = \".homonto/tmp\"\n"); err != nil {
		t.Fatalf(".homonto/tmp must validate: %v", err)
	}
}
