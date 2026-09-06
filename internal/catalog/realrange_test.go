package catalog

import (
	"io/fs"
	"reflect"
	"sort"
	"testing"

	embedded "github.com/noviopenworks/homonto/catalog"
)

// TestNew_CatalogShipsOnlyNativeFrameworks pins the shipped-framework
// surface: the embedded catalog carries exactly the homonto-native
// frameworks — onto, to, and the h GitHub-intake companion that depends on
// both — (plus loose, framework-agnostic skills/commands indexed
// separately). comet, openspec, and superpowers were removed deliberately —
// a third-party framework reappearing here is a packaging regression, not a
// feature. (Ranged-dep and capability mechanics keep their fstest coverage
// in version_test.go and capabilities_test.go; no shipped framework
// exercises them anymore.)
func TestNew_CatalogShipsOnlyNativeFrameworks(t *testing.T) {
	if _, err := New(); err != nil {
		t.Fatalf("embedded catalog failed to load: %v", err)
	}
	entries, err := fs.ReadDir(embedded.FS, "frameworks")
	if err != nil {
		t.Fatalf("reading embedded frameworks dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if want := []string{"h", "onto", "to"}; !reflect.DeepEqual(names, want) {
		t.Errorf("shipped frameworks = %v, want exactly %v", names, want)
	}
}
