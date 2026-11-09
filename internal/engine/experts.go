package engine

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/Novirmor/homonto/internal/catalog"
)

func allExpertReferencesMatch(root string, skills []string, want []byte) bool {
	for _, name := range skills {
		if name != "expert" && name != catalog.SharedKnowledgeSkill {
			continue
		}
		p := filepath.Join(root, name, filepath.FromSlash(catalog.ExpertReferencePath))
		info, err := os.Lstat(p)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		got, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(got, want) {
			return false
		}
	}
	return true
}
