package starter

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

const (
	combosDoc   = "../../docs/guides/supported-combinations.md"
	combosBegin = "<!-- BEGIN GENERATED COMBINATIONS (task docs-config) -->"
	combosEnd   = "<!-- END GENERATED COMBINATIONS -->"
)

func renderCombinations() string {
	var b strings.Builder
	b.WriteString(combosBegin + "\n\n")
	b.WriteString("| Host | Harness | Engines | Evidence a release needs |\n|---|---|---|---|\n")
	hostName := func(id string) string {
		if id == "" {
			return "none"
		}
		return host(id).Name + " (`" + host(id).Workflow + "`)"
	}
	for _, c := range Cells() {
		var ids []string
		for _, e := range CellEvidence(c[0], c[1]) {
			ids = append(ids, "`"+e.ID+"`")
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", hostName(c[0]), harnessName(c[1]), "any of "+strings.Join(Engines, ", "), strings.Join(ids, ", "))
	}
	b.WriteString("\n| Evidence | Produced by |\n|---|---|\n")
	seen := map[string]bool{}
	add := func(list []Evidence) {
		for _, e := range list {
			if !seen[e.ID] {
				seen[e.ID] = true
				fmt.Fprintf(&b, "| `%s` | %s |\n", e.ID, e.Check)
			}
		}
	}
	add(AllCells)
	for _, h := range Harnesses {
		add(HarnessEvidence[h.ID])
	}
	for _, h := range Hosts {
		add(HostEvidence[h.ID])
	}
	b.WriteString("\n" + combosEnd)
	return b.String()
}

// TestSupportedCombinationsDocMatchesTheMatrix keeps the published table
// equal to the matrix homonto init offers. HOMONTO_UPDATE_REFERENCE=1
// rewrites it.
func TestSupportedCombinationsDocMatchesTheMatrix(t *testing.T) {
	data, err := os.ReadFile(combosDoc)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	begin, end := strings.Index(doc, combosBegin), strings.Index(doc, combosEnd)
	if begin < 0 || end < begin {
		t.Fatalf("%s lacks the generated markers", combosDoc)
	}
	want := renderCombinations()
	if doc[begin:end+len(combosEnd)] == want {
		return
	}
	if os.Getenv("HOMONTO_UPDATE_REFERENCE") == "1" {
		if err := os.WriteFile(combosDoc, []byte(doc[:begin]+want+doc[end+len(combosEnd):]), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("%s is out of date with internal/starter; run `task docs-config`", combosDoc)
}

// TestEveryE2EEvidenceKeyIsAMatrixCell keeps each e2e/<cell> release key
// produced by a cell scripts/e2e-matrix.sh runs, from a suite that exists, so
// a key can never name a cell that no run can pass.
func TestEveryE2EEvidenceKeyIsAMatrixCell(t *testing.T) {
	matrix, err := os.ReadFile("../../scripts/e2e-matrix.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range Harnesses {
		for _, e := range HarnessEvidence[h.ID] {
			cell, ok := strings.CutPrefix(e.ID, "e2e/")
			if !ok {
				continue
			}
			suite, tool, _ := strings.Cut(cell, "/")
			if tool == "" {
				tool = `""`
			}
			if !strings.Contains(string(matrix), "_cell "+suite+" "+tool+"\n") {
				t.Errorf("%s: scripts/e2e-matrix.sh runs no %s cell", e.ID, cell)
			}
			if e.Check != "scripts/e2e-matrix.sh "+cell {
				t.Errorf("%s: check %q does not name its cell", e.ID, e.Check)
			}
			if _, err := os.Stat("../../test/e2e/suites/" + suite + ".sh"); err != nil {
				t.Errorf("%s: %v", e.ID, err)
			}
		}
	}
}

func TestEveryHostAndHarnessHasEvidence(t *testing.T) {
	for _, h := range Hosts {
		if len(HostEvidence[h.ID]) == 0 {
			t.Errorf("host %s has no release evidence", h.ID)
		}
	}
	for _, h := range Harnesses {
		if len(HarnessEvidence[h.ID]) == 0 {
			t.Errorf("harness %s has no release evidence", h.ID)
		}
	}
}
