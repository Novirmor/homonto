package catalog

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	embedded "github.com/Novirmor/homonto/catalog"
)

// contractRoot is a local schema 2 package root with one skill and one command.
func contractRoot(manifest string) fstest.MapFS {
	return fstest.MapFS{
		"framework.toml":         {Data: []byte(manifest)},
		"skills/entry/SKILL.md":  {Data: []byte("---\nname: entry\n---\nentry")},
		"commands/entry.md":      {Data: []byte("entry command")},
		"subagents/worker.md":    {Data: []byte("---\nname: worker\n---\nworker")},
		"plugins/extra/index.ts": {Data: []byte("export {}")},
	}
}

const engineHeader = "manifest_schema = 2\nname = \"pkg\"\nkind = \"engine\"\nversion = \"0.1.0\"\n[skills]\nentry = \"skills/entry\"\n"

const statelessEngine = engineHeader + `[engine]
lifecycle = "stateless"
entry = ["entry"]
operations = ["start", "resume"]
stages = ["work"]
`

func loadContract(t *testing.T, manifest string) (*Catalog, error) {
	t.Helper()
	return LoadWithLocal(embeddedForTest(t), map[string]fs.FS{"pkg": contractRoot(manifest)})
}

func TestContractAcceptsMinimalPackagesOfEachKind(t *testing.T) {
	for name, manifest := range map[string]string{
		"stateless engine": statelessEngine,
		"native engine": engineHeader + `[engine]
lifecycle = "native"
binary = "pkgctl"
entry = ["entry"]
operations = ["start", "resume", "integration-check", "release"]
stages = ["plan", "build"]
coordinator = "shared"
[requires]
harness = ["skills", "subagents"]
`,
		"workflow": `manifest_schema = 2
name = "pkg"
kind = "workflow"
version = "0.1.0"
[workflow]
coordinator = "shared"
entry = ["entry"]
[commands]
entry = "commands/entry.md"
`,
		"service": `manifest_schema = 2
name = "pkg"
kind = "service"
version = "0.1.0"
[service]
id = "tracker"
operations = ["read", "publish"]
transport = "tracker-cli"
credentials = [{ kind = "env", name = "TRACKER_TOKEN" }, { kind = "cli", check = "tracker-cli auth status" }]
[permissions.coordinator]
bash_ask = ["tracker-cli issue delete*"]
[permissions.workers]
bash_deny = ["tracker-cli *"]
`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadContract(t, manifest); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestContractRejectsInvalidManifests(t *testing.T) {
	for _, tc := range []struct {
		name, manifest, want string
	}{
		{"missing kind", "manifest_schema = 2\nname = \"pkg\"\nversion = \"0.1.0\"\n", "kind is required"},
		{"unknown kind", "manifest_schema = 2\nname = \"pkg\"\nkind = \"agent\"\nversion = \"0.1.0\"\n", `unknown kind "agent"`},
		{"missing version", "manifest_schema = 2\nname = \"pkg\"\nkind = \"engine\"\n", "version is required"},
		{"engine without table", engineHeader, "has no [engine] table"},
		{"misspelled key", statelessEngine + "stagez = []\n", "unknown manifest fields"},
		{"wrong kind table", statelessEngine + "[workflow]\nentry = []\n", `declares an [workflow] table`},
		{"no lifecycle", engineHeader + "[engine]\nentry = [\"entry\"]\noperations = [\"start\", \"resume\"]\nstages = [\"a\"]\n", "lifecycle is required"},
		{"native without binary", strings.Replace(statelessEngine, `"stateless"`, `"native"`, 1), "requires the binary"},
		{"stateless with binary", strings.Replace(statelessEngine, "entry = [", "binary = \"x\"\nentry = [", 1), "ships no lifecycle binary"},
		{"undeclared entry", strings.Replace(statelessEngine, `entry = ["entry"]`, `entry = ["ghost"]`, 1), `entry skill "ghost" is not declared`},
		{"missing resume", strings.Replace(statelessEngine, `["start", "resume"]`, `["start"]`, 1), `must include "resume"`},
		{"unknown operation", strings.Replace(statelessEngine, `["start", "resume"]`, `["start", "resume", "merge"]`, 1), `unknown value "merge"`},
		{"no stages", strings.Replace(statelessEngine, `stages = ["work"]`, `stages = []`, 1), "stages must name at least one"},
		{"bad stage", strings.Replace(statelessEngine, `["work"]`, `["Work"]`, 1), `stage "Work" is not an identifier`},
		{"duplicate stage", strings.Replace(statelessEngine, `["work"]`, `["work", "work"]`, 1), `stage "work" is listed twice`},
		{"own coordinator", strings.Replace(statelessEngine, "stages", "coordinator = \"mine\"\nstages", 1), `coordinator "mine" is not supported`},
		{"declares shared agent", statelessEngine + "[subagents]\nhomonto = \"subagents/worker.md\"\n", `shared coordinator's subagent "homonto"`},
		{"unknown harness capability", statelessEngine + "[requires]\nharness = [\"telepathy\"]\n", `unknown harness capability "telepathy"`},
		{"engine contributes permissions", statelessEngine + "[permissions.workers]\nbash_deny = [\"x\"]\n", "only a service may contribute"},
		{"workflow without entry", "manifest_schema = 2\nname = \"pkg\"\nkind = \"workflow\"\nversion = \"0.1.0\"\n[workflow]\n", "entry must name at least one entry command"},
		{"service draft without runtime", "manifest_schema = 2\nname = \"pkg\"\nkind = \"service\"\nversion = \"0.1.0\"\n[service]\nid = \"t\"\noperations = [\"draft\"]\ntransport = \"t\"\n", "declare runtime"},
		{"service unknown runtime", "manifest_schema = 2\nname = \"pkg\"\nkind = \"service\"\nversion = \"0.1.0\"\n[service]\nid = \"t\"\nruntime = \"jira\"\noperations = [\"read\"]\ntransport = \"t\"\n", `runtime "jira" is not a provider`},
		{"service with subagent", "manifest_schema = 2\nname = \"pkg\"\nkind = \"service\"\nversion = \"0.1.0\"\n[service]\nid = \"t\"\noperations = [\"read\"]\ntransport = \"t\"\n[subagents]\nworker = \"subagents/worker.md\"\n", "ships no subagents"},
		{"service bad env", "manifest_schema = 2\nname = \"pkg\"\nkind = \"service\"\nversion = \"0.1.0\"\n[service]\nid = \"t\"\noperations = [\"read\"]\ntransport = \"t\"\ncredentials = [{ kind = \"env\", name = \"lower\" }]\n", "is not an environment variable name"},
		{"service multiline rule", "manifest_schema = 2\nname = \"pkg\"\nkind = \"service\"\nversion = \"0.1.0\"\n[service]\nid = \"t\"\noperations = [\"read\"]\ntransport = \"t\"\n[permissions.coordinator]\nbash_ask = [\"a\\nb\"]\n", "single-line pattern"},
		{"unknown default service", statelessEngine + "[requires.services]\ntracker = \"nope\"\n", `default package "nope", which is not a known package`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadContract(t, tc.manifest)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestContractSharedCoordinatorComesFromBase(t *testing.T) {
	c, err := loadContract(t, strings.Replace(statelessEngine, "stages", "coordinator = \"shared\"\nstages", 1))
	if err != nil {
		t.Fatal(err)
	}
	fw, _ := c.Framework("pkg")
	if !fw.SharedCoordinator {
		t.Fatal("SharedCoordinator not recorded")
	}
	agents, err := c.ExpandSubagents([]string{"pkg"})
	if err != nil {
		t.Fatal(err)
	}
	skills, err := c.Expand([]string{"pkg"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(agents, func(e ExpandedSubagent) bool { return e.Name == SharedCoordinatorAgent }) ||
		!slices.ContainsFunc(skills, func(e ExpandedSkill) bool { return e.Name == SharedCoordinatorSkill }) {
		t.Fatalf("shared coordinator not expanded: agents=%v skills=%v", agents, skills)
	}
	base, _, err := embeddedForTestCatalog(t).SubagentContent(SharedCoordinatorAgent)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := c.SubagentContent(SharedCoordinatorAgent)
	if err != nil || string(got) != string(base) {
		t.Fatalf("shared coordinator content did not come from the base catalog: %v", err)
	}
}

func TestContractSharedCoordinatorNeedsABaseThatProvidesIt(t *testing.T) {
	_, err := LoadWithLocal(baseFS(), map[string]fs.FS{"pkg": contractRoot(strings.Replace(statelessEngine, "stages", "coordinator = \"shared\"\nstages", 1))})
	if err == nil || !strings.Contains(err.Error(), "does not provide it") {
		t.Fatalf("err = %v", err)
	}
}

func TestContractLegacyManifestsStayResourceBundles(t *testing.T) {
	c, err := LoadWithLocal(baseFS(), map[string]fs.FS{"pkg": contractRoot("name = \"pkg\"\nversion = \"0.1.0\"\nunknown_key = 1\n[skills]\nentry = \"skills/entry\"\n")})
	if err != nil {
		t.Fatal(err)
	}
	fw, _ := c.Framework("pkg")
	if fw.ManifestSchema != 0 || fw.Engine != nil || fw.Workflow != nil || fw.Service != nil {
		t.Fatalf("legacy manifest carries a contract: %+v", fw)
	}
}

func TestBuiltinPackagesConformToTheirContract(t *testing.T) {
	c := embeddedForTestCatalog(t)
	want := map[string]string{
		"onto": KindEngine, "to": KindEngine, "no": KindEngine,
		"h-workflow-gh": KindWorkflow, "h-workflow-gl": KindWorkflow, "h-workflow-bb": KindWorkflow,
	}
	for name, kind := range want {
		fw, ok := c.Framework(name)
		if !ok {
			t.Fatalf("builtin %s missing", name)
		}
		if fw.ManifestSchema != ContractManifestSchema || fw.Kind != kind {
			t.Errorf("%s: schema %d kind %q, want schema %d kind %q", name, fw.ManifestSchema, fw.Kind, ContractManifestSchema, kind)
		}
		if len(fw.HarnessRequirements) == 0 {
			t.Errorf("%s declares no harness requirements", name)
		}
	}
	for name, lifecycle := range map[string]string{"onto": LifecycleNative, "to": LifecycleNative, "no": LifecycleStateless} {
		fw, _ := c.Framework(name)
		if fw.Engine.Lifecycle != lifecycle {
			t.Errorf("%s lifecycle %q, want %q", name, fw.Engine.Lifecycle, lifecycle)
		}
		if fw.Engine.OwnsLifecycle() != (fw.Engine.Binary != "") {
			t.Errorf("%s: lifecycle ownership and binary disagree", name)
		}
	}
	// no ships no coordinator (ADR 0067); every other builtin shares it.
	for name := range want {
		fw, _ := c.Framework(name)
		if fw.SharedCoordinator != (name != "no") {
			t.Errorf("%s SharedCoordinator = %v", name, fw.SharedCoordinator)
		}
	}
}

func embeddedForTest(t *testing.T) fs.FS {
	t.Helper()
	return embedded.FS
}

func embeddedForTestCatalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := Load(embedded.FS)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
