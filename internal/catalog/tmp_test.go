package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderTmpDeterministicAndNamesDir(t *testing.T) {
	a, b := RenderTmp(".tmp"), RenderTmp(".tmp")
	if !bytes.Equal(a, b) {
		t.Fatal("RenderTmp must be byte-deterministic for a given dir")
	}
	for _, want := range []string{".tmp", "never deletes", "without prompts"} {
		if !strings.Contains(string(a), want) {
			t.Errorf("tmp reference missing %q:\n%s", want, a)
		}
	}
	if string(RenderTmp("scratch/tmp")) == string(a) {
		t.Fatal("different dirs must render different bytes")
	}
}

func TestTmpFingerprintStatesDiffer(t *testing.T) {
	disabled := TmpFingerprint(false, "")
	dotTmp := TmpFingerprint(true, ".tmp")
	other := TmpFingerprint(true, "scratch/tmp")
	if disabled == dotTmp || dotTmp == other {
		t.Fatal("enabled/disabled and dir changes must change the fingerprint")
	}
	if TmpFingerprint(false, "") != disabled || TmpFingerprint(true, ".tmp") != dotTmp {
		t.Fatal("fingerprint must be stable for identical inputs")
	}
}

// The reference rides with dispatchers and the shared knowledge skill — never
// with phase skills — and only when [tmp] is declared.
func TestMaterializeWritesTmpRefToDispatchersAndSharedSkill(t *testing.T) {
	dst := t.TempDir()
	c := baseCatalog(t)
	names := []string{"onto", "onto-build", "homonto"}
	if err := c.Materialize(dst, names, "none", "none", ".tmp"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"onto", "homonto"} {
		ref := filepath.Join(dst, name, filepath.FromSlash(TmpReferencePath))
		data, err := os.ReadFile(ref)
		if err != nil {
			t.Fatalf("%s must carry the tmp reference: %v", name, err)
		}
		if !strings.Contains(string(data), ".tmp") {
			t.Errorf("%s reference must name the dir:\n%s", name, data)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "onto-build", filepath.FromSlash(TmpReferencePath))); err == nil {
		t.Fatal("a phase skill must not carry the tmp reference")
	}

	// Disabled: no reference anywhere, and a stale one from a prior enabled
	// run is removed by the wholesale directory rebuild.
	if err := c.Materialize(dst, names, "none", "none", ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dst, name, filepath.FromSlash(TmpReferencePath))); err == nil {
			t.Fatalf("%s kept a stale tmp reference after disable", name)
		}
	}
}
