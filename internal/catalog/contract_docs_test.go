package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readContractDoc(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "contracts", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestContractDocsDescribeTheBuiltins keeps the published builtin tables and
// recorded exceptions in step with the shipped manifests.
func TestContractDocsDescribeTheBuiltins(t *testing.T) {
	c := embeddedForTestCatalog(t)
	engine := readContractDoc(t, "engine.md")
	routing := readContractDoc(t, "routing.md")
	for _, name := range []string{"onto", "to", "no"} {
		fw, _ := c.Framework(name)
		binary := "—"
		if fw.Engine.Binary != "" {
			binary = "`" + fw.Engine.Binary + "`"
		}
		row := "| `" + name + "` | " + fw.Engine.Lifecycle + " | " + binary + " | " + strings.Join(fw.Engine.Stages, ", ") + " |"
		if !strings.Contains(engine, row) {
			t.Errorf("engine.md lacks the row %q", row)
		}
		stages := "| `" + name + "` | " + strings.Join(fw.Engine.Stages, ", ") + " |"
		if !strings.Contains(routing, stages) {
			t.Errorf("routing.md lacks the stage row %q", stages)
		}
	}
	service := readContractDoc(t, "service.md")
	for _, name := range []string{"host-gh", "host-gl", "host-bb"} {
		fw, _ := c.Framework(name)
		row := "| `" + name + "` | `" + fw.Service.ID + "` | `" + fw.Service.Runtime + "` | `" + fw.Service.Transport + "` |"
		if !strings.Contains(service, row) {
			t.Errorf("service.md lacks the row starting %q", row)
		}
		// The recorded exception: builtin services contribute no rules.
		if !fw.Permissions.Empty() {
			t.Errorf("%s contributes permissions, but service.md records that builtin services contribute none", name)
		}
	}
	settings := readContractDoc(t, "settings.md")
	no, _ := c.Framework("no")
	iso := no.Settings["isolation"]
	enum := make([]string, len(iso.Enum))
	for i, v := range iso.Enum {
		enum[i] = "`" + v.(string) + "`"
	}
	row := "| `no` | `isolation` | " + strings.Join(enum, ", ") + " | `" + iso.Default.(string) + "` |"
	if !strings.Contains(settings, row) {
		t.Errorf("settings.md lacks the row %q", row)
	}
	// The only builtin package without the shared coordinator is no.
	workflow := readContractDoc(t, "engine.md")
	if !strings.Contains(workflow, "`no` has no coordinator") {
		t.Error("engine.md no longer records that no has no coordinator")
	}
}
