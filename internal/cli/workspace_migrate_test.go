package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/workspacemigration"
)

var migrationAliases = []string{
	".github-private",
	"bff-service",
	"cash-service",
	"device-service",
	"external-merchant-api",
	"field-service",
	"file-service",
	"identity-service",
	"keycloak-facade",
	"merchant-service",
	"planogram-service",
	"portal-ui",
	"terminal-config-service",
	"warehouse-service",
}

var migrationConfigAliases = append([]string{"service-user"}, migrationAliases...)

type migrationCLIFixture struct {
	root     string
	config   string
	records  string
	manifest string
	repos    map[string]string
	bases    map[string]string
	input    workspacemigration.Manifest
}

func TestWorkspaceMigratePlanCLIInventoriesThreeLegacyRecordsReadOnly(t *testing.T) {
	f := newMigrationCLIFixture(t)
	before := migrationCLISnapshot(t, f)

	output, err := runMigrationCLI(f, "workspace", "migrate", "plan", "--manifest", f.manifest, "--json")
	if err != nil {
		t.Fatalf("workspace migrate plan: %v\n%s", err, output)
	}
	var plan workspacemigration.Plan
	if err := json.Unmarshal([]byte(output), &plan); err != nil {
		t.Fatalf("plan JSON: %v\n%s", err, output)
	}
	if plan.Status != "ready" || !plan.ReadOnly || plan.PlanHash == "" {
		t.Fatalf("plan summary = %+v", plan)
	}
	if plan.ManifestRequirements.Version != 1 || plan.ManifestRequirements.ControlOnlyAttestation != workspacemigration.ControlOnlyAttestation {
		t.Fatalf("manifest requirements = %+v", plan.ManifestRequirements)
	}
	if len(plan.Records) != 3 {
		t.Fatalf("records = %+v", plan.Records)
	}
	password := migrationCLIRecord(t, plan, "1dd2f21e")
	ci := migrationCLIRecord(t, plan, "eab6ef3d")
	abandoned := migrationCLIRecord(t, plan, "006ff3b4")
	if password.Framework != "onto" || password.Workflow != "full" || password.SchemaVersion != 3 || password.Phase != "open" || len(password.Sources) != 1 || password.Sources[0].Alias != "service-user" {
		t.Fatalf("password record = %+v", password)
	}
	if ci.Framework != "onto" || ci.Workflow != "full" || ci.SchemaVersion != 2 || ci.Phase != "build" || !ci.LegacyConfigSource || len(ci.Sources) != len(migrationAliases) {
		t.Fatalf("CI record = %+v", ci)
	}
	if abandoned.SchemaVersion != 1 || abandoned.Lifecycle != "retired" || !abandoned.LegacyConfigSource || abandoned.Name != ci.Name || abandoned.RelativePath != "changes/standardize-ci-ecr-pipelines-abandoned-006ff3b4" {
		t.Fatalf("abandoned record was reactivated, renamed, or collapsed: %+v", abandoned)
	}
	if !migrationCLIHasFingerprint(plan.Files, filepath.Join(f.records, "changes", "standardize-ci-ecr-pipelines", ".onto", "handoff.md"), "onto_evidence") {
		t.Fatal("historical .onto handoff was not fingerprinted")
	}
	if strings.Contains(output, "historical handoff") {
		t.Fatal("plan exposed record contents instead of fingerprints")
	}
	if after := migrationCLISnapshot(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("successful plan changed tree/index/marker/state bytes\nbefore: %#v\nafter:  %#v", before, after)
	}
	if _, err := os.Lstat(filepath.Join(f.root, ".homonto", "workflow-layout.json")); !os.IsNotExist(err) {
		t.Fatalf("plan created a layout marker: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.root, ".homonto", "worktrees.json")); !os.IsNotExist(err) {
		t.Fatalf("plan created a worktree registry: %v", err)
	}

	again, err := runMigrationCLI(f, "workspace", "migrate", "plan", "--manifest", f.manifest, "--json")
	if err != nil || again != output {
		t.Fatalf("plan output is not deterministic\nfirst: %s\nsecond: %s\nerror: %v", output, again, err)
	}
}

func TestWorkspaceMigratePlanCLIBlockedInputDoesNotMutate(t *testing.T) {
	f := newMigrationCLIFixture(t)
	f.input.Records[1].Sources[0].BaseRef = strings.Repeat("0", 40)
	writeMigrationCLIManifest(t, f)
	before := migrationCLISnapshot(t, f)

	output, err := runMigrationCLI(f, "workspace", "migrate", "plan", "--manifest", f.manifest, "--json")
	if err == nil {
		t.Fatalf("blocked plan succeeded: %s", output)
	}
	var plan workspacemigration.Plan
	if jsonErr := json.Unmarshal([]byte(output), &plan); jsonErr != nil {
		t.Fatalf("blocked plan did not emit JSON: %v\n%s", jsonErr, output)
	}
	if plan.Status != "blocked" || plan.PlanHash == "" || !migrationCLIHasBlocker(plan, "source_base_missing") {
		t.Fatalf("blocked plan = %+v", plan)
	}
	if after := migrationCLISnapshot(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("blocked plan changed tree/index/marker/state bytes\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestWorkspaceMigratePlanCLIRequiresJSON(t *testing.T) {
	f := newMigrationCLIFixture(t)
	before := migrationCLISnapshot(t, f)
	output, err := runMigrationCLI(f, "workspace", "migrate", "plan", "--manifest", f.manifest)
	if err == nil || !strings.Contains(err.Error(), "requires --json") || output != "" {
		t.Fatalf("plan without --json = output %q, error %v", output, err)
	}
	if after := migrationCLISnapshot(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("non-JSON refusal changed fixture\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func newMigrationCLIFixture(t *testing.T) *migrationCLIFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	root := filepath.Join(base, "control")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	repos := make(map[string]string, len(migrationConfigAliases))
	bases := make(map[string]string, len(migrationConfigAliases))
	for _, alias := range migrationConfigAliases {
		repo := filepath.Join(base, "sources", alias)
		migrationCLIGit(t, repo, "init", "-b", "main")
		migrationCLIGit(t, repo, "config", "user.name", "Migration CLI Test")
		migrationCLIGit(t, repo, "config", "user.email", "migration-cli@example.invalid")
		migrationCLIWrite(t, filepath.Join(repo, "tracked"), alias+"\n")
		migrationCLIGit(t, repo, "add", "tracked")
		migrationCLIGit(t, repo, "commit", "-m", "base")
		repos[alias] = repo
		bases[alias] = migrationCLIGitText(t, repo, "rev-parse", "HEAD")
	}
	config := filepath.Join(root, "homonto.toml")
	var configText strings.Builder
	configText.WriteString("schema_version = 2\n[workflow]\nroot = \".homonto-local\"\ngit = \"existing\"\n[worktrees]\ndir = \"../execution\"\n[repos]\n")
	for _, alias := range migrationConfigAliases {
		fmt.Fprintf(&configText, "%q = %q\n", alias, repos[alias])
	}
	migrationCLIWrite(t, config, configText.String())
	migrationCLIWrite(t, filepath.Join(root, ".homonto", "workflow-root"), ".homonto-local\n")
	records := filepath.Join(root, ".homonto-local")
	migrationCLIGit(t, records, "init", "-b", "main")
	migrationCLIGit(t, records, "config", "user.name", "Migration CLI Test")
	migrationCLIGit(t, records, "config", "user.email", "migration-cli@example.invalid")

	migrationCLIWrite(t, filepath.Join(records, "changes", "issue-52-server-password-length", "onto-state.yaml"), fmt.Sprintf("schema_version: 3\nchange: issue-52-server-password-length\nid: 1dd2f21e\nworkflow: full\nphase: open\nrepos: [service-user]\nrepo_mode: explicit\nrepo_bases:\n  service-user:\n    base_ref: %s\n    base_branch: main\n    git_common_dir: %q\n", bases["service-user"], filepath.Join(repos["service-user"], ".git")))
	var ciState strings.Builder
	ciState.WriteString("schema_version: 2\nchange: standardize-ci-ecr-pipelines\nid: eab6ef3d\nworkflow: full\nphase: build\nrepos:\n")
	for _, alias := range migrationAliases {
		fmt.Fprintf(&ciState, "  - %q\n", alias)
	}
	migrationCLIWrite(t, filepath.Join(records, "changes", "standardize-ci-ecr-pipelines", "onto-state.yaml"), ciState.String())
	migrationCLIWrite(t, filepath.Join(records, "changes", "standardize-ci-ecr-pipelines", ".onto", "handoff.md"), "historical handoff\n")
	migrationCLIWrite(t, filepath.Join(records, "changes", "standardize-ci-ecr-pipelines-abandoned-006ff3b4", "onto-state.yaml"), "schema_version: 1\nchange: standardize-ci-ecr-pipelines\nid: 006ff3b4\nphase: build\nabandoned: true\n")
	migrationCLIGit(t, records, "add", ".")
	migrationCLIGit(t, records, "commit", "-m", "legacy records")

	input := workspacemigration.Manifest{
		Version:                workspacemigration.ManifestVersion,
		ControlOnlyAttestation: workspacemigration.ControlOnlyAttestation,
		Records: []workspacemigration.ManifestRecord{
			{
				Path: "changes/issue-52-server-password-length",
				ID:   "1dd2f21e",
				Sources: []workspacemigration.ManifestSource{{
					Alias:        "service-user",
					BaseRef:      bases["service-user"],
					BaseBranch:   "main",
					GitCommonDir: filepath.Join(repos["service-user"], ".git"),
				}},
			},
			{
				Path:    "changes/standardize-ci-ecr-pipelines",
				ID:      "eab6ef3d",
				Sources: migrationCLISources(repos, bases),
			},
			{
				Path:    "changes/standardize-ci-ecr-pipelines-abandoned-006ff3b4",
				ID:      "006ff3b4",
				Sources: []workspacemigration.ManifestSource{},
			},
		},
	}
	fixture := &migrationCLIFixture{
		root:     root,
		config:   config,
		records:  records,
		manifest: filepath.Join(base, "migration-manifest.json"),
		repos:    repos,
		bases:    bases,
		input:    input,
	}
	writeMigrationCLIManifest(t, fixture)
	return fixture
}

func migrationCLISources(repos, bases map[string]string) []workspacemigration.ManifestSource {
	sources := make([]workspacemigration.ManifestSource, 0, len(migrationAliases))
	for _, alias := range migrationAliases {
		sources = append(sources, workspacemigration.ManifestSource{
			Alias:        alias,
			BaseRef:      bases[alias],
			BaseBranch:   "main",
			GitCommonDir: filepath.Join(repos[alias], ".git"),
		})
	}
	return sources
}

func runMigrationCLI(f *migrationCLIFixture, args ...string) (string, error) {
	cmd := NewRootCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(append(args, "--config", f.config))
	err := cmd.Execute()
	return output.String(), err
}

func migrationCLIGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func migrationCLIGitText(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}

func migrationCLIWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeMigrationCLIManifest(t *testing.T, f *migrationCLIFixture) {
	t.Helper()
	data, err := json.MarshalIndent(f.input, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	migrationCLIWrite(t, f.manifest, string(data)+"\n")
}

func migrationCLIRecord(t *testing.T, plan workspacemigration.Plan, id string) workspacemigration.Record {
	t.Helper()
	for _, record := range plan.Records {
		if record.ID == id {
			return record
		}
	}
	t.Fatalf("record %q not found in %+v", id, plan.Records)
	return workspacemigration.Record{}
}

func migrationCLIHasFingerprint(files []workspacemigration.FileFingerprint, path, classification string) bool {
	for _, file := range files {
		if file.Path == path && file.Classification == classification && file.SHA256 != "" {
			return true
		}
	}
	return false
}

func migrationCLIHasBlocker(plan workspacemigration.Plan, code string) bool {
	for _, blocker := range plan.Blockers {
		if blocker.Code == code {
			return true
		}
	}
	return false
}

func migrationCLISnapshot(t *testing.T, f *migrationCLIFixture) map[string]string {
	t.Helper()
	paths := []string{f.config, f.manifest, filepath.Join(f.root, ".homonto", "workflow-root"), filepath.Join(f.records, ".git", "index")}
	if err := filepath.WalkDir(filepath.Join(f.records, "changes"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Type().IsRegular() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, repo := range f.repos {
		paths = append(paths, filepath.Join(repo, ".git", "index"))
	}
	sort.Strings(paths)
	snapshot := make(map[string]string, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		snapshot[path] = hex.EncodeToString(sum[:])
	}
	return snapshot
}
