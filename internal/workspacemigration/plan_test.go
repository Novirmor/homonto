package workspacemigration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/workspace"
)

type migrationFixture struct {
	root     string
	config   string
	records  string
	manifest string
	repos    map[string]string
	bases    map[string]string
	input    Manifest
}

func TestBuildInventoriesRetiredRecordWithoutReactivatingIt(t *testing.T) {
	f := newMigrationFixture(t, true)
	before := migrationSnapshot(t, f)

	// Ordinary loading remains fail-closed. Only the dedicated migration read
	// path can inspect this legacy ownership transition.
	if _, err := workspace.Load(f.config); err == nil {
		t.Fatal("ordinary workspace.Load accepted legacy records")
	}
	plan, err := Build(f.config, f.manifest)
	if err != nil {
		t.Fatalf("Build: %v\nblockers: %+v", err, plan.Blockers)
	}
	if !plan.ReadOnly || plan.Status != "ready" || plan.PlanHash == "" {
		t.Fatalf("unexpected plan summary: %+v", plan)
	}
	if got := plan.ManifestRequirements; got.Version != 1 || got.ControlOnlyAttestation != ControlOnlyAttestation || !reflect.DeepEqual(got.RecordFields, []string{"path", "id", "sources"}) {
		t.Fatalf("manifest requirements = %+v", got)
	}
	if len(plan.Records) != 2 {
		t.Fatalf("records = %+v", plan.Records)
	}
	active, retired := findRecord(t, plan, "active-id"), findRecord(t, plan, "retired-id")
	if active.Lifecycle != "active" || retired.Lifecycle != "retired" {
		t.Fatalf("record lifecycles = active:%q retired:%q", active.Lifecycle, retired.Lifecycle)
	}
	if active.Name != retired.Name || retired.RelativePath != "changes/active-retired" || filepath.Base(retired.Path) != "active-retired" {
		t.Fatalf("reused name was not preserved by path plus ID: active=%+v retired=%+v", active, retired)
	}
	if len(active.Sources) != 1 || active.Sources[0].Alias != "app" || active.Sources[0].BaseRef != f.bases["app"] {
		t.Fatalf("active sources = %+v", active.Sources)
	}
	if !hasFingerprint(plan.Files, filepath.Join(f.records, "changes", "active", ".onto", "handoff.md"), "onto_evidence") {
		t.Fatal("historical .onto handoff was not fingerprinted")
	}
	if after := migrationSnapshot(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("read-only plan changed fixture\nbefore: %#v\nafter:  %#v", before, after)
	}
	again, err := Build(f.config, f.manifest)
	if err != nil || !reflect.DeepEqual(plan, again) {
		t.Fatalf("plan is not deterministic\nfirst:  %+v\nsecond: %+v\nerror: %v", plan, again, err)
	}
	if _, err := os.Lstat(filepath.Join(f.root, ".homonto", "workflow-layout.json")); !os.IsNotExist(err) {
		t.Fatalf("plan created layout marker: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.root, ".homonto", "worktrees.json")); !os.IsNotExist(err) {
		t.Fatalf("plan created registry: %v", err)
	}
}

func TestMigrationRejectsHiddenSensitiveContentDrift(t *testing.T) {
	fixture := newMigrationFixture(t, false)
	secret := filepath.Join(fixture.repos["app"], ".env")
	writeFile(t, secret, "migration-secret-before\n")
	runGit(t, fixture.repos["app"], "add", "-f", ".env")
	runGit(t, fixture.repos["app"], "commit", "-m", "add tracked environment")
	runGit(t, fixture.repos["app"], "update-index", "--assume-unchanged", ".env")
	writeFile(t, secret, "migration-secret-after\n")

	plan, err := Build(fixture.config, fixture.manifest)
	if !errors.Is(err, ErrBlocked) || !hasBlocker(plan, "source_index_flags_unsupported") {
		t.Fatalf("Build with hidden sensitive drift = %v, blockers=%+v", err, plan.Blockers)
	}
	encoded, marshalErr := json.Marshal(plan)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, value := range []string{"migration-secret-before", "migration-secret-after"} {
		if strings.Contains(string(encoded), value) {
			t.Fatalf("plan exposed sensitive source bytes %q", value)
		}
	}
}

func TestMigrationPlanEnumeratesRecoveryMaterial(t *testing.T) {
	fixture := newMigrationFixture(t, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, want := range []struct {
		kind, path, dataClass, mutation, dynamic string
		mode                                     uint32
	}{
		{journalKindRecoveryRunDirectory, plannedRunPath(fixture.records), "private_recovery_directory", "create_or_validate", "", 0o700},
		{journalKindRecoveryPrivateDirectory, plannedRunPath(fixture.records, "private"), "private_recovery_directory", "create_or_validate", "", 0o700},
		{journalKindRecoveryJournal, plannedRunPath(fixture.records, "private", "journal.json"), "private_recovery_journal", "create_or_update", dynamicPrivateJournalRule, 0o600},
		{journalKindRecoveryIntent, plannedRunPath(fixture.records, "private", "intent.json"), "private_preparation_intent", "create_then_remove", dynamicPreparationIntentRule, 0o600},
		{journalKindRecoveryBundle, plannedRunPath(fixture.records, "private", "records.git.bundle"), "private_records_git_bundle", "create_or_validate", dynamicRecordsBundleRule, 0o600},
	} {
		operation, found := prospectiveOperation(plan, want.kind)
		if !found || operation.Scope != journalScopeRecovery || operation.Path != want.path || !operation.PostExists || operation.PostMode != want.mode || operation.DataClass != want.dataClass || operation.Mutation != want.mutation || operation.DynamicRule != want.dynamic {
			t.Fatalf("recovery operation %q = %+v, found=%t", want.kind, operation, found)
		}
	}
	completion, found := prospectiveOperation(plan, journalKindCompletion)
	if !found || completion.DataClass != "private_completion_witness" || completion.Mutation != "create_or_update" || completion.PostMode != 0o600 {
		t.Fatalf("completion witness operation = %+v, found=%t", completion, found)
	}
}

func TestBuildPreparesExplicitTransformsAndPreservesRetiredBytes(t *testing.T) {
	f := newMigrationFixture(t, true, false)
	aliases := []string{"app"}
	for i := 1; i < 15; i++ {
		alias := fmt.Sprintf("source-%02d", i)
		repo := newGitRepo(t, filepath.Join(filepath.Dir(f.root), "sources", alias), "main")
		f.repos[alias] = repo
		f.bases[alias] = gitTextTest(t, repo, "rev-parse", "HEAD")
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	var config strings.Builder
	config.WriteString("schema_version = 2\n[workflow]\nroot = \".homonto-local\"\ngit = \"existing\"\n[worktrees]\ndir = \"../execution\"\n[repos]\n")
	for _, alias := range aliases {
		fmt.Fprintf(&config, "%q = %q\n", alias, f.repos[alias])
	}
	writeFile(t, f.config, config.String())

	var state strings.Builder
	state.WriteString("schema_version: 2\nchange: active\nid: active-id\nworkflow: full\nphase: build\n")
	fmt.Fprintf(&state, "base_ref: %s\nbase_branch: main\n", f.bases["app"])
	state.WriteString("directive: operator-only-directive\ndeviates_from: [decision-17]\nrepos:\n")
	for _, alias := range aliases {
		fmt.Fprintf(&state, "  - %q\n", alias)
	}
	activePath := filepath.Join(f.records, "changes", "active", "onto-state.yaml")
	writeFile(t, activePath, state.String())
	f.input.Records[0].Sources = f.input.Records[0].Sources[:0]
	for _, alias := range aliases {
		f.input.Records[0].Sources = append(f.input.Records[0].Sources, ManifestSource{
			Alias:        alias,
			BaseRef:      f.bases[alias],
			BaseBranch:   "main",
			GitCommonDir: filepath.Join(f.repos[alias], ".git"),
		})
	}
	writeManifest(t, f)

	taskPath := filepath.Join(f.records, "changes", "active", "tasks.md")
	evidencePath := filepath.Join(f.records, "changes", "active", ".onto", "handoff.md")
	writeFile(t, taskPath, "# unchanged task markdown\n")
	writeFile(t, evidencePath, "private handoff evidence\n")
	runGit(t, f.records, "add", "changes/active/onto-state.yaml", "changes/active/tasks.md", "changes/active/.onto/handoff.md")
	runGit(t, f.records, "commit", "-m", "prepare records inventory")
	retiredPath := filepath.Join(f.records, "changes", "active-retired", "onto-state.yaml")
	retiredBefore := []byte(readFile(t, retiredPath))
	before := migrationSnapshot(t, f)

	plan, err := Build(f.config, f.manifest)
	if err != nil {
		t.Fatalf("Build: %v\nblockers: %+v", err, plan.Blockers)
	}
	if plan.Status != "ready" || len(plan.RecordWrites) != 2 {
		t.Fatalf("plan writes = %+v", plan.RecordWrites)
	}
	activeWrite := findRecordWrite(t, plan, activePath)
	if activeWrite.Action != RecordWriteTransformActive || activeWrite.PreSHA256 == activeWrite.PostSHA256 || !reflect.DeepEqual(activeWrite.ChangedFields, []string{"base_branch", "base_ref", "repo_bases", "repo_mode", "schema_version"}) {
		t.Fatalf("active record write = %+v", activeWrite)
	}
	if !reflect.DeepEqual(activeWrite.LegacyConfig, &ontostate.LegacyConfig{
		BaseRef: f.bases["app"], BaseBranch: "main", Provenance: ontostate.LegacyConfigProvenance,
	}) {
		t.Fatalf("active legacy config receipt = %+v", activeWrite.LegacyConfig)
	}
	retiredWrite := findRecordWrite(t, plan, retiredPath)
	if retiredWrite.Action != RecordWritePreserveRetired || retiredWrite.PreSHA256 != retiredWrite.PostSHA256 || len(retiredWrite.ChangedFields) != 0 || retiredWrite.LegacyConfig != nil {
		t.Fatalf("retired record write = %+v", retiredWrite)
	}

	activePrepared := findPreparedRecordWrite(t, plan, activePath)
	postPath := filepath.Join(t.TempDir(), "onto-state.yaml")
	if err := os.WriteFile(postPath, activePrepared.postimage, 0o644); err != nil {
		t.Fatal(err)
	}
	post, err := ontostate.Load(postPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := post.Validate(); err != nil {
		t.Fatal(err)
	}
	if post.SchemaVersion != ontostate.CurrentSchemaVersion || post.ID != "active-id" || post.Phase != "build" || post.RepoMode != "explicit" || post.BaseRef != "" || post.BaseBranch != "" || post.Directive != "operator-only-directive" || !reflect.DeepEqual(post.DeviatesFrom, []string{"decision-17"}) {
		t.Fatalf("postimage state = %+v", post)
	}
	record := findRecord(t, plan, "active-id")
	if record.ScalarBaseRef != f.bases["app"] || record.ScalarBaseBranch != "main" {
		t.Fatalf("preimage scalar inventory = %+v", record)
	}
	if len(post.RepoBases) != len(aliases) {
		t.Fatalf("postimage anchors = %+v", post.RepoBases)
	}
	for _, alias := range aliases {
		if got := post.RepoBases[alias]; got.BaseRef != f.bases[alias] || got.BaseBranch != "main" || got.GitCommonDir != filepath.Join(f.repos[alias], ".git") {
			t.Fatalf("postimage anchor %q = %+v", alias, got)
		}
	}
	retiredPrepared := findPreparedRecordWrite(t, plan, retiredPath)
	if !reflect.DeepEqual(retiredPrepared.preimage, retiredBefore) || !reflect.DeepEqual(retiredPrepared.postimage, retiredBefore) || readFile(t, retiredPath) != string(retiredBefore) {
		t.Fatal("retired state was not held byte-identical")
	}

	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"operator-only-directive", "unchanged task markdown", "private handoff evidence"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("plan exposed raw record content %q: %s", private, encoded)
		}
	}
	if after := migrationSnapshot(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("planning changed fixture\nbefore: %#v\nafter:  %#v", before, after)
	}
	again, err := Build(f.config, f.manifest)
	if err != nil || !reflect.DeepEqual(plan, again) {
		t.Fatalf("prepared plan is not deterministic\nfirst: %+v\nsecond: %+v\nerror: %v", plan, again, err)
	}
}

func TestBuildRefusesCoResidentActiveStateTransformation(t *testing.T) {
	f := newMigrationFixture(t, false, false)
	ontoPath := filepath.Join(f.records, "changes", "active", "onto-state.yaml")
	writeFile(t, filepath.Join(f.records, "changes", "active", "state.yaml"), readFile(t, ontoPath))
	before := migrationSnapshot(t, f)

	plan, err := Build(f.config, f.manifest)
	if !errors.Is(err, ErrBlocked) || !hasBlocker(plan, "co_resident_state_transform_unsupported") {
		t.Fatalf("Build = %v, blockers=%+v", err, plan.Blockers)
	}
	if len(plan.RecordWrites) != 0 {
		t.Fatalf("co-resident transform prepared writes: %+v", plan.RecordWrites)
	}
	if after := migrationSnapshot(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("co-resident refusal changed fixture\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestBuildRefusesSchemaOneIntegrationRequirementTransformation(t *testing.T) {
	f := newMigrationFixture(t, false)
	activePath := filepath.Join(f.records, "changes", "active", "onto-state.yaml")
	writeFile(t, activePath, fmt.Sprintf("schema_version: 1\nchange: active\nid: active-id\nphase: build\nbase_ref: %s\nintegration_required: true\nrepos: [app]\n", f.bases["app"]))
	before := migrationSnapshot(t, f)

	plan, err := Build(f.config, f.manifest)
	if !errors.Is(err, ErrBlocked) || !hasBlocker(plan, "state_transform_unsupported") {
		t.Fatalf("Build = %v, blockers=%+v", err, plan.Blockers)
	}
	if len(plan.RecordWrites) != 0 || len(plan.preparedRecordWrites) != 0 {
		t.Fatalf("schema-1 integration requirement prepared writes: summaries=%+v prepared=%+v", plan.RecordWrites, plan.preparedRecordWrites)
	}
	if after := migrationSnapshot(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("schema-1 integration requirement refusal changed fixture\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestLoadMigrationRefusesSymlinkConfigBeforeParsing(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target.toml")
	writeFile(t, target, "not a migration config\n")
	link := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := workspace.LoadMigration(link); err == nil || !strings.Contains(err.Error(), "real regular config file") {
		t.Fatalf("LoadMigration symlink error = %v", err)
	}
}

func TestBuildAcceptsInstalledProjectionAndNormalChangeDocuments(t *testing.T) {
	f := newMigrationFixture(t, true)
	control := filepath.Join(f.root, ".homonto")
	writeFile(t, filepath.Join(control, "state.json"), `{"note":"projection-state-must-not-appear"}`)
	writeFile(t, filepath.Join(control, "state.vend-portal-ui-old.json"), `{"managed":{"legacy":{}}}`)
	writeFile(t, filepath.Join(control, "remote.lock.json"), `{"remotes":[]}`)
	catalog := filepath.Join(control, "catalog")
	writeFile(t, filepath.Join(catalog, ".env"), "must-not-be-read\n")
	if err := os.Chmod(catalog, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(catalog, 0o755) })
	catalogBefore, err := os.Lstat(catalog)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.records, "changes", "README.md"), "# Changes\n")
	writeFile(t, filepath.Join(f.records, "changes", "templates", "README.md"), "# Template\n")
	runGit(t, f.records, "add", "changes/README.md", "changes/templates/README.md")
	runGit(t, f.records, "commit", "-m", "add record documents")
	before := migrationSnapshot(t, f)

	if _, err := workspace.Load(f.config); err == nil {
		t.Fatal("ordinary workspace.Load accepted the legacy transition")
	}
	plan, err := Build(f.config, f.manifest)
	if err != nil {
		t.Fatalf("Build: %v\nblockers: %+v", err, plan.Blockers)
	}
	if !hasFingerprint(plan.ControlFiles, filepath.Join(control, "state.json"), "projection_state") ||
		!hasFingerprint(plan.ControlFiles, filepath.Join(control, "state.vend-portal-ui-old.json"), "projection_state") ||
		!hasFingerprint(plan.ControlFiles, filepath.Join(control, "remote.lock.json"), "projection_remote_lock") {
		t.Fatalf("projection control fingerprints = %+v", plan.ControlFiles)
	}
	for _, file := range plan.ControlFiles {
		if file.Path == catalog {
			t.Fatalf("catalog directory was read or fingerprinted: %+v", file)
		}
	}
	if !hasFingerprint(plan.Files, filepath.Join(f.records, "changes", "README.md"), "workflow_record") ||
		!hasFingerprint(plan.Files, filepath.Join(f.records, "changes", "templates", "README.md"), "workflow_record") {
		t.Fatalf("change documentation fingerprints = %+v", plan.Files)
	}
	if len(plan.Layout.Repos) != 1 || plan.Layout.Repos[0].Alias != "app" {
		t.Fatalf("projection state was adopted as a source: %+v", plan.Layout.Repos)
	}
	for _, record := range plan.Records {
		if record.RelativePath == "changes/templates" {
			t.Fatalf("non-change directory was invented as a record: %+v", record)
		}
		for _, source := range record.Sources {
			if source.Alias == "vend-portal-ui-old" {
				t.Fatalf("deleted projection partition was adopted as a source: %+v", record)
			}
		}
	}
	encoded, marshalErr := json.Marshal(plan)
	if marshalErr != nil || strings.Contains(string(encoded), "must-not-be-read") || strings.Contains(string(encoded), "projection-state-must-not-appear") {
		t.Fatalf("plan exposed catalog content: %v %s", marshalErr, encoded)
	}
	if after := migrationSnapshot(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("projection/doc inventory changed fixture\nbefore: %#v\nafter: %#v", before, after)
	}
	catalogAfter, err := os.Lstat(catalog)
	if err != nil || catalogAfter.Mode() != catalogBefore.Mode() {
		t.Fatalf("catalog changed during planning: before=%v after=%v error=%v", catalogBefore.Mode(), catalogAfter.Mode(), err)
	}
}

func TestBuildDoesNotSkipStateBearingChangeDirectories(t *testing.T) {
	f := newMigrationFixture(t, false)
	writeFile(t, filepath.Join(f.records, "changes", "README.md"), "# Changes\n")
	writeFile(t, filepath.Join(f.records, "changes", "templates", "README.md"), "# Template\n")
	bad := filepath.Join(f.records, "changes", "broken", "onto-state.yaml")
	writeFile(t, bad, "schema_version: [\n")
	before := migrationSnapshot(t, f)

	plan, err := Build(f.config, f.manifest)
	if !errors.Is(err, ErrBlocked) || !hasBlocker(plan, "state_malformed") {
		t.Fatalf("Build = %v, blockers=%+v", err, plan.Blockers)
	}
	var broken *Record
	for i := range plan.Records {
		if plan.Records[i].RelativePath == "changes/broken" {
			broken = &plan.Records[i]
		}
		if plan.Records[i].RelativePath == "changes/templates" {
			t.Fatalf("non-change directory was invented as a record: %+v", plan.Records[i])
		}
	}
	if broken == nil || !reflect.DeepEqual(broken.StateFiles, []string{bad}) {
		t.Fatalf("malformed state-bearing directory was skipped: %+v", plan.Records)
	}
	if after := migrationSnapshot(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("state-bearing discovery changed fixture\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestBuildReportsSafeLegacyLayoutDiagnostic(t *testing.T) {
	f := newMigrationFixture(t, false)
	writeFile(t, filepath.Join(f.root, ".homonto", "workflow-root"), "private-unreported-root\n")
	plan, err := Build(f.config, f.manifest)
	if !errors.Is(err, ErrBlocked) || !hasBlocker(plan, "legacy_layout_legacy_marker_mismatch") || hasBlocker(plan, "legacy_layout_invalid") {
		t.Fatalf("Build = %v, blockers=%+v", err, plan.Blockers)
	}
	encoded, marshalErr := json.Marshal(plan)
	if marshalErr != nil || strings.Contains(string(encoded), "private-unreported-root") {
		t.Fatalf("layout diagnostic leaked raw marker data: %v %s", marshalErr, encoded)
	}
}

func TestBuildRejectsInvalidInputsWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(t *testing.T, f *migrationFixture)
		blocker string
	}{
		{
			name: "duplicate state ID",
			mutate: func(t *testing.T, f *migrationFixture) {
				replaceFile(t, filepath.Join(f.records, "changes", "active-retired", "onto-state.yaml"), "retired-id", "active-id")
			},
			blocker: "duplicate_state_id",
		},
		{
			name: "duplicate manifest ID",
			mutate: func(t *testing.T, f *migrationFixture) {
				f.input.Records[1].ID = "active-id"
				writeManifest(t, f)
			},
			blocker: "manifest_record_id_duplicate",
		},
		{
			name: "missing base",
			mutate: func(t *testing.T, f *migrationFixture) {
				f.input.Records[0].Sources[0].BaseRef = ""
				writeManifest(t, f)
			},
			blocker: "manifest_source_anchor_missing",
		},
		{
			name: "incorrect base",
			mutate: func(t *testing.T, f *migrationFixture) {
				f.input.Records[0].Sources[0].BaseRef = strings.Repeat("0", 40)
				writeManifest(t, f)
			},
			blocker: "source_base_missing",
		},
		{
			name: "invalid branch identity",
			mutate: func(t *testing.T, f *migrationFixture) {
				f.input.Records[0].Sources[0].BaseBranch = "missing-branch"
				writeManifest(t, f)
			},
			blocker: "source_branch_missing",
		},
		{
			name: "symlinked marker",
			mutate: func(t *testing.T, f *migrationFixture) {
				marker := filepath.Join(f.root, ".homonto", "workflow-root")
				outside := filepath.Join(t.TempDir(), "marker")
				writeFile(t, outside, ".homonto-local\n")
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, marker); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			},
			blocker: "legacy_layout_legacy_marker_invalid",
		},
		{
			name: "existing layout owner",
			mutate: func(t *testing.T, f *migrationFixture) {
				marker := fmt.Sprintf(`{"schema_version":2,"config_path":%q,"workflow_root":%q,"git_mode":"existing"}`, f.config, f.records)
				writeFile(t, filepath.Join(f.root, ".homonto", "workflow-layout.json"), marker)
			},
			blocker: "legacy_layout_layout_marker_present",
		},
		{
			name: "unknown manifest field",
			mutate: func(t *testing.T, f *migrationFixture) {
				writeFile(t, f.manifest, `{"version":1,"control_only_attestation":"legacy-implicit-config-source-contained-control-work-only","records":[],"unknown":true}`)
			},
			blocker: "manifest_invalid",
		},
		{
			name: "wrong control-only attestation",
			mutate: func(t *testing.T, f *migrationFixture) {
				f.input.ControlOnlyAttestation = "unreviewed"
				writeManifest(t, f)
			},
			blocker: "manifest_invalid",
		},
		{
			name: "manifest inside records",
			mutate: func(t *testing.T, f *migrationFixture) {
				f.manifest = filepath.Join(f.records, "migration-manifest.json")
				writeManifest(t, f)
			},
			blocker: "manifest_path_unsafe",
		},
		{
			name: "Git control root",
			mutate: func(t *testing.T, f *migrationFixture) {
				runGit(t, f.root, "init")
			},
			blocker: "legacy_layout_config_root_is_git",
		},
		{
			name: "legacy docs state",
			mutate: func(t *testing.T, f *migrationFixture) {
				if err := os.MkdirAll(filepath.Join(f.root, "docs", "changes"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			blocker: "legacy_layout_legacy_alternative_state_present",
		},
		{
			name: "legacy docs promotion state",
			mutate: func(t *testing.T, f *migrationFixture) {
				if err := os.MkdirAll(filepath.Join(f.root, "docs", ".to-promote"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			blocker: "legacy_layout_legacy_alternative_state_present",
		},
		{
			name: "records remote",
			mutate: func(t *testing.T, f *migrationFixture) {
				runGit(t, f.records, "remote", "add", "origin", f.repos["app"])
			},
			blocker: "records_remote_present",
		},
		{
			name: "other control owner",
			mutate: func(t *testing.T, f *migrationFixture) {
				writeFile(t, filepath.Join(f.root, ".homonto", "other-owner.json"), "{}\n")
			},
			blocker: "other_control_owner_present",
		},
		{
			name: "symlinked projection state",
			mutate: func(t *testing.T, f *migrationFixture) {
				target := filepath.Join(t.TempDir(), "state.json")
				writeFile(t, target, `{}`)
				if err := os.Symlink(target, filepath.Join(f.root, ".homonto", "state.json")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			},
			blocker: "control_file_unsafe",
		},
		{
			name: "workflow overlaps source",
			mutate: func(t *testing.T, f *migrationFixture) {
				writeFile(t, f.config, fmt.Sprintf("schema_version = 2\n[workflow]\nroot = %q\ngit = \"existing\"\n[repos]\napp = %q\n", f.repos["app"], f.repos["app"]))
			},
			blocker: "legacy_layout_config_validation_failed",
		},
		{
			name: "worktrees prefix overlaps source",
			mutate: func(t *testing.T, f *migrationFixture) {
				writeFile(t, f.config, fmt.Sprintf("schema_version = 2\n[workflow]\nroot = \".homonto-local\"\ngit = \"existing\"\n[worktrees]\ndir = %q\n[repos]\napp = %q\n", filepath.Join(f.repos["app"], "execution"), f.repos["app"]))
			},
			blocker: "legacy_layout_config_validation_failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMigrationFixture(t, true)
			tc.mutate(t, f)
			before := migrationSnapshot(t, f)
			plan, err := Build(f.config, f.manifest)
			if !errors.Is(err, ErrBlocked) {
				t.Fatalf("Build error = %v, want ErrBlocked; plan=%+v", err, plan)
			}
			if !hasBlocker(plan, tc.blocker) {
				t.Fatalf("blockers = %+v, want %q", plan.Blockers, tc.blocker)
			}
			if plan.PlanHash == "" {
				t.Fatal("blocked plan did not retain its deterministic inventory hash")
			}
			again, againErr := Build(f.config, f.manifest)
			if !errors.Is(againErr, ErrBlocked) || again.PlanHash != plan.PlanHash {
				t.Fatalf("blocked plan is not deterministic: first=%q second=%q error=%v", plan.PlanHash, again.PlanHash, againErr)
			}
			if after := migrationSnapshot(t, f); !reflect.DeepEqual(before, after) {
				t.Fatalf("blocked plan changed fixture\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestBuildRetainsUnknownStateFieldMetadata(t *testing.T) {
	f := newMigrationFixture(t, false, false)
	state := filepath.Join(f.records, "changes", "active", "onto-state.yaml")
	writeFile(t, state, readFile(t, state)+"future_metadata:\n  retained: true\n")
	runGit(t, f.records, "add", "changes/active/onto-state.yaml")
	runGit(t, f.records, "commit", "-m", "add unknown state metadata")
	plan, err := Build(f.config, f.manifest)
	if err != nil {
		t.Fatalf("Build: %v\nblockers: %+v", err, plan.Blockers)
	}
	if got := findRecord(t, plan, "active-id").UnknownFields; !reflect.DeepEqual(got, []string{"future_metadata"}) {
		t.Fatalf("unknown state fields = %v", got)
	}
	if !findRecord(t, plan, "active-id").LegacyConfigSource {
		t.Fatal("implicit legacy config source was not retained in the inventory")
	}
}

func TestBuildRetainsLegacyScalarBasesWithoutTreatingThemAsSourceAnchors(t *testing.T) {
	f := newMigrationFixture(t, false, false)
	state := filepath.Join(f.records, "changes", "active", "onto-state.yaml")
	writeFile(t, state, readFile(t, state)+fmt.Sprintf("base_ref: %s\nbase_branch: main\n", f.bases["app"]))
	runGit(t, f.records, "add", "changes/active/onto-state.yaml")
	runGit(t, f.records, "commit", "-m", "add legacy scalar bases")

	plan, err := Build(f.config, f.manifest)
	if err != nil {
		t.Fatalf("Build: %v\nblockers: %+v", err, plan.Blockers)
	}
	record := findRecord(t, plan, "active-id")
	if record.ScalarBaseRef != f.bases["app"] || record.ScalarBaseBranch != "main" || !record.LegacyConfigSource {
		t.Fatalf("legacy scalar base metadata = %+v", record)
	}
	if len(record.Sources) != 1 || record.Sources[0].BaseRef != f.bases["app"] {
		t.Fatalf("manifest source anchor = %+v", record.Sources)
	}
}

func TestBuildRejectsCoResidentStateSourceScopeConflicts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		second func(t *testing.T, f *migrationFixture, first string) string
	}{
		{
			name: "selected repositories",
			second: func(t *testing.T, f *migrationFixture, first string) string {
				t.Helper()
				second := strings.Replace(first, "repos: [app]", "repos: [other]", 1)
				return strings.Replace(second, "\n  app:\n", "\n  other:\n", 1)
			},
		},
		{
			name: "per-repository base anchor",
			second: func(t *testing.T, f *migrationFixture, first string) string {
				t.Helper()
				return strings.Replace(first, "base_ref: "+f.bases["app"], "base_ref: "+strings.Repeat("0", 40), 1)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMigrationFixture(t, false)
			first := activeState(f.bases["app"], filepath.Join(f.repos["app"], ".git"), true)
			writeFile(t, filepath.Join(f.records, "changes", "active", "onto-state.yaml"), first)
			writeFile(t, filepath.Join(f.records, "changes", "active", "state.yaml"), tc.second(t, f, first))

			plan, err := Build(f.config, f.manifest)
			if !errors.Is(err, ErrBlocked) || !hasBlocker(plan, "state_identity_conflict") {
				t.Fatalf("Build = %v, blockers=%+v", err, plan.Blockers)
			}
		})
	}
}

func TestCoResidentStatesEqualComparesSourceScope(t *testing.T) {
	base := ontostate.RawInspection{
		ID:         "change-id",
		Change:     "change",
		Workflow:   "full",
		Phase:      "build",
		Repos:      []string{"app", "other"},
		RepoMode:   "explicit",
		BaseRef:    "scalar-base",
		BaseBranch: "main",
		RepoBases: map[string]ontostate.RepoBase{
			"app":   {BaseRef: "app-base", BaseBranch: "main", GitCommonDir: "/repos/app/.git"},
			"other": {BaseRef: "other-base", BaseBranch: "main", GitCommonDir: "/repos/other/.git"},
		},
	}
	reordered := base
	reordered.Repos = []string{"other", "app"}
	if !coResidentStatesEqual(base, reordered) {
		t.Fatal("repository order changed an otherwise equal source scope")
	}
	for _, tc := range []struct {
		name  string
		other ontostate.RawInspection
	}{
		{
			name: "repositories",
			other: func() ontostate.RawInspection {
				other := base
				other.Repos = []string{"app"}
				return other
			}(),
		},
		{
			name: "repository mode",
			other: func() ontostate.RawInspection {
				other := base
				other.RepoMode = "legacy"
				return other
			}(),
		},
		{
			name: "repository anchor",
			other: func() ontostate.RawInspection {
				other := base
				other.RepoBases = map[string]ontostate.RepoBase{
					"app":   {BaseRef: "different-base", BaseBranch: "main", GitCommonDir: "/repos/app/.git"},
					"other": {BaseRef: "other-base", BaseBranch: "main", GitCommonDir: "/repos/other/.git"},
				}
				return other
			}(),
		},
		{
			name: "scalar base ref",
			other: func() ontostate.RawInspection {
				other := base
				other.BaseRef = "different-scalar-base"
				return other
			}(),
		},
		{
			name: "scalar base branch",
			other: func() ontostate.RawInspection {
				other := base
				other.BaseBranch = "release"
				return other
			}(),
		},
		{
			name: "workflow",
			other: func() ontostate.RawInspection {
				other := base
				other.Workflow = "fix"
				return other
			}(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if coResidentStatesEqual(base, tc.other) {
				t.Fatal("different source scope was accepted")
			}
		})
	}
}

func TestBuildInventoriesOptionalExecutionDirtWithoutChangingItsIndex(t *testing.T) {
	f := newMigrationFixture(t, false)
	f.input.Records[0].Sources[0].ExecutionPath = f.repos["app"]
	writeManifest(t, f)
	writeFile(t, filepath.Join(f.repos["app"], "untracked"), "preserve this work\n")
	before := migrationSnapshot(t, f)
	plan, err := Build(f.config, f.manifest)
	if err != nil {
		t.Fatalf("Build: %v\nblockers: %+v", err, plan.Blockers)
	}
	execution := findRecord(t, plan, "active-id").Sources[0].Execution
	if execution == nil || execution.Path != f.repos["app"] || execution.Dirt.Untracked != 1 {
		t.Fatalf("execution inventory = %+v", execution)
	}
	if after := migrationSnapshot(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("execution inspection changed fixture\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestBuildBlocksDirtyRecordsAndRetainsInventory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, *migrationFixture)
		dirt   Dirt
	}{
		{
			name: "tracked",
			mutate: func(t *testing.T, f *migrationFixture) {
				t.Helper()
				replaceFile(t, filepath.Join(f.records, "changes", "active", "onto-state.yaml"), "phase: open", "phase: build")
			},
			dirt: Dirt{Tracked: 1},
		},
		{
			name: "untracked",
			mutate: func(t *testing.T, f *migrationFixture) {
				t.Helper()
				writeFile(t, filepath.Join(f.records, "changes", "active", "operator-note.md"), "untracked operator note\n")
			},
			dirt: Dirt{Untracked: 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMigrationFixture(t, false)
			tc.mutate(t, f)
			before := migrationSnapshot(t, f)

			plan, err := Build(f.config, f.manifest)
			if !errors.Is(err, ErrBlocked) || plan.Status != "blocked" || !hasBlocker(plan, "records_dirty") {
				t.Fatalf("Build dirty records = %v, status=%q blockers=%+v", err, plan.Status, plan.Blockers)
			}
			if plan.RecordsGit.Path != f.records || plan.RecordsGit.Head == "" || plan.RecordsGit.Dirt.Tracked != tc.dirt.Tracked || plan.RecordsGit.Dirt.Untracked != tc.dirt.Untracked || len(plan.Files) == 0 || len(plan.Records) == 0 || len(plan.Prospective.Operations) == 0 {
				t.Fatalf("blocked dirty-record inventory = %+v", plan)
			}
			if after := migrationSnapshot(t, f); !reflect.DeepEqual(before, after) {
				t.Fatalf("dirty-record planning changed fixture\nbefore: %#v\nafter: %#v", before, after)
			}
		})
	}
}

func TestBuildDoesNotRetainProspectiveForMixedBlockers(t *testing.T) {
	f := newMigrationFixture(t, false)
	replaceFile(t, filepath.Join(f.records, "changes", "active", "onto-state.yaml"), "phase: open", "phase: build")
	writeFile(t, f.manifest, "{}\n")

	plan, err := Build(f.config, f.manifest)
	if !errors.Is(err, ErrBlocked) || !hasBlocker(plan, "records_dirty") || !hasBlocker(plan, "manifest_invalid") {
		t.Fatalf("Build mixed blockers = %v, blockers=%+v", err, plan.Blockers)
	}
	if len(plan.Prospective.Operations) != 0 || len(plan.Prospective.Commits) != 0 {
		t.Fatalf("mixed-blocker plan exposed prospective authority: %+v", plan.Prospective)
	}
}

func TestBuildDisablesConfiguredFSMonitorHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("synthetic fsmonitor hook uses a POSIX shell")
	}
	f := newMigrationFixture(t, false)
	sentinels := []string{
		installMigrationFSMonitorSentinel(t, f.records, "records"),
		installMigrationFSMonitorSentinel(t, f.repos["app"], "source"),
	}
	before := migrationSnapshot(t, f)
	if _, err := Build(f.config, f.manifest); err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, sentinel := range sentinels {
		if _, err := os.Lstat(sentinel); !os.IsNotExist(err) {
			t.Fatalf("fsmonitor hook ran during planning: %s: %v", sentinel, err)
		}
	}
	if after := migrationSnapshot(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("fsmonitor-enabled planning changed fixture\nbefore: %#v\nafter: %#v", before, after)
	}
}

func installMigrationFSMonitorSentinel(t *testing.T, repo, name string) string {
	t.Helper()
	base := t.TempDir()
	sentinel := filepath.Join(base, name+"-fsmonitor-ran")
	hook := filepath.Join(base, name+"-fsmonitor-hook")
	writeFile(t, hook, fmt.Sprintf("#!/bin/sh\n: > %q\nprintf '0000000000000000000000000000000000000000\\n'\n", sentinel))
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "config", "--local", "core.fsmonitor", hook)
	command := exec.Command("git", "-C", repo, "status", "--porcelain=v1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("configured fsmonitor probe: %v\n%s", err, output)
	}
	if _, err := os.Lstat(sentinel); err != nil {
		t.Fatalf("configured fsmonitor hook did not run: %v", err)
	}
	if err := os.Remove(sentinel); err != nil {
		t.Fatal(err)
	}
	return sentinel
}

func TestBuildRejectsUnsupportedRecordScopes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(t *testing.T, f *migrationFixture)
		blocker string
	}{
		{
			name: "archive",
			mutate: func(t *testing.T, f *migrationFixture) {
				writeFile(t, filepath.Join(f.records, "changes", "archive", "2026-09-10-old", "onto-state.yaml"), "schema_version: 1\nchange: old\nid: archived-id\nphase: close\narchived: true\n")
			},
			blocker: "archives_unsupported",
		},
		{
			name: "to task",
			mutate: func(t *testing.T, f *migrationFixture) {
				writeFile(t, filepath.Join(f.records, "tasks", "legacy-task", "to-state.yaml"), "schema_version: 1\nchange: legacy-task\nphase: do\n")
			},
			blocker: "tasks_unsupported",
		},
		{
			name: "conversion recovery",
			mutate: func(t *testing.T, f *migrationFixture) {
				writeFile(t, filepath.Join(f.records, ".to-promote", "pending"), "pending\n")
			},
			blocker: "conversion_recovery_present",
		},
		{
			name: "migration journal",
			mutate: func(t *testing.T, f *migrationFixture) {
				writeFile(t, filepath.Join(f.records, ".workflow", "migrations", "run-1", "pending.json"), "{}\n")
			},
			blocker: "migration_journal_present",
		},
		{
			name: "conversion snapshot",
			mutate: func(t *testing.T, f *migrationFixture) {
				writeFile(t, filepath.Join(f.records, ".workflow", "snapshots", "operation", "onto", "onto-state.yaml"), "schema_version: 1\nchange: snapshot\nphase: build\n")
			},
			blocker: "conversion_recovery_present",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMigrationFixture(t, false)
			tc.mutate(t, f)
			before := migrationSnapshot(t, f)
			plan, err := Build(f.config, f.manifest)
			if !errors.Is(err, ErrBlocked) || !hasBlocker(plan, tc.blocker) {
				t.Fatalf("Build = %v, blockers=%+v, want %q", err, plan.Blockers, tc.blocker)
			}
			if after := migrationSnapshot(t, f); !reflect.DeepEqual(before, after) {
				t.Fatalf("blocked scope inspection changed fixture\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestBuildRefusesToFingerprintPotentialSecretRecordFiles(t *testing.T) {
	f := newMigrationFixture(t, false)
	secret := filepath.Join(f.records, "changes", "active", ".env")
	writeFile(t, secret, "TOKEN=must-not-enter-the-plan\n")
	before := migrationSnapshot(t, f)

	plan, err := Build(f.config, f.manifest)
	if !errors.Is(err, ErrBlocked) || !hasBlocker(plan, "sensitive_record_file") {
		t.Fatalf("Build = %v, blockers=%+v", err, plan.Blockers)
	}
	if hasFingerprint(plan.Files, secret, "workflow_record") {
		t.Fatal("potential secret-bearing file was fingerprinted")
	}
	if after := migrationSnapshot(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("sensitive-file inspection changed fixture\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestBuildBlocksDirtySensitiveSourceWithoutExposingItsContents(t *testing.T) {
	f := newMigrationFixture(t, false)
	secret := filepath.Join(f.repos["app"], ".env")
	writeFile(t, secret, "TOKEN=must-not-enter-the-plan\n")

	plan, err := Build(f.config, f.manifest)
	if !errors.Is(err, ErrBlocked) || !hasBlocker(plan, "sensitive_source_file") {
		t.Fatalf("Build = %v, blockers=%+v", err, plan.Blockers)
	}
	for _, blocker := range plan.Blockers {
		if blocker.Code == "sensitive_source_file" && blocker.Path != secret {
			t.Fatalf("sensitive source blocker path = %q, want %q", blocker.Path, secret)
		}
	}
	encoded, marshalErr := json.Marshal(plan)
	if marshalErr != nil || strings.Contains(string(encoded), "must-not-enter-the-plan") {
		t.Fatalf("plan exposed source secret: %v %s", marshalErr, encoded)
	}
}

func TestBuildPreservesSourceMetadataWithoutSourcePayloads(t *testing.T) {
	f := newMigrationFixture(t, false)
	const payload = "source-content-must-not-enter-the-plan"
	writeFile(t, filepath.Join(f.repos["app"], "notes.txt"), payload+"\n")

	plan, err := Build(f.config, f.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	source := findRecord(t, plan, "active-id").Sources[0]
	var note *PreservedFile
	for i := range source.Preservation.Files {
		if source.Preservation.Files[i].Path == "notes.txt" {
			note = &source.Preservation.Files[i]
			break
		}
	}
	if note == nil || note.Status != sourceStatusUntracked || note.Type != "regular" || note.Mode != 0o644 || note.SHA256 == "" {
		t.Fatalf("untracked source preservation = %+v", note)
	}
	encoded, marshalErr := json.Marshal(plan)
	if marshalErr != nil || strings.Contains(string(encoded), payload) {
		t.Fatalf("plan exposed source payload: %v %s", marshalErr, encoded)
	}
}

func TestPlanHashBindsConfigBaseAndRecordBytes(t *testing.T) {
	f := newMigrationFixture(t, false, false)
	initial, err := Build(f.config, f.manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.config, readFile(t, f.config)+"# config fingerprint changes\n")
	afterConfig, err := Build(f.config, f.manifest)
	if err != nil || afterConfig.PlanHash == initial.PlanHash {
		t.Fatalf("config update did not change plan hash: %q / %q (%v)", initial.PlanHash, afterConfig.PlanHash, err)
	}

	writeFile(t, filepath.Join(f.repos["app"], "next"), "next\n")
	runGit(t, f.repos["app"], "add", "next")
	runGit(t, f.repos["app"], "commit", "-m", "next")
	f.bases["app"] = gitTextTest(t, f.repos["app"], "rev-parse", "HEAD")
	f.input.Records[0].Sources[0].BaseRef = f.bases["app"]
	writeManifest(t, f)
	afterBase, err := Build(f.config, f.manifest)
	if err != nil || afterBase.PlanHash == afterConfig.PlanHash {
		t.Fatalf("base update did not change plan hash: %q / %q (%v)", afterConfig.PlanHash, afterBase.PlanHash, err)
	}

	state := filepath.Join(f.records, "changes", "active", "onto-state.yaml")
	replaceFile(t, state, "phase: build", "phase: design")
	runGit(t, f.records, "add", "changes/active/onto-state.yaml")
	runGit(t, f.records, "commit", "-m", "change records state")
	afterRecord, err := Build(f.config, f.manifest)
	if err != nil || afterRecord.PlanHash == afterBase.PlanHash {
		t.Fatalf("record update did not change plan hash: %q / %q (%v)", afterBase.PlanHash, afterRecord.PlanHash, err)
	}
	withoutStateBytes := afterRecord
	withoutStateBytes.Files = append([]FileFingerprint(nil), afterRecord.Files...)
	for i := range withoutStateBytes.Files {
		if withoutStateBytes.Files[i].Path != state {
			continue
		}
		withoutStateBytes.Files[i].SHA256 = strings.Repeat("0", 64)
		if withoutStateBytes.Files[i].SHA256 == afterRecord.Files[i].SHA256 {
			withoutStateBytes.Files[i].SHA256 = strings.Repeat("f", 64)
		}
		break
	}
	if planHash(withoutStateBytes) == afterRecord.PlanHash {
		t.Fatal("state file fingerprint does not bind the plan hash")
	}
}

func TestPlanHashBindsRecordWriteSummary(t *testing.T) {
	plan := newPlan()
	plan.Status = "ready"
	plan.RecordWrites = []RecordWrite{{
		Path:          "/records/changes/active/onto-state.yaml",
		Action:        RecordWriteTransformActive,
		PreSHA256:     strings.Repeat("a", 64),
		PostSHA256:    strings.Repeat("b", 64),
		ChangedFields: []string{"repo_bases", "repo_mode", "schema_version"},
		LegacyConfig: &ontostate.LegacyConfig{
			BaseRef: "legacy-ref", BaseBranch: "legacy-branch", Provenance: ontostate.LegacyConfigProvenance,
		},
	}}
	initial := planHash(plan)
	plan.RecordWrites[0].PostSHA256 = strings.Repeat("c", 64)
	if changed := planHash(plan); changed == initial {
		t.Fatal("record-write postimage digest did not affect plan hash")
	}
	plan.RecordWrites[0].PostSHA256 = strings.Repeat("b", 64)
	plan.RecordWrites[0].LegacyConfig.BaseRef = "different-legacy-ref"
	if changed := planHash(plan); changed == initial {
		t.Fatal("legacy config receipt did not affect plan hash")
	}
}

func newMigrationFixture(t *testing.T, retired bool, stateSchema3 ...bool) *migrationFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	schema3 := true
	if len(stateSchema3) != 0 {
		schema3 = stateSchema3[0]
	}
	base := t.TempDir()
	root := filepath.Join(base, "control")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := newGitRepo(t, filepath.Join(base, "source-app"), "main")
	baseRef := gitTextTest(t, repo, "rev-parse", "HEAD")
	config := filepath.Join(root, "homonto.toml")
	records := filepath.Join(root, ".homonto-local")
	writeFile(t, config, fmt.Sprintf("schema_version = 2\n[workflow]\nroot = \".homonto-local\"\ngit = \"existing\"\n[worktrees]\ndir = %q\n[repos]\napp = %q\n", filepath.Join(base, "execution"), repo))
	writeFile(t, filepath.Join(root, ".homonto", "workflow-root"), ".homonto-local\n")
	if err := os.MkdirAll(records, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, records, "init", "-b", "main")
	runGit(t, records, "config", "user.name", "Migration Test")
	runGit(t, records, "config", "user.email", "migration@example.invalid")
	writeFile(t, filepath.Join(records, "changes", "active", "onto-state.yaml"), activeState(baseRef, filepath.Join(repo, ".git"), schema3))
	writeFile(t, filepath.Join(records, "changes", "active", ".onto", "handoff.md"), "historical handoff\n")
	input := Manifest{
		Version:                ManifestVersion,
		ControlOnlyAttestation: ControlOnlyAttestation,
		Records: []ManifestRecord{{
			Path: "changes/active",
			ID:   "active-id",
			Sources: []ManifestSource{{
				Alias:        "app",
				BaseRef:      baseRef,
				BaseBranch:   "main",
				GitCommonDir: filepath.Join(repo, ".git"),
			}},
		}},
	}
	if retired {
		writeFile(t, filepath.Join(records, "changes", "active-retired", "onto-state.yaml"), "schema_version: 1\nchange: active\nid: retired-id\nphase: build\nabandoned: true\n")
		input.Records = append(input.Records, ManifestRecord{Path: "changes/active-retired", ID: "retired-id", Sources: []ManifestSource{}})
	}
	runGit(t, records, "add", ".")
	runGit(t, records, "commit", "-m", "records")
	fixture := &migrationFixture{
		root:     root,
		config:   config,
		records:  records,
		manifest: filepath.Join(base, "migration-manifest.json"),
		repos:    map[string]string{"app": repo},
		bases:    map[string]string{"app": baseRef},
		input:    input,
	}
	writeManifest(t, fixture)
	return fixture
}

func activeState(base, common string, schema3 bool) string {
	if !schema3 {
		return "schema_version: 2\nchange: active\nid: active-id\nphase: build\nrepos: [app]\n"
	}
	return fmt.Sprintf("schema_version: 3\nchange: active\nid: active-id\nphase: open\nrepos: [app]\nrepo_mode: explicit\nrepo_bases:\n  app:\n    base_ref: %s\n    base_branch: main\n    git_common_dir: %q\n", base, common)
}

func newGitRepo(t *testing.T, dir, branch string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-b", branch)
	runGit(t, dir, "config", "user.name", "Migration Test")
	runGit(t, dir, "config", "user.email", "migration@example.invalid")
	writeFile(t, filepath.Join(dir, "tracked"), "base\n")
	runGit(t, dir, "add", "tracked")
	runGit(t, dir, "commit", "-m", "base")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func gitTextTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}

func writeManifest(t *testing.T, f *migrationFixture) {
	t.Helper()
	data, err := json.MarshalIndent(f.input, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.manifest, string(data)+"\n")
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func replaceFile(t *testing.T, path, old, replacement string) {
	t.Helper()
	data := readFile(t, path)
	if !strings.Contains(data, old) {
		t.Fatalf("%s does not contain %q", path, old)
	}
	writeFile(t, path, strings.Replace(data, old, replacement, 1))
}

func findRecord(t *testing.T, plan Plan, id string) Record {
	t.Helper()
	for _, record := range plan.Records {
		if record.ID == id {
			return record
		}
	}
	t.Fatalf("record %q not found in %+v", id, plan.Records)
	return Record{}
}

func findRecordWrite(t *testing.T, plan Plan, path string) RecordWrite {
	t.Helper()
	for _, write := range plan.RecordWrites {
		if write.Path == path {
			return write
		}
	}
	t.Fatalf("record write %q not found in %+v", path, plan.RecordWrites)
	return RecordWrite{}
}

func findPreparedRecordWrite(t *testing.T, plan Plan, path string) preparedRecordWrite {
	t.Helper()
	for _, write := range plan.preparedRecordWrites {
		if write.summary.Path == path {
			return write
		}
	}
	t.Fatalf("prepared record write %q not found", path)
	return preparedRecordWrite{}
}

func hasFingerprint(files []FileFingerprint, path, classification string) bool {
	for _, file := range files {
		if file.Path == path && file.Classification == classification && file.SHA256 != "" {
			return true
		}
	}
	return false
}

func hasBlocker(plan Plan, code string) bool {
	for _, blocker := range plan.Blockers {
		if blocker.Code == code {
			return true
		}
	}
	return false
}

func prospectiveOperation(plan Plan, kind string) (ProspectiveOperation, bool) {
	for _, operation := range plan.Prospective.Operations {
		if operation.Kind == kind {
			return operation, true
		}
	}
	return ProspectiveOperation{}, false
}

func migrationSnapshot(t *testing.T, f *migrationFixture) map[string]string {
	t.Helper()
	paths := []string{f.config, f.manifest, filepath.Join(f.root, ".homonto", "workflow-root"), filepath.Join(f.records, ".git", "index")}
	for _, root := range []string{filepath.Join(f.root, ".homonto"), filepath.Join(f.records, "changes")} {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				t.Fatal(err)
			}
			if path == filepath.Join(f.root, ".homonto", "catalog") && entry.IsDir() {
				return filepath.SkipDir
			}
			if !entry.IsDir() && entry.Type().IsRegular() {
				paths = append(paths, path)
			}
			return nil
		})
	}
	for _, repo := range f.repos {
		paths = append(paths, filepath.Join(repo, ".git", "index"))
	}
	sort.Strings(paths)
	snapshot := make(map[string]string, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				snapshot[path] = "absent"
				continue
			}
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		snapshot[path] = hex.EncodeToString(sum[:])
	}
	return snapshot
}
