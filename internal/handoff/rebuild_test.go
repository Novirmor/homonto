package handoff

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/checkpoint"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// mustFingerprint digests a canonical config fixture.
func mustFingerprint(t *testing.T, cfg workspacecfg.Config) fingerprint.Digest {
	t.Helper()
	d, err := workspacecfg.Fingerprint(cfg)
	if err != nil {
		t.Fatalf("handoff: fingerprint: %v", err)
	}
	return d
}

func TestRuntimeKeyMintAndStability(t *testing.T) {
	db := newRuntimeDB(t)
	defer func() { _ = db.Close() }()

	key, err := RuntimeKey(context.Background(), db)
	if err != nil {
		t.Fatalf("handoff: runtime key: %v", err)
	}
	if err := identity.ValidateToken(string(key)); err != nil {
		t.Fatalf("handoff: runtime key form: %v", err)
	}
	again, err := RuntimeKey(context.Background(), db)
	if err != nil {
		t.Fatalf("handoff: runtime key reread: %v", err)
	}
	if again != key {
		t.Fatal("handoff: runtime key not stable across reads")
	}
}

func TestFreshnessTokenKeyIsolation(t *testing.T) {
	action, err := identity.NewActionID()
	if err != nil {
		t.Fatalf("handoff: action id: %v", err)
	}
	keyA, keyB := mustToken(t), mustToken(t)
	token := IssueFreshnessToken(keyA, action)
	if err := identity.ValidateToken(string(token)); err != nil {
		t.Fatalf("handoff: token form: %v", err)
	}
	if !VerifyFreshnessToken(keyA, action, token) {
		t.Fatal("handoff: token does not verify under its issuing key")
	}
	if VerifyFreshnessToken(keyB, action, token) {
		t.Fatal("handoff: token verifies under a foreign key")
	}
	other, err := identity.NewActionID()
	if err != nil {
		t.Fatalf("handoff: action id: %v", err)
	}
	if VerifyFreshnessToken(keyA, other, token) {
		t.Fatal("handoff: token verifies for a different action")
	}
}

func TestRebuildRuntimeStandalone(t *testing.T) {
	m := newMachine(t)
	root := filepath.Join(t.TempDir(), "ws")
	if err := copyDirsForRebuild(t, m, root); err != nil {
		t.Fatalf("handoff: stage root: %v", err)
	}

	cfg := m.cfg
	cp := readCP(t, root)
	mappings := []ConfirmedMapping{
		{RepositoryID: m.controlID, Path: root},
		{RepositoryID: m.memberA, Path: filepath.Join(root, "member-a")},
		{RepositoryID: m.memberB, Path: filepath.Join(root, "member-b")},
	}
	if err := RebuildRuntime(context.Background(), cfg, cp, mappings); err != nil {
		t.Fatalf("handoff: rebuild: %v", err)
	}

	db := openDB(t, root)
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	var memberCount, factCount int
	var key string
	err := db.View(ctx, func(tx *store.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM members`).Scan(&memberCount); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM facts`).Scan(&factCount); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, MetaRuntimeKey).Scan(&key)
	})
	if err != nil {
		t.Fatalf("handoff: rebuilt rows: %v", err)
	}
	if memberCount != 3 {
		t.Errorf("handoff: members = %d, want 3", memberCount)
	}
	if factCount < 7 {
		t.Errorf("handoff: facts = %d, want at least 7 (phase + 6 member facts)", factCount)
	}
	if err := identity.ValidateToken(key); err != nil {
		t.Errorf("handoff: runtime key: %v", err)
	}

	// Rebuild is idempotent: a second pass converges without error and
	// rotates to another runtime key (attach issues fresh tokens).
	before := key
	if err := RebuildRuntime(ctx, cfg, cp, mappings); err != nil {
		t.Fatalf("handoff: rebuild again: %v", err)
	}
	after := metaValue(t, root, MetaRuntimeKey)
	if after == "" || after == before {
		t.Errorf("handoff: rebuild kept the previous runtime key")
	}
}

// copyDirsForRebuild stages a minimal attachable root for RebuildRuntime:
// .homonto with the checkpoint and member directories.
func copyDirsForRebuild(t *testing.T, m *machine, root string) error {
	t.Helper()
	copyTree(t, m.root, root)
	cp := m.cp
	data, err := checkpoint.Encode(cp)
	if err != nil {
		return err
	}
	return writeFileMkdir(filepath.Join(root, ".homonto", "checkpoint.json"), data)
}

// writeFileMkdir writes data to path, creating parent directories.
func writeFileMkdir(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// newRuntimeDB opens a fresh runtime database in a temp directory.
func newRuntimeDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "runtime.db"), store.OpenOptions{})
	if err != nil {
		t.Fatalf("handoff: open db: %v", err)
	}
	return db
}

func mustHex(t *testing.T, seed byte) string {
	t.Helper()
	out := make([]byte, 40)
	for i := range out {
		out[i] = "0123456789abcdef"[int(seed)%16]
	}
	return string(out)
}

func mustDigest(t *testing.T) fingerprint.Digest {
	t.Helper()
	d := fingerprintOf(t.Name())
	return d
}
