package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
)

const (
	wsID  = identity.WorkspaceID("3f3f8d4e-5f60-4a71-9cde-0123456789ab")
	ctlID = identity.RepositoryID("4a4a9e5f-6071-4b82-8def-123456789abc")
)

func TestCanonicalPath(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	mkdir(t, real)
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"resolves symlinks", link, CanonicalPathOf(t, real)},
		{"cleans dot dot", filepath.Join(base, "real", "..", "real"), CanonicalPathOf(t, real)},
		{"missing path stays lexical", filepath.Join(base, "ghost", "..", "real"), CanonicalPathOf(t, real)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalPath(tt.in)
			if err != nil {
				t.Fatalf("CanonicalPath: %v", err)
			}
			if got != tt.want {
				t.Errorf("CanonicalPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
