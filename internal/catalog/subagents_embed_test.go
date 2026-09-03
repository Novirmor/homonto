package catalog

import (
	"io/fs"
	"testing"

	embedded "github.com/noviopenworks/homonto/catalog"
)

func TestSubagentsEmbedded(t *testing.T) {
	for _, name := range []string{
		"onto", "onto-reviewer", "onto-explorer", "onto-implementer", "onto-skeptic",
		"to", "to-reviewer", "to-explorer", "to-implementer", "to-skeptic",
	} {
		p := "subagents/" + name + ".md"
		if _, err := fs.Stat(embedded.FS, p); err != nil {
			t.Errorf("%s not embedded: %v", p, err)
		}
	}
}
