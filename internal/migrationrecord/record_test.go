package migrationrecord

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsRetiredRequiresEveryPreservedStateFileToMatch(t *testing.T) {
	root := t.TempDir()
	changeDir := filepath.Join(root, "changes", "retired")
	ontoPath := filepath.Join(changeDir, "onto-state.yaml")
	statePath := filepath.Join(changeDir, "state.yaml")
	onto := []byte("schema_version: 3\nid: retired-id\nchange: retired\nworkflow: full\nphase: build\nabandoned: true\n")
	state := []byte("schema_version: 3\nid: retired-id\nchange: retired\nworkflow: full\nphase: build\nabandoned: true\nextra: retained\n")
	writeTestFile(t, ontoPath, onto, 0o644)
	writeTestFile(t, statePath, state, 0o644)
	seedRetiredReceipt(t, root, "changes/retired", "retired-id", map[string][]byte{
		ontoPath:  onto,
		statePath: state,
	})

	retired, err := IsRetired(root, changeDir, "retired-id")
	if err != nil || !retired {
		t.Fatalf("IsRetired = %t, %v", retired, err)
	}
	writeTestFile(t, statePath, append(state, []byte("changed: true\n")...), 0o644)
	if _, err := IsRetired(root, changeDir, "retired-id"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("IsRetired after change = %v, want receipt-integrity error", err)
	}
}

func TestIsRetiredRejectsRawSchemaMismatch(t *testing.T) {
	root := t.TempDir()
	changeDir := filepath.Join(root, "changes", "retired")
	statePath := filepath.Join(changeDir, "onto-state.yaml")
	state := []byte("schema_version: 3\nid: retired-id\nchange: retired\nworkflow: full\nphase: build\nabandoned: true\n")
	writeTestFile(t, statePath, state, 0o644)
	seedRetiredReceipt(t, root, "changes/retired", "retired-id", map[string][]byte{statePath: state})

	if _, err := IsRetired(root, changeDir, "retired-id", 2); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("IsRetired schema mismatch = %v", err)
	}
}

func TestPublicRecordsRejectDuplicateJSONKeys(t *testing.T) {
	root := t.TempDir()
	const runID = "migration-12345678"
	seedRetiredReceipt(t, root, "changes/retired", "retired-id", map[string][]byte{
		filepath.Join(root, "changes", "retired", "onto-state.yaml"): []byte("state\n"),
	})
	receiptPath, err := ReceiptPath(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseReceipt([]byte(strings.Replace(string(receipt), `{"version":1`, `{"version":1,"version":1`, 1))); err == nil {
		t.Fatal("ParseReceipt accepted duplicate version keys")
	}
	proofPath, err := CommitProofPath(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCommitProof([]byte(strings.Replace(string(proof), `{"version":1`, `{"version":1,"version":1`, 1)), runID); err == nil {
		t.Fatal("ParseCommitProof accepted duplicate version keys")
	}
}

func TestPreparationJournalStatusBlocksOrdinaryLoaders(t *testing.T) {
	root := t.TempDir()
	const runID = "migration-12345678"
	path, err := JournalPath(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, path, preparationJournalStatus(root, runID), 0o600)
	status, err := LoadJournalStatus(root, runID)
	if err != nil || status.Phase != "preparing" {
		t.Fatalf("LoadJournalStatus = %+v, %v", status, err)
	}
	if err := ValidateBarrier(root); err == nil || !strings.Contains(err.Error(), "workspace migration pending") {
		t.Fatalf("ValidateBarrier preparing = %v", err)
	}
}

func TestIsPreparationOrphanRejectsPreparingStatusAndTemporaryFile(t *testing.T) {
	root := t.TempDir()
	const runID = "migration-12345678"
	path, err := JournalPath(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, path, preparationJournalStatus(root, runID), 0o600)
	writeTestFile(t, filepath.Join(filepath.Dir(path), ".homonto-migration-0123456789abcdef0123456789abcdef"), []byte("partial\n"), 0o600)

	orphan, err := IsPreparationOrphan(root, runID)
	if err != nil || orphan {
		t.Fatalf("IsPreparationOrphan = %t, %v, want false", orphan, err)
	}
}

func preparationJournalStatus(root, runID string) JournalStatus {
	configRoot := filepath.Join(root, "config")
	return JournalStatus{
		Version: JournalVersion,
		RunID:   runID,
		Phase:   "preparing",
		Preparation: &PreparationStatus{
			PlanHash:     strings.Repeat("a", 64),
			ConfigPath:   filepath.Join(configRoot, "homonto.toml"),
			ConfigRoot:   configRoot,
			WorkflowRoot: root,
			ManifestPath: filepath.Join(configRoot, "migration.yaml"),
			Owner:        strings.Repeat("b", 32),
			Guardian:     "process-guardian-v1",
		},
	}
}

func TestPreparationDirectoryWithoutStatusBlocksOrdinaryLoaders(t *testing.T) {
	root := t.TempDir()
	const runID = "migration-12345678"
	private := filepath.Join(root, ".workflow", "migrations", runID, "private")
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
	}
	orphan, err := IsPreparationOrphan(root, runID)
	if err != nil || !orphan {
		t.Fatalf("IsPreparationOrphan = %t, %v, want true", orphan, err)
	}
	if err := ValidateBarrier(root); err == nil || !strings.Contains(err.Error(), "workspace migration pending (preparing)") {
		t.Fatalf("ValidateBarrier partial preparation directory = %v", err)
	}
}

func TestPreparationDirectoryWithUnrecognizedTemporaryFileRemainsBlocked(t *testing.T) {
	root := t.TempDir()
	const runID = "migration-12345678"
	private := filepath.Join(root, ".workflow", "migrations", runID, "private")
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(private, ".homonto-migration-0123456789abcdef0123456789abcdef")
	if err := os.WriteFile(temporary, []byte("foreign\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	orphan, err := IsPreparationOrphan(root, runID)
	if err != nil || orphan {
		t.Fatalf("IsPreparationOrphan = %t, %v, want false", orphan, err)
	}
	if err := ValidateBarrier(root); err == nil || !strings.Contains(err.Error(), "migration journal is missing, malformed, or unsupported") {
		t.Fatalf("ValidateBarrier unrecognized preparation temporary = %v", err)
	}
	if data, err := os.ReadFile(temporary); err != nil || string(data) != "foreign\n" {
		t.Fatalf("unrecognized temporary changed: %q, %v", data, err)
	}
}

func seedRetiredReceipt(t *testing.T, root, retiredPath, stateID string, states map[string][]byte) {
	t.Helper()
	const runID = "migration-12345678"
	proofPath := filepath.Join(root, ".workflow", "migrations", runID, "commit-proof.json")
	writes := make([]RecordWrite, 0, len(states))
	anchor, anchorPath := "", ""
	for path, data := range states {
		digest := sha256.Sum256(data)
		encoded := hex.EncodeToString(digest[:])
		writes = append(writes, RecordWrite{Path: path, PreSHA256: encoded, PostSHA256: encoded, Action: "preserve_retired"})
		if anchorPath == "" || path < anchorPath {
			anchorPath = path
			anchor = encoded
		}
	}
	receipt := Receipt{
		Version: ReceiptVersion, RunID: runID, PlanHash: strings.Repeat("a", 64),
		Config:          FileRef{Path: filepath.Join(root, "homonto.toml"), SHA256: strings.Repeat("b", 64)},
		Manifest:        FileRef{Path: filepath.Join(root, "migration-manifest.json"), SHA256: strings.Repeat("c", 64)},
		LayoutMarker:    FileRef{Path: filepath.Join(root, ".homonto", "workflow-layout.json"), SHA256: strings.Repeat("d", 64)},
		Registry:        FileRef{Path: filepath.Join(root, ".homonto", "worktrees.json"), SHA256: strings.Repeat("e", 64)},
		RecordWrites:    writes,
		Retired:         []RetiredRecord{{Path: retiredPath, ID: stateID, SchemaVersion: 3, SHA256: anchor}},
		Bindings:        []Binding{},
		CommitProofPath: filepath.ToSlash(proofPath[len(root)+1:]),
	}
	writeTestJSON(t, filepath.Join(root, ".workflow", "migrations", runID, "private", "journal.json"), JournalStatus{Version: JournalVersion, RunID: runID, Phase: "complete"}, 0o600)
	writeTestJSON(t, filepath.Join(root, ".workflow", "migrations", runID, "receipt.json"), receipt, 0o644)
	writeTestJSON(t, proofPath, CommitProof{Version: ReceiptVersion, RunID: runID, MigrationCommit: strings.Repeat("1", 40), Parent: strings.Repeat("2", 40), Tree: strings.Repeat("3", 40), MessageSHA256: strings.Repeat("4", 64)}, 0o644)
	receiptData, err := os.ReadFile(filepath.Join(root, ".workflow", "migrations", runID, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	proofData, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatal(err)
	}
	witness, err := NewCompletionWitness(runID, receiptData, proofData)
	if err != nil {
		t.Fatal(err)
	}
	witnessPath, err := CompletionWitnessPath(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, witnessPath, witness, 0o600)
}

func writeTestFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func writeTestJSON(t *testing.T, path string, value any, mode os.FileMode) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, append(data, '\n'), mode)
}
