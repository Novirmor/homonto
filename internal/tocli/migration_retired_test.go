package tocli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
)

const retiredMigrationRunID = "migration-12345678"

func TestStatusAllSkipsRetiredOntoMigrationRecord(t *testing.T) {
	root := t.TempDir()
	workflow := filepath.Join(root, "docs")
	writeFile(t, filepath.Join(workflow, "changes", "retired-record", "onto-state.yaml"), "schema_version: 3\nid: retired-record\nchange: historical-duplicate\nworkflow: full\nphase: build\nabandoned: true\n")
	seedRetiredOntoMigrationRecord(t, workflow, "changes/retired-record", "retired-record")

	out := run(t, false, "status", "--all", "--dir", root)
	if strings.Contains(out, "historical-duplicate") {
		t.Fatalf("combined status discovered retired onto record:\n%s", out)
	}
}

func TestDoctorSiblingDuplicatesSkipsRetiredOntoMigrationRecord(t *testing.T) {
	root := t.TempDir()
	workflow := filepath.Join(root, "docs")
	writeFile(t, filepath.Join(workflow, "changes", "retired-record", "onto-state.yaml"), "schema_version: 3\nid: retired-record\nchange: historical-duplicate\nworkflow: full\nphase: build\nabandoned: true\n")
	seedRetiredOntoMigrationRecord(t, workflow, "changes/retired-record", "retired-record")
	if err := os.MkdirAll(filepath.Join(workflow, "tasks", "retired-record"), 0o755); err != nil {
		t.Fatal(err)
	}

	dupes, err := siblingDuplicates(root, "changes")
	if err != nil {
		t.Fatalf("sibling duplicates: %v", err)
	}
	if len(dupes) != 0 {
		t.Fatalf("retired onto record reserved sibling name: %v", dupes)
	}
}

func seedRetiredOntoMigrationRecord(t *testing.T, workflow, retiredPath, stateID string) {
	t.Helper()
	state, err := os.ReadFile(filepath.Join(workflow, filepath.FromSlash(retiredPath), "onto-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(state)
	receiptPath := filepath.Join(workflow, ".workflow", "migrations", retiredMigrationRunID, "receipt.json")
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
			Path: retiredOntoStatePath(workflow, retiredPath), PreSHA256: hex.EncodeToString(digest[:]), PostSHA256: hex.EncodeToString(digest[:]), Action: "preserve_retired",
		}},
		Retired: []migrationrecord.RetiredRecord{{
			Path: retiredPath, ID: stateID, SchemaVersion: 3, SHA256: hex.EncodeToString(digest[:]),
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
	writeRetiredMigrationJSON(t, filepath.Join(workflow, ".workflow", "migrations", retiredMigrationRunID, "private", "journal.json"), migrationrecord.JournalStatus{
		Version: migrationrecord.JournalVersion,
		RunID:   retiredMigrationRunID,
		Phase:   "complete",
	}, 0o600)
	writeRetiredMigrationJSON(t, receiptPath, receipt, 0o644)
	writeRetiredMigrationJSON(t, proofPath, proof, 0o644)
	receiptData, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	proofData, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatal(err)
	}
	witness, err := migrationrecord.NewCompletionWitness(retiredMigrationRunID, receiptData, proofData)
	if err != nil {
		t.Fatal(err)
	}
	witnessPath, err := migrationrecord.CompletionWitnessPath(workflow, retiredMigrationRunID)
	if err != nil {
		t.Fatal(err)
	}
	writeRetiredMigrationJSON(t, witnessPath, witness, 0o600)
}

func retiredOntoStatePath(workflow, retiredPath string) string {
	return filepath.Join(workflow, filepath.FromSlash(retiredPath), "onto-state.yaml")
}

func writeRetiredMigrationJSON(t *testing.T, path string, value any, mode os.FileMode) {
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
