package ontocli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/evidence"
	"github.com/noviopenworks/homonto/internal/ontostate"
)

func TestScenarioDeclarationsRejectAmbiguousEvidence(t *testing.T) {
	const delta = "## ADDED Requirements\n### Requirement: first obligation\nRequirement-ID: REQ-first\n#### Scenario: first behavior\nScenario-ID: SC-shared\n- **GIVEN** input\n- **WHEN** processed\n- **THEN** the first behavior holds\n"
	otherDelta := strings.ReplaceAll(strings.ReplaceAll(delta, "first", "second"), "REQ-second", "REQ-other")
	for _, tc := range []struct {
		name, workflow, firstPath, first, secondPath, second, firstSite, secondSite string
	}{
		{"separate specs", "full", "specs/first.md", delta, "specs/second.md", otherDelta, "specs/first.md:5", "specs/second.md:5"},
		{"same spec distinct scenarios", "full", "specs/first.md", delta, "specs/first.md", delta + otherDelta, "specs/first.md:5", "specs/first.md:13"},
		{"preset tasks and report", "tweak", "tasks.md", "- [x] #1 verify\nScenario-ID: SC-shared\n", "verification.md", "Result: pass\nScenario-ID: SC-shared\n", "tasks.md:2", "verification.md:2"},
		{"preset duplicate task declarations", "fix", "tasks.md", "- [x] #1 verify\nScenario-ID: SC-shared\n", "tasks.md", "- [x] #1 verify\nScenario-ID: SC-shared\nAnother obligation:\nScenario-ID: SC-shared\n", "tasks.md:2", "tasks.md:4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := prepWorkspace(t)
			seedDocsLayout(t, root)
			changeDir := filepath.Join(changesDir(root), "ambiguous")
			st := ontostate.State{Change: "ambiguous", Workflow: tc.workflow, Phase: "verify"}
			if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{"proposal.md", "design.md", "plan.md"} {
				writeFile(t, filepath.Join(changeDir, file), "")
			}
			writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [x] #1 verify\n")
			writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
			writeFile(t, filepath.Join(changeDir, tc.firstPath), tc.first)
			args := []string{"evidence", "record", "ambiguous", "--dir", root, "--task", "1", "--scenario", "SC-shared", "--exec", "go", "--cmd-hash", cmdHash}
			// Existing audit claims sharing the same task must not collapse two
			// obligations when a duplicate declaration is introduced later.
			for i := 0; i < 2; i++ {
				if _, err := runOnto(t, args...); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(evidence.Path(changeDir))
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(changeDir, tc.secondPath), tc.second)
			_, err = runOnto(t, args...)
			if err == nil {
				t.Fatal("ambiguous evidence was accepted")
			}
			for _, want := range []string{"duplicate Scenario-ID", "SC-shared", tc.firstSite, tc.secondSite} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("rejection lacks %q: %v", want, err)
				}
			}
			after, err := os.ReadFile(evidence.Path(changeDir))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("rejected claim changed audit history")
			}
			out, err := runOnto(t, "trace", "ambiguous", "--json", "--dir", root)
			var graph traceGraph
			if err != nil || json.Unmarshal([]byte(out), &graph) != nil || len(graph.Findings) != 1 {
				t.Fatalf("trace lacks ambiguity finding: %s %v", out, err)
			}
			for _, site := range []string{tc.firstSite, tc.secondSite} {
				if !strings.Contains(graph.Findings[0], site) {
					t.Errorf("trace lacks declaration site %s: %v", site, graph.Findings)
				}
			}
			scenarios, records := map[string]bool{}, 0
			for _, node := range graph.Nodes {
				if node.Kind == "scenario" {
					scenarios[node.ID] = true
				}
				if node.Kind == "evidence" {
					records++
				}
			}
			if len(scenarios) != 2 || scenarios["SC-shared"] || records != 2 {
				t.Fatalf("trace conflated obligations or lost history: %+v", graph)
			}
			for _, edge := range graph.Edges {
				if edge.Kind == "verified-by" || edge.Kind == "superseded-by" {
					t.Errorf("ambiguous obligation got coverage/supersession: %+v", edge)
				}
			}
			if out, err := runOnto(t, "trace", "ambiguous", "--dir", root); err != nil || !strings.Contains(out, "finding: ambiguous: duplicate Scenario-ID") {
				t.Fatalf("text trace hid ambiguity: %s %v", out, err)
			}
			// Diagnosis must not depend on there already being an evidence sidecar.
			if err := os.Remove(evidence.Path(changeDir)); err != nil {
				t.Fatal(err)
			}
			out, err = runOnto(t, "doctor", "--dir", root)
			if err == nil || !strings.Contains(out, "duplicate Scenario-ID") || !strings.Contains(out, tc.firstSite) || !strings.Contains(out, tc.secondSite) {
				t.Fatalf("doctor lost declaration ambiguity: %s %v", out, err)
			}
			if _, err := runOnto(t, args...); err == nil {
				t.Fatal("first ambiguous evidence was accepted")
			}
			if _, err := os.Stat(evidence.Path(changeDir)); !os.IsNotExist(err) {
				t.Fatalf("refused first claim created a sidecar: %v", err)
			}
		})
	}
}

func TestScenarioDeclarationsIgnoreReferencesAndExamples(t *testing.T) {
	for _, workflow := range []string{"full", "fix", "tweak"} {
		t.Run(workflow, func(t *testing.T) {
			root := prepWorkspace(t)
			changeDir := filepath.Join(changesDir(root), "unique")
			st := ontostate.State{Change: "unique", Workflow: workflow, Phase: "verify"}
			if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
				t.Fatal(err)
			}
			const references = "SC-unique is checked again.\nSee Scenario-ID: SC-unique for the contract.\n`Scenario-ID: SC-unique`\n> Scenario-ID: SC-unique\n```markdown\nScenario-ID: SC-unique\n```\n~~~~markdown\nScenario-ID: SC-unique\n~~~~\n"
			writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [x] #1 verify SC-unique\n"+references)
			writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n"+references)
			writeFile(t, filepath.Join(changeDir, "proposal.md"), "Scenario-ID: SC-unique\n")
			writeFile(t, filepath.Join(changeDir, "notes.md"), "Scenario-ID: SC-unique\n")
			if workflow == "full" {
				writeFile(t, filepath.Join(changeDir, "specs", "unique.md"), "Scenario-ID: SC-unique\n## ADDED Requirements\n### Requirement: unique behavior\nRequirement-ID: REQ-unique\n#### Scenario: checked behavior\nScenario-ID: SC-unique\n"+references)
			} else {
				writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [x] #1 verify SC-unique\nScenario-ID: SC-unique\n"+references)
			}
			if index, err := loadScenarioIndex(changeDir, st); err != nil || len(index) != 1 || len(index["SC-unique"]) != 1 {
				t.Fatalf("references became declarations: %+v %v", index, err)
			}
			if _, err := runOnto(t, "evidence", "record", "unique", "--dir", root, "--task", "1", "--scenario", "SC-unique", "--exec", "go", "--cmd-hash", cmdHash); err != nil {
				t.Fatal(err)
			}
			if findings, _ := evidenceFindings(NewRootCmd(), root, changeDir, "unique"); len(findings) != 0 {
				t.Fatalf("unique contract got findings: %v", findings)
			}
			graph := buildTrace(NewRootCmd(), root, changesDir(root), []string{"unique"})
			if len(graph.Findings) != 0 {
				t.Fatalf("trace flagged references: %v", graph.Findings)
			}
			covered := false
			for _, edge := range graph.Edges {
				covered = covered || (edge.From == "scenario:SC-unique" && edge.Kind == "verified-by")
			}
			if !covered {
				t.Fatal("unique scenario lost trace coverage")
			}
		})
	}
}
