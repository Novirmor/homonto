package engine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	embedded "github.com/Novirmor/homonto/catalog"
)

func expertModels(names ...string) string {
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "\n[subagents.%s.opencode]\nmodel='provider/model'\n", name)
	}
	return b.String()
}

func expertWrite(t *testing.T, root, name, text string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func expertRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func expertApply(t *testing.T, home, root, doc string) *Engine {
	t.Helper()
	expertWrite(t, root, "homonto.toml", doc)
	e, err := Build(context.Background(), filepath.Join(root, "homonto.toml"), home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestExpertOnlyApplyNonGitAndRepairInventory(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	doc := "[experts.architecture]\nsource='builtin:architecture'\nscope='project'\n" + expertModels("homonto", "expert-architecture", "expert-architecture-reviewer")
	e := expertApply(t, home, root, doc)
	for _, p := range []string{".git", "docs/changes", "docs/tasks"} {
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Fatalf("expert created lifecycle surface %s: %v", p, err)
		}
	}
	coordinator := expertRead(t, filepath.Join(e.SubagentDir(), "homonto.opencode.md"))
	if !strings.Contains(coordinator, `"expert-architecture": allow`) || strings.Contains(coordinator, `"onto-implementer": allow`) {
		t.Fatalf("coordinator dispatch not limited to installed roles:\n%s", coordinator)
	}
	if !strings.Contains(coordinator, `"skill": "expert-architecture"`) {
		t.Fatal("missing runtime inventory")
	}
	ref := filepath.Join(e.CatalogDir(), "expert", "references", "experts.md")
	if !strings.Contains(expertRead(t, ref), "expert-architecture") {
		t.Fatal("missing skill inventory")
	}
	if e.CatalogNeedsMaterialize() {
		t.Fatal("unchanged expert apply is not idempotent")
	}
	if err := os.Remove(ref); err != nil {
		t.Fatal(err)
	}
	if !e.CatalogNeedsMaterialize() {
		t.Fatal("deleted inventory not detected")
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(expertRead(t, ref), "expert-architecture") {
		t.Fatal("inventory not repaired")
	}
}

func TestLocalExpertCompositionUpdateAndRemoval(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	expertWrite(t, root, "custom/framework.toml", `name='company'
kind='expert'
version='0.1.0'
description='Company design conventions'
[expert]
skill='company-design'
analyst='company-advisor'
reviewer='company-reviewer'
[skills]
company-design='skills/design'
[subagents]
company-advisor='agents/advisor.md'
company-reviewer='agents/reviewer.md'
`)
	expertWrite(t, root, "custom/skills/design/SKILL.md", "---\nname: company-design\ndescription: Company conventions\n---\nOriginal guidance\n")
	worker := "---\ndescription: Company expert\nhomonto:\n  read_only: true\n  bash: false\n  dialogs: false\n  spawn: []\n---\nUse company-design; return advice to the parent.\n"
	for _, name := range []string{"advisor", "reviewer"} {
		expertWrite(t, root, "custom/agents/"+name+".md", worker)
	}
	base := "[engines.no]\nsource='builtin:no'\nscope='project'\n" + expertModels("no-implementer", "no-reviewer", "homonto")
	doc := base + "\n[experts.company]\nsource='local:./custom'\nscope='project'\n" + expertModels("company-advisor", "company-reviewer")
	e := expertApply(t, home, root, doc)
	advisor := expertRead(t, filepath.Join(e.SubagentDir(), "company-advisor.opencode.md"))
	for _, want := range []string{"edit: deny", "bash: deny", "task: deny", "homonto_github_publish: deny", "external_directory:"} {
		if !strings.Contains(advisor, want) {
			t.Fatalf("BYO worker lacks %q:\n%s", want, advisor)
		}
	}
	coordinator := expertRead(t, filepath.Join(e.SubagentDir(), "homonto.opencode.md"))
	for _, name := range []string{"company-advisor", "company-reviewer", "no-implementer", "no-reviewer"} {
		if !strings.Contains(coordinator, `"`+name+`": allow`) {
			t.Fatalf("missing installed worker %s", name)
		}
	}
	expertWrite(t, root, "custom/skills/design/SKILL.md", "---\nname: company-design\ndescription: Updated conventions\n---\nUpdated guidance\n")
	e = expertApply(t, home, root, doc)
	if !strings.Contains(expertRead(t, filepath.Join(e.CatalogDir(), "company-design", "SKILL.md")), "Updated guidance") {
		t.Fatal("local expert content stayed stale")
	}
	expertApply(t, home, root, strings.ReplaceAll(base, expertModels("homonto"), ""))
	for _, p := range []string{".opencode/agents/company-advisor.md", ".homonto/catalog/skills/company-design", ".homonto/catalog/subagents/homonto.opencode.md"} {
		if _, err := os.Lstat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Fatalf("removed expert resource survives %s: %v", p, err)
		}
	}
	if !strings.Contains(expertRead(t, filepath.Join(root, "custom/skills/design/SKILL.md")), "Updated guidance") {
		t.Fatal("BYO source modified")
	}
}

func TestExternalExpertReferencePreservesNativeFiles(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	native := "---\ndescription: Native advisor\nmode: subagent\n---\nUser-owned content\n"
	expertWrite(t, root, ".opencode/agents/team/advisor.md", native)
	base := toFrameworkTOML
	e := expertApply(t, home, root, base+"\n[experts.own]\nsource='external:team/advisor'\nscope='project'\ndescription='Own expert'\n")
	if !strings.Contains(expertRead(t, filepath.Join(e.SubagentDir(), "homonto.opencode.md")), `"team/advisor": allow`) {
		t.Fatal("external reference not dispatchable")
	}
	for _, keys := range e.State.Managed {
		for key := range keys {
			if strings.Contains(key, "team/advisor") {
				t.Fatalf("native agent adopted into state: %s", key)
			}
		}
	}
	e = expertApply(t, home, root, base)
	if got := expertRead(t, filepath.Join(root, ".opencode/agents/team/advisor.md")); got != native {
		t.Fatal("native file modified")
	}
	if strings.Contains(expertRead(t, filepath.Join(e.SubagentDir(), "homonto.opencode.md")), `"team/advisor": allow`) {
		t.Fatal("removed native reference remains dispatchable")
	}
}

func TestAllBuiltinExpertsInstallTogether(t *testing.T) {
	var doc strings.Builder
	doc.WriteString(expertModels("homonto"))
	for _, domain := range []string{"architecture", "ui-ux", "database", "security"} {
		fmt.Fprintf(&doc, "\n[experts.%s]\nsource='builtin:%s'\nscope='project'\n", domain, domain)
		doc.WriteString(expertModels("expert-"+domain, "expert-"+domain+"-reviewer"))
	}
	e := expertApply(t, t.TempDir(), t.TempDir(), doc.String())
	for _, domain := range []string{"architecture", "ui-ux", "database", "security"} {
		for _, suffix := range []string{"", "-reviewer"} {
			worker := expertRead(t, filepath.Join(e.SubagentDir(), "expert-"+domain+suffix+".opencode.md"))
			for _, rule := range []string{"edit: deny", "bash: deny", "task: deny"} {
				if !strings.Contains(worker, rule) {
					t.Fatalf("%s lacks %s", domain+suffix, rule)
				}
			}
		}
	}
	if e.CatalogNeedsMaterialize() {
		t.Fatal("combined expert install is not idempotent")
	}
}

func TestExpertMetadataOnlyRefresh(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	doc := "[experts.own]\nsource='external:team/advisor'\nscope='project'\ndescription='Original expertise'\n" + expertModels("homonto")
	e := expertApply(t, home, root, doc)
	old := e.State.RenderFingerprintRecorded()
	doc = strings.ReplaceAll(doc, "Original expertise", "Updated expertise")
	expertWrite(t, root, "homonto.toml", doc)
	e, err := Build(context.Background(), filepath.Join(root, "homonto.toml"), home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	if !e.CatalogNeedsMaterialize() {
		t.Fatal("metadata-only edit was missed")
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	if e.State.RenderFingerprintRecorded() == old {
		t.Fatal("metadata absent from fingerprint")
	}
	for _, p := range []string{
		filepath.Join(e.CatalogDir(), "expert", "references", "experts.md"),
		filepath.Join(e.CatalogDir(), "homonto", "references", "experts.md"),
		filepath.Join(e.SubagentDir(), "homonto.opencode.md"),
	} {
		text := expertRead(t, p)
		if !strings.Contains(text, "Updated expertise") || strings.Contains(text, "Original expertise") {
			t.Fatalf("stale inventory in %s", p)
		}
	}
}

func TestPinnedRemoteExpert(t *testing.T) {
	// Repackage the real architecture expert as a remote-only package. This
	// exercises the same advisory contract through digest verification and reload.
	var archive bytes.Buffer
	zw := gzip.NewWriter(&archive)
	tw := tar.NewWriter(zw)
	for _, name := range []string{"frameworks/architecture/framework.toml", "skills/expert-architecture/SKILL.md", "skills/expert-architecture/references/design.md", "subagents/expert-architecture.md", "subagents/expert-architecture-reviewer.md", "commands/architecture.md"} {
		data, err := fs.ReadFile(embedded.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.ReplaceAll(string(data), "architecture", "remote-design")
		name = strings.ReplaceAll(name, "architecture", "remote-design")
		if strings.HasSuffix(name, "framework.toml") {
			name = "framework.toml"
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(t.TempDir(), "expert.tar.gz")
	if err := os.WriteFile(fixture, archive.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	validPin := pinFor(t, fixture).String()
	for _, tc := range []struct {
		name, pin    string
		missingModel bool
	}{
		{"valid", validPin, false},
		{"wrong digest", "sha256:" + strings.Repeat("0", 64), false},
		{"missing reviewer route", validPin, true},
		{"unknown tune-only agent", validPin, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			doc := fmt.Sprintf("[experts.remote-design]\nsource='remote:file://%s'\ndigest='%s'\nscope='project'\n", fixture, tc.pin) + expertModels("homonto", "expert-remote-design")
			if !tc.missingModel {
				doc += expertModels("expert-remote-design-reviewer")
			}
			if tc.name == "unknown tune-only agent" {
				doc += expertModels("uninstalled-advisor")
			}
			expertWrite(t, root, "homonto.toml", doc)
			e, err := Build(context.Background(), filepath.Join(root, "homonto.toml"), home, "homonto")
			if tc.name != "valid" {
				if err == nil {
					t.Fatal("invalid remote expert built successfully")
				}
				if _, statErr := os.Stat(filepath.Join(root, ".opencode", "agents", "homonto.md")); !os.IsNotExist(statErr) {
					t.Fatal("invalid expert projected files")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			skills, err := e.Cfg.ExpandedSkillEntriesForTool("opencode")
			if err != nil {
				t.Fatal(err)
			}
			for _, skill := range skills {
				for _, origin := range skill.Origins {
					if !strings.HasPrefix(origin.Source, "builtin:") {
						t.Fatalf("resource provenance exposes the remote locator: %+v", origin)
					}
				}
			}
			if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(expertRead(t, filepath.Join(e.SubagentDir(), "homonto.opencode.md")), `"expert-remote-design": allow`) {
				t.Fatal("remote expert not dispatchable")
			}
			if e.CatalogNeedsMaterialize() {
				t.Fatal("remote expert is not idempotent")
			}
		})
	}
}
