package checkpoint

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/securefs"
)

const storeRel = ".homonto/checkpoint.json"

// newTestStore opens a securefs root over a temp dir whose .homonto slot
// directory already exists (securefs never creates directories) and
// returns the store plus the absolute path of the checkpoint slot.
func newTestStore(t *testing.T) (Store, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".homonto"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := securefs.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	st, err := NewStore(root, storeRel)
	if err != nil {
		t.Fatal(err)
	}
	return st, filepath.Join(dir, filepath.FromSlash(storeRel))
}

func TestNewStoreRejectsBadArguments(t *testing.T) {
	root, err := securefs.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := NewStore(nil, storeRel); err == nil {
		t.Error("NewStore accepted a nil root")
	}
	if _, err := NewStore(root, ""); err == nil {
		t.Error("NewStore accepted an empty path")
	}
}

func TestStoreWritePersistsCanonicalBytesAt0600(t *testing.T) {
	st, abs := newTestStore(t)
	cp := validCheckpoint()

	digest, err := st.Write(cp)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Encode(cp)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, want) {
		t.Errorf("on-disk bytes are not the canonical encoding:\n disk: %s\n want: %s", raw, want)
	}
	if got := fingerprint.Bytes("checkpoint", raw); digest != got {
		t.Errorf("Write digest = %s, want %s", digest, got)
	}
	info, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("checkpoint mode = %o, want 600", info.Mode().Perm())
	}
}

func TestStoreWriteDigestsCanonicalFormNotInputOrder(t *testing.T) {
	st, _ := newTestStore(t)
	sorted := validCheckpoint()
	sorted.UnresolvedGates = []string{"accept-finding", "approve-design", "z-gate"}
	d1, err := st.Write(sorted)
	if err != nil {
		t.Fatal(err)
	}
	shuffled := validCheckpoint()
	shuffled.Members = []Member{sorted.Members[1], sorted.Members[0]}
	shuffled.UnresolvedGates = []string{"z-gate", "approve-design", "accept-finding"}
	d2, err := st.Write(shuffled)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("digest changed with member/gate order: %s vs %s", d1, d2)
	}
}

func TestStoreRepairRewritesUnconditionally(t *testing.T) {
	st, abs := newTestStore(t)
	first := validCheckpoint()
	if _, err := st.Write(first); err != nil {
		t.Fatal(err)
	}
	// Corrupt the slot the way a torn write or careless edit would.
	root, err := securefs.OpenRoot(filepath.Dir(abs))
	if err != nil {
		t.Fatal(err)
	}
	if err := root.WriteAtomic("checkpoint.json", []byte("{\"schema_version\":1,"), 0o600); err != nil {
		t.Fatal(err)
	}
	root.Close()
	if _, _, err := Load(abs); err == nil {
		t.Fatal("corrupted checkpoint still loaded; test setup is wrong")
	}

	repaired := validCheckpoint()
	repaired.Work.Phase = "done"
	digest, err := st.Repair(repaired)
	if err != nil {
		t.Fatal(err)
	}
	got, gotDigest, err := Load(abs)
	if err != nil {
		t.Fatal(err)
	}
	if got.Work.Phase != "done" {
		t.Errorf("Repair did not restore the expected checkpoint; phase = %q", got.Work.Phase)
	}
	if digest != gotDigest {
		t.Errorf("Repair digest = %s, want %s", digest, gotDigest)
	}
}

func TestStoreRepairOverwritesDifferentValidCheckpoint(t *testing.T) {
	st, abs := newTestStore(t)
	if _, err := st.Write(validCheckpoint()); err != nil {
		t.Fatal(err)
	}
	next := validCheckpoint()
	next.Work = nil
	if _, err := st.Repair(next); err != nil {
		t.Fatal(err)
	}
	got, _, err := Load(abs)
	if err != nil {
		t.Fatal(err)
	}
	if got.Work != nil {
		t.Error("Repair did not overwrite the previous valid checkpoint")
	}
}

func TestLoadRoundTrip(t *testing.T) {
	st, abs := newTestStore(t)
	cp := validCheckpoint()
	digest, err := st.Write(cp)
	if err != nil {
		t.Fatal(err)
	}
	got, gotDigest, err := Load(abs)
	if err != nil {
		t.Fatal(err)
	}
	if got.Work == nil || got.Work.ID != cp.Work.ID {
		t.Errorf("Load lost the active work: %+v", got.Work)
	}
	if digest != gotDigest {
		t.Errorf("Load digest = %s, want %s", gotDigest, digest)
	}
}

func TestLoadRejectsTrailingAndUnknown(t *testing.T) {
	st, abs := newTestStore(t)
	cp := validCheckpoint()
	if _, err := st.Write(cp); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	torn := append(append([]byte{}, raw...), []byte(" {}")...)
	if err := os.WriteFile(abs, torn, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(abs); !errors.Is(err, ErrTrailingData) {
		t.Errorf("Load error = %v, want ErrTrailingData", err)
	}
}
