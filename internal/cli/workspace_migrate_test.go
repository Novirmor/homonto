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

	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/workflowstatus"
	"github.com/noviopenworks/homonto/internal/workspace"
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
	root       string
	config     string
	records    string
	manifest   string
	repos      map[string]string
	bases      map[string]string
	executions map[string]string
	input      workspacemigration.Manifest
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

func TestWorkspaceMigrateFourteenLegacyCheckoutsPreservation(t *testing.T) {
	f := newMigrationCLIFixture(t)
	beforeSources := migrationCLISourceFingerprints(t, f)
	beforeExecutions := migrationCLIExecutionFingerprints(t, f)
	beforeGitMetadata := migrationCLIGitMetadata(t, f)
	retiredPath := filepath.Join(f.records, "changes", "standardize-ci-ecr-pipelines-abandoned-006ff3b4", "onto-state.yaml")
	retiredBefore, err := os.ReadFile(retiredPath)
	if err != nil {
		t.Fatal(err)
	}
	output, err := runMigrationCLI(f, "workspace", "migrate", "plan", "--manifest", f.manifest, "--json")
	if err != nil {
		t.Fatalf("workspace migrate plan: %v\n%s", err, output)
	}
	var plan workspacemigration.Plan
	if err := json.Unmarshal([]byte(output), &plan); err != nil {
		t.Fatalf("plan JSON: %v\n%s", err, output)
	}

	output, err = runMigrationCLI(f, "workspace", "migrate", "apply", "--manifest", f.manifest, "--plan-hash", plan.PlanHash)
	if err == nil || !strings.Contains(err.Error(), "requires --yes") || output != "" {
		t.Fatalf("apply without --yes = output %q, error %v", output, err)
	}
	if _, err := os.Lstat(filepath.Join(f.root, ".homonto", "workflow-layout.json")); !os.IsNotExist(err) {
		t.Fatalf("unconfirmed apply created layout marker: %v", err)
	}

	output, err = runMigrationCLI(f, "workspace", "migrate", "apply", "--manifest", f.manifest, "--plan-hash", plan.PlanHash, "--yes")
	if err != nil {
		t.Fatalf("workspace migrate apply: %v\n%s", err, output)
	}
	parts := strings.Fields(output)
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "migration-") || parts[1] != "complete" {
		t.Fatalf("apply output = %q", output)
	}
	runID := parts[0]
	if got := migrationCLIGitText(t, f.records, "rev-list", "--count", "HEAD"); got != "3" {
		t.Fatalf("records history has %s commits, want legacy plus migration and proof", got)
	}

	output, err = runMigrationCLI(f, "workspace", "migrate", "verify", "--run-id", runID)
	if err == nil || !strings.Contains(err.Error(), "requires --json") || output != "" {
		t.Fatalf("verify without --json = output %q, error %v", output, err)
	}
	output, err = runMigrationCLI(f, "workspace", "migrate", "verify", "--run-id", runID, "--json")
	if err != nil {
		t.Fatalf("workspace migrate verify: %v\n%s", err, output)
	}
	var verification workspacemigration.Verification
	if err := json.Unmarshal([]byte(output), &verification); err != nil {
		t.Fatalf("verification JSON: %v\n%s", err, output)
	}
	if verification.RunID != runID || verification.PlanHash != plan.PlanHash || verification.Status != "complete" || verification.RecordWrites != 3 || verification.RetiredRecords != 1 || verification.Bindings != len(migrationAliases) {
		t.Fatalf("verification = %+v", verification)
	}
	receipt, err := migrationrecord.LoadReceipt(f.records, runID)
	if err != nil {
		t.Fatalf("load migration receipt: %v", err)
	}
	ciStatePath := filepath.Join(f.records, "changes", "standardize-ci-ecr-pipelines", "onto-state.yaml")
	legacyConfigFound := false
	for _, write := range receipt.RecordWrites {
		if write.Path != ciStatePath {
			continue
		}
		wantBaseRef := f.bases[migrationAliases[0]]
		if write.Action != workspacemigration.RecordWriteTransformActive || write.LegacyConfig == nil || write.LegacyConfig.BaseRef != wantBaseRef || write.LegacyConfig.BaseBranch != "main" || write.LegacyConfig.Provenance != migrationrecord.LegacyConfigProvenance {
			t.Fatalf("CI legacy config receipt = %+v", write)
		}
		legacyConfigFound = true
		break
	}
	if !legacyConfigFound {
		t.Fatalf("receipt omitted CI record write %s", ciStatePath)
	}
	retiredAfter, err := os.ReadFile(retiredPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(retiredAfter, retiredBefore) {
		t.Fatalf("retired state bytes changed\nwant: %q\n got: %q", retiredBefore, retiredAfter)
	}
	retiredState, err := ontostate.InspectRaw(retiredAfter, retiredPath)
	if err != nil || retiredState.ID != "006ff3b4" || retiredState.Phase != "build" || !retiredState.Abandoned {
		t.Fatalf("retired state identity = %+v, %v", retiredState, err)
	}
	if afterSources := migrationCLISourceFingerprints(t, f); !reflect.DeepEqual(afterSources, beforeSources) {
		t.Fatalf("migration changed source head/index/ref/file fingerprints\nbefore: %#v\nafter: %#v", beforeSources, afterSources)
	}
	if afterExecutions := migrationCLIExecutionFingerprints(t, f); !reflect.DeepEqual(afterExecutions, beforeExecutions) {
		t.Fatalf("migration changed execution head/index/ref/file fingerprints\nbefore: %#v\nafter: %#v", beforeExecutions, afterExecutions)
	}
	assertMigrationCLITokensOnly(t, f, beforeGitMetadata, migrationCLIGitMetadata(t, f))

	output, err = runMigrationCLI(f, "worktree", "list", "--json")
	if err != nil {
		t.Fatalf("worktree list: %v\n%s", err, output)
	}
	var entries []workspace.Worktree
	if err := json.Unmarshal([]byte(output), &entries); err != nil {
		t.Fatalf("worktree list JSON: %v\n%s", err, output)
	}
	if len(entries) != len(migrationAliases) {
		t.Fatalf("worktree list entries = %d, want %d: %+v", len(entries), len(migrationAliases), entries)
	}
	for _, entry := range entries {
		if entry.Origin != "legacy-migration" || entry.MigrationRunID != runID || entry.Workflow != "onto" || entry.Change != "standardize-ci-ecr-pipelines" || entry.StateID != "id:eab6ef3d" || entry.Path != f.executions[entry.Repo] {
			t.Fatalf("worktree list entry = %+v", entry)
		}
	}
	status := workflowstatus.ReadConfig(f.config)
	if len(status.Findings) != 0 || len(status.Changes) != 2 {
		t.Fatalf("normal status = %+v", status)
	}
	for _, change := range status.Changes {
		if change.Identity == "006ff3b4" || change.Name == "standardize-ci-ecr-pipelines-abandoned-006ff3b4" {
			t.Fatalf("retired record appeared in normal status: %+v", status)
		}
	}
	output, err = runMigrationCLI(f, "workspace", "migrate", "recover", "--run-id", runID, "--action", "resume", "--yes")
	if err == nil || !strings.Contains(err.Error(), "plan-hash") {
		t.Fatalf("recover without --plan-hash = output %q, error %v", output, err)
	}
	output, err = runMigrationCLI(f, "workspace", "migrate", "recover", "--run-id", runID, "--action", "resume", "--plan-hash", plan.PlanHash, "--yes")
	if err != nil || strings.TrimSpace(output) != runID+"\tcomplete" {
		t.Fatalf("completed recover = output %q, error %v", output, err)
	}

	output, err = runMigrationCLI(f, "workspace", "migrate", "apply", "--manifest", f.manifest, "--plan-hash", plan.PlanHash, "--yes")
	if err != nil || strings.TrimSpace(output) != runID+"\tcomplete" {
		t.Fatalf("repeated apply = output %q, error %v", output, err)
	}
	if afterSources := migrationCLISourceFingerprints(t, f); !reflect.DeepEqual(afterSources, beforeSources) {
		t.Fatalf("recover or repeated apply changed source head/index/ref/file fingerprints\nbefore: %#v\nafter: %#v", beforeSources, afterSources)
	}
	if afterExecutions := migrationCLIExecutionFingerprints(t, f); !reflect.DeepEqual(afterExecutions, beforeExecutions) {
		t.Fatalf("recover or repeated apply changed execution head/index/ref/file fingerprints\nbefore: %#v\nafter: %#v", beforeExecutions, afterExecutions)
	}
	assertMigrationCLITokensOnly(t, f, beforeGitMetadata, migrationCLIGitMetadata(t, f))
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
	executions := make(map[string]string, len(migrationAliases))
	for _, alias := range migrationAliases {
		execution := filepath.Join(root, ".legacy-worktrees", alias)
		migrationCLIGit(t, repos[alias], "worktree", "add", "-b", "migration-"+alias, execution, bases[alias])
		executions[alias] = execution
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
	ciState.WriteString("schema_version: 2\nchange: standardize-ci-ecr-pipelines\nid: eab6ef3d\nworkflow: full\nphase: build\n")
	fmt.Fprintf(&ciState, "base_ref: %s\nbase_branch: main\n", bases[migrationAliases[0]])
	ciState.WriteString("repos:\n")
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
				Sources: migrationCLISources(repos, bases, executions),
			},
			{
				Path:    "changes/standardize-ci-ecr-pipelines-abandoned-006ff3b4",
				ID:      "006ff3b4",
				Sources: []workspacemigration.ManifestSource{},
			},
		},
	}
	fixture := &migrationCLIFixture{
		root:       root,
		config:     config,
		records:    records,
		manifest:   filepath.Join(base, "migration-manifest.json"),
		repos:      repos,
		bases:      bases,
		executions: executions,
		input:      input,
	}
	writeMigrationCLIManifest(t, fixture)
	return fixture
}

func migrationCLISources(repos, bases, executions map[string]string) []workspacemigration.ManifestSource {
	sources := make([]workspacemigration.ManifestSource, 0, len(migrationAliases))
	for _, alias := range migrationAliases {
		sources = append(sources, workspacemigration.ManifestSource{
			Alias:         alias,
			BaseRef:       bases[alias],
			BaseBranch:    "main",
			GitCommonDir:  filepath.Join(repos[alias], ".git"),
			ExecutionPath: executions[alias],
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

type migrationCLISourceFingerprint struct {
	Head  string
	Index string
	Refs  map[string]string
	Files map[string]string
}

func migrationCLISourceFingerprints(t *testing.T, f *migrationCLIFixture) map[string]migrationCLISourceFingerprint {
	t.Helper()
	return migrationCLICheckoutFingerprints(t, f.repos)
}

func migrationCLIExecutionFingerprints(t *testing.T, f *migrationCLIFixture) map[string]migrationCLISourceFingerprint {
	t.Helper()
	return migrationCLICheckoutFingerprints(t, f.executions)
}

func migrationCLICheckoutFingerprints(t *testing.T, fRoots map[string]string) map[string]migrationCLISourceFingerprint {
	t.Helper()
	result := make(map[string]migrationCLISourceFingerprint, len(fRoots))
	for alias, root := range fRoots {
		indexPath := migrationCLIGitText(t, root, "rev-parse", "--path-format=absolute", "--git-path", "index")
		index, err := os.ReadFile(indexPath)
		if err != nil {
			t.Fatal(err)
		}
		refs := map[string]string{}
		for _, line := range strings.Split(migrationCLIGitText(t, root, "for-each-ref", "--format=%(refname) %(objectname)", "refs"), "\n") {
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 2 {
				t.Fatalf("source refs for %s = %q", alias, line)
			}
			refs[fields[0]] = fields[1]
		}
		result[alias] = migrationCLISourceFingerprint{
			Head:  migrationCLIGitText(t, root, "rev-parse", "HEAD"),
			Index: migrationCLIHash(index),
			Refs:  refs,
			Files: migrationCLICheckoutFiles(t, root),
		}
	}
	return result
}

func migrationCLICheckoutFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[rel] = fmt.Sprintf("regular:%04o:%s", info.Mode().Perm(), migrationCLIHash(data))
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			files[rel] = fmt.Sprintf("symlink:%04o:%s", info.Mode().Perm(), migrationCLIHash([]byte(target)))
		case info.IsDir():
			return nil
		default:
			return fmt.Errorf("unsupported checkout file %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func migrationCLIGitMetadata(t *testing.T, f *migrationCLIFixture) map[string]map[string]string {
	t.Helper()
	result := make(map[string]map[string]string, len(f.executions))
	for alias, execution := range f.executions {
		gitDir := migrationCLIGitText(t, execution, "rev-parse", "--absolute-git-dir")
		metadata := map[string]string{}
		err := filepath.WalkDir(gitDir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(gitDir, path)
			if err != nil {
				return err
			}
			if rel == "." || entry.IsDir() {
				return nil
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				metadata[filepath.ToSlash(rel)] = fmt.Sprintf("regular:%04o:%s", info.Mode().Perm(), migrationCLIHash(data))
				return nil
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				metadata[filepath.ToSlash(rel)] = fmt.Sprintf("symlink:%04o:%s", info.Mode().Perm(), migrationCLIHash([]byte(target)))
				return nil
			}
			return fmt.Errorf("unsupported Git metadata file %s", rel)
		})
		if err != nil {
			t.Fatal(err)
		}
		result[alias] = metadata
	}
	return result
}

func assertMigrationCLITokensOnly(t *testing.T, f *migrationCLIFixture, before, after map[string]map[string]string) {
	t.Helper()
	for alias, execution := range f.executions {
		beforeMetadata, ok := before[alias]
		if !ok {
			t.Fatalf("missing pre-migration Git metadata for %s", alias)
		}
		afterMetadata, ok := after[alias]
		if !ok {
			t.Fatalf("missing post-migration Git metadata for %s", alias)
		}
		for path, fingerprint := range beforeMetadata {
			if afterMetadata[path] != fingerprint {
				t.Fatalf("Git metadata changed for %s at %s", alias, path)
			}
		}
		for path := range afterMetadata {
			if _, existed := beforeMetadata[path]; !existed && path != "homonto-owner" {
				t.Fatalf("unexpected Git metadata created for %s at %s", alias, path)
			}
		}
		gitDir := migrationCLIGitText(t, execution, "rev-parse", "--absolute-git-dir")
		owner, err := os.Lstat(filepath.Join(gitDir, "homonto-owner"))
		if err != nil || !owner.Mode().IsRegular() || owner.Mode().Perm() != 0o600 {
			t.Fatalf("migration owner token for %s = %v, %v", alias, owner, err)
		}
	}
}

func migrationCLIHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
