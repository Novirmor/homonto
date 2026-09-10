package ontocli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
)

const retiredMigrationRunID = "migration-12345678"

func TestStatusCommandSkipsRetiredMigrationRecord(t *testing.T) {
	root := t.TempDir()
	workflow := filepath.Join(root, "docs")
	writeFile(t, filepath.Join(workflow, "changes", "active", "onto-state.yaml"), "schema_version: 3\nid: active-record\nchange: active\nworkflow: full\nphase: open\n")
	writeFile(t, filepath.Join(workflow, "changes", "retired-record", "onto-state.yaml"), "schema_version: 3\nid: retired-record\nchange: historical-duplicate\nworkflow: full\nphase: build\nabandoned: true\n")
	seedRetiredMigrationRecord(t, workflow, "changes/retired-record", "retired-record")

	out, err := runOnto(t, "status", "--dir", root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "active: open") {
		t.Fatalf("status omitted active record:\n%s", out)
	}
	if strings.Contains(out, "retired-record: build") {
		t.Fatalf("status discovered a retired record as active:\n%s", out)
	}

	// The preserved directory remains available to an explicit audit read.
	out, err = runOnto(t, "state", "retired-record", "--dir", root)
	if err != nil || !strings.Contains(out, "retired-record: build") {
		t.Fatalf("explicit retired state read = %q, %v", out, err)
	}
}

func TestActiveMigrationDiscoverySkipsRetiredRecord(t *testing.T) {
	root := t.TempDir()
	workflow := filepath.Join(root, "docs")
	writeFile(t, filepath.Join(workflow, "changes", "active", "onto-state.yaml"), "schema_version: 3\nid: active-record\nchange: active\nworkflow: full\nphase: open\n")
	writeFile(t, filepath.Join(workflow, "changes", "retired-record", "onto-state.yaml"), "schema_version: 3\nid: retired-record\nchange: historical-duplicate\nworkflow: full\nphase: build\nabandoned: true\n")
	seedRetiredMigrationRecord(t, workflow, "changes/retired-record", "retired-record")

	names, err := activeChangeNames(root, filepath.Join(workflow, "changes"))
	if err != nil {
		t.Fatalf("active change names: %v", err)
	}
	if want := []string{"active"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("active names = %v, want %v", names, want)
	}

	nodes, _, err := buildGraph(root)
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	for _, node := range nodes {
		if node.Change == "historical-duplicate" {
			t.Fatalf("graph discovered retired record: %+v", node)
		}
	}
}

func TestStatusRejectsChangedRetiredMigrationRecord(t *testing.T) {
	root := t.TempDir()
	workflow := filepath.Join(root, "docs")
	retiredState := filepath.Join(workflow, "changes", "retired-record", "onto-state.yaml")
	writeFile(t, retiredState, "schema_version: 3\nid: retired-record\nchange: historical-duplicate\nworkflow: full\nphase: build\nabandoned: true\n")
	seedRetiredMigrationRecord(t, workflow, "changes/retired-record", "retired-record")
	writeFile(t, retiredState, "schema_version: 3\nid: retired-record\nchange: historical-duplicate\nworkflow: full\nphase: open\nabandoned: true\n")

	_, err := runOnto(t, "status", "--dir", root)
	if err == nil || !strings.Contains(err.Error(), "retired migration record") {
		t.Fatalf("status with changed retired record = %v, want receipt-integrity error", err)
	}
}

func TestHandoffWriteRejectsRetiredMigrationRecord(t *testing.T) {
	root := prepWorkspace(t)
	workflow := filepath.Join(root, "docs")
	retiredState := filepath.Join(workflow, "changes", "retired-record", "onto-state.yaml")
	writeFile(t, retiredState, "schema_version: 3\nid: retired-record\nchange: historical-duplicate\nworkflow: full\nphase: build\nabandoned: true\n")
	seedRetiredMigrationRecord(t, workflow, "changes/retired-record", "retired-record")
	before := readFile(t, retiredState)

	_, err := runOnto(t, "handoff", "retired-record", "--write", "--dir", root)
	if err == nil || !strings.Contains(err.Error(), "retired migration record") {
		t.Fatalf("handoff write = %v, want audit-only refusal", err)
	}
	if got := readFile(t, retiredState); got != before {
		t.Fatalf("handoff changed retired state\nwant: %q\n got: %q", before, got)
	}
	if _, err := os.Stat(filepath.Join(workflow, "changes", "retired-record", ".onto", "handoff")); !os.IsNotExist(err) {
		t.Fatalf("handoff wrote retired record artifacts: %v", err)
	}
}

func TestBypassRejectsRetiredMigrationRecord(t *testing.T) {
	root := prepWorkspace(t)
	workflow := filepath.Join(root, "docs")
	retiredState := filepath.Join(workflow, "changes", "retired-record", "onto-state.yaml")
	writeFile(t, retiredState, "schema_version: 3\nid: retired-record\nchange: retired-record\nworkflow: full\nphase: build\nabandoned: true\n")
	seedRetiredMigrationRecord(t, workflow, "changes/retired-record", "retired-record")
	before := readFile(t, retiredState)

	_, err := runOnto(t, "bypass", "retired-record", "--to", "open", "--reason", "test", "--dir", root)
	if err == nil || !strings.Contains(err.Error(), "retired migration record") {
		t.Fatalf("bypass = %v, want audit-only refusal", err)
	}
	if got := readFile(t, retiredState); got != before {
		t.Fatalf("bypass changed retired state\nwant: %q\n got: %q", before, got)
	}
	if _, err := os.Stat(filepath.Join(workflow, "changes", "retired-record", ".onto", "bypass.json")); !os.IsNotExist(err) {
		t.Fatalf("bypass wrote retired record audit: %v", err)
	}
}

func TestScaleSetRejectsRetiredMigrationRecord(t *testing.T) {
	root := prepWorkspace(t)
	workflow := filepath.Join(root, "docs")
	retiredState := filepath.Join(workflow, "changes", "retired-record", "onto-state.yaml")
	writeFile(t, retiredState, "schema_version: 3\nid: retired-record\nchange: historical-duplicate\nworkflow: full\nphase: build\nabandoned: true\n")
	seedRetiredMigrationRecord(t, workflow, "changes/retired-record", "retired-record")
	before := readFile(t, retiredState)

	_, err := runOnto(t, "scale", "retired-record", "--set", "--dir", root)
	if err == nil || !strings.Contains(err.Error(), "retired migration record") {
		t.Fatalf("scale --set = %v, want audit-only refusal", err)
	}
	if got := readFile(t, retiredState); got != before {
		t.Fatalf("scale --set changed retired state\nwant: %q\n got: %q", before, got)
	}
}

func TestDoctorSiblingDuplicatesSkipsRetiredMigrationRecord(t *testing.T) {
	root := t.TempDir()
	workflow := filepath.Join(root, "docs")
	writeFile(t, filepath.Join(workflow, "changes", "retired-record", "onto-state.yaml"), "schema_version: 3\nid: retired-record\nchange: historical-duplicate\nworkflow: full\nphase: build\nabandoned: true\n")
	seedRetiredMigrationRecord(t, workflow, "changes/retired-record", "retired-record")
	if err := os.MkdirAll(filepath.Join(workflow, "tasks", "retired-record"), 0o755); err != nil {
		t.Fatal(err)
	}

	dupes, err := siblingActiveDuplicates(root)
	if err != nil {
		t.Fatalf("sibling active duplicates: %v", err)
	}
	if len(dupes) != 0 {
		t.Fatalf("retired record reserved sibling name: %v", dupes)
	}
}

func seedRetiredMigrationRecord(t *testing.T, workflow, retiredPath, stateID string) {
	t.Helper()
	state, err := os.ReadFile(filepath.Join(workflow, filepath.FromSlash(retiredPath), "onto-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(state)
	proofPath := filepath.Join(workflow, ".workflow", "migrations", retiredMigrationRunID, "commit-proof.json")
	receipt := migrationrecord.Receipt{
		Version:  migrationrecord.ReceiptVersion,
		RunID:    retiredMigrationRunID,
		PlanHash: strings.Repeat("a", 64),
		Config: migrationrecord.FileRef{
			Path: filepath.Join(filepath.Dir(workflow), "homonto.toml"), SHA256: strings.Repeat("b", 64),
		},
		Manifest: migrationrecord.FileRef{
			Path: filepath.Join(filepath.Dir(workflow), "migration-manifest.json"), SHA256: strings.Repeat("c", 64),
		},
		LayoutMarker: migrationrecord.FileRef{
			Path: filepath.Join(filepath.Dir(workflow), ".homonto", "workflow-layout.json"), SHA256: strings.Repeat("d", 64),
		},
		Registry: migrationrecord.FileRef{
			Path: filepath.Join(filepath.Dir(workflow), ".homonto", "worktrees.json"), SHA256: strings.Repeat("e", 64),
		},
		RecordWrites: []migrationrecord.RecordWrite{{
			Path: statePath(workflow, retiredPath), PreSHA256: hex.EncodeToString(digest[:]), PostSHA256: hex.EncodeToString(digest[:]), Action: "preserve_retired",
		}},
		Retired: []migrationrecord.RetiredRecord{{
			Path: retiredPath, ID: stateID, SHA256: hex.EncodeToString(digest[:]),
		}},
		Bindings:        []migrationrecord.Binding{},
		CommitProofPath: filepath.ToSlash(proofPath[len(workflow)+1:]),
	}
	if err := receipt.Validate(); err != nil {
		t.Fatalf("test receipt: %v", err)
	}
	proof := migrationrecord.CommitProof{
		Version:         migrationrecord.ReceiptVersion,
		RunID:           retiredMigrationRunID,
		MigrationCommit: strings.Repeat("1", 40),
		Parent:          strings.Repeat("2", 40),
		Tree:            strings.Repeat("3", 40),
		MessageSHA256:   strings.Repeat("4", 64),
	}
	if err := proof.Validate(retiredMigrationRunID); err != nil {
		t.Fatalf("test proof: %v", err)
	}
	writeMigrationJSON(t, filepath.Join(workflow, ".workflow", "migrations", retiredMigrationRunID, "private", "journal.json"), migrationrecord.JournalStatus{
		Version: migrationrecord.JournalVersion,
		RunID:   retiredMigrationRunID,
		Phase:   "complete",
	}, 0o600)
	writeMigrationJSON(t, filepath.Join(workflow, ".workflow", "migrations", retiredMigrationRunID, "receipt.json"), receipt, 0o644)
	writeMigrationJSON(t, proofPath, proof, 0o644)
}

func statePath(workflow, retiredPath string) string {
	return filepath.Join(workflow, filepath.FromSlash(retiredPath), "onto-state.yaml")
}

func writeMigrationJSON(t *testing.T, path string, value any, mode os.FileMode) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), mode); err != nil {
		t.Fatal(err)
	}
}
