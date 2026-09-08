package convert

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/tostate"
)

func TestDemotionPreservesExecutionScopeAndMultilineContracts(t *testing.T) {
	for _, explicitFinal := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit-final=%v", explicitFinal), func(t *testing.T) {
			l, original := provenanceWorkspace(t)
			ops := testOps()
			src, err := Run(Promote, l.ConfigRoot, original.Change, original.Change, ops)
			if err != nil {
				t.Fatal(err)
			}
			st, err := ontostate.LoadChange(src)
			if err != nil {
				t.Fatal(err)
			}
			st.Phase = "build"
			if err := ontostate.Save(filepath.Join(src, "onto-state.yaml"), st); err != nil {
				t.Fatal(err)
			}
			provenanceWrite(t, filepath.Join(src, "tasks.md"), "- [ ] 1.1 API parser [trace #4]\n- [ ] 1.10 UI parser [trace #9]\n")
			// Deliberately put 1.10 first; both repositories edit shared.go.
			plan := fmt.Sprintf(`## Task 1.10 - UI parser
- Owner: coordinator
- Repo: b
- Cwd: %s
- Files: shared.go
- Do: Implement the UI parser.
Preserve UI-specific errors as well.
- Verify: go test ./ui
  go test -race ./ui

## Task 1.1 - API parser
- Owner: implementer
- Repo: a
- Cwd: %s
- Files: shared.go,
  shared_test.go (parser fixtures)
- Do: Implement the API parser.
  Preserve streamed errors.

  Add the missing-body regression.
- Verify: go test ./api
  go vet ./api
`, l.Repos["b"], l.Repos["a"])
			final := "Final Verify: go test ./ui\n  go test -race ./ui"
			if explicitFinal {
				final = fmt.Sprintf("Final Verify: go -C %s test ./...\n  go -C %s test ./...\n  git diff --check", l.Repos["a"], l.Repos["b"])
				plan += "\n" + final + "\n"
			}
			provenanceWrite(t, filepath.Join(src, "plan.md"), plan)
			target, err := Run(Demote, l.ConfigRoot, original.Change, original.Change, ops)
			if err != nil {
				t.Fatal(err)
			}
			gotState, err := tostate.Load(filepath.Join(target, "to-state.yaml"))
			if err != nil || gotState.Phase != "do" || !reflect.DeepEqual(gotState.Repos, original.Repos) {
				t.Fatalf("translated state: %+v %v", gotState, err)
			}
			data, err := os.ReadFile(filepath.Join(target, "plan.md"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				fmt.Sprintf("- [ ] #4 API parser\n  - Owner: implementer\n  - Repo: a\n  - Cwd: %s\n  - Files: shared.go,\n    shared_test.go (parser fixtures)", l.Repos["a"]),
				fmt.Sprintf("- [ ] #9 UI parser\n  - Owner: coordinator\n  - Repo: b\n  - Cwd: %s\n  - Files: shared.go", l.Repos["b"]),
				"  - Change: Implement the API parser.\n    Preserve streamed errors.\n\n    Add the missing-body regression.",
				"  - Change: Implement the UI parser.\n    Preserve UI-specific errors as well.",
				"  - Verify: go test ./api\n    go vet ./api",
				"  - Verify: go test ./ui\n    go test -race ./ui",
				final,
			} {
				if !strings.Contains(string(data), want) {
					t.Fatalf("missing contract %q in:\n%s", want, data)
				}
			}
		})
	}
}

func TestDemotionDeferredTasksRemainActionable(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"DEFERRED to close: publish the notes", " "},
		{"SUPERSEDED: obsolete layout", "x"},
		{"SUPERSEDED: DEFERRED to close: obsolete publication task", "x"},
		{"(deferred, done at close 2026-09-08): published notes", "x"},
		{"Validated the layout", "x"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			root := t.TempDir()
			src := auditSource(t, root, Demote, "docs")
			provenanceWrite(t, filepath.Join(src, "tasks.md"), "- [x] 1.1 "+tc.title+" [trace #7]\n")
			plan := fmt.Sprintf("## Task 1.1 - Notes\n- Owner: coordinator\n- Repo: records\n- Cwd: %s\n- Files: guide.md\n- Do: Describe the supported layout\n- Verify: git diff --check\n", filepath.Join(root, "docs"))
			provenanceWrite(t, filepath.Join(src, "plan.md"), plan)
			target, err := Run(Demote, root, "docs", "docs", testOps())
			if err != nil {
				t.Fatal(err)
			}
			st, err := tostate.Load(filepath.Join(target, "to-state.yaml"))
			if err != nil || st.Phase != "do" {
				t.Fatalf("phase: %+v %v", st, err)
			}
			data, err := os.ReadFile(filepath.Join(target, "plan.md"))
			if err != nil {
				t.Fatal(err)
			}
			if want := "- [" + tc.want + "] #7 " + tc.title; !strings.Contains(string(data), want) {
				t.Fatalf("missing %q in %s", want, data)
			}
		})
	}
}

func TestPlanFieldContinuationPreservesNestedChecks(t *testing.T) {
	plan := "## Task 1.1 - Verify\n- Files: parser.go\n  - parser_test.go\n- Do: Cover both paths.\n  ```text\n  - preserve this nested instruction\n  ```\n- Verify: Run both checks:\n  - go test ./parser\n  - go vet ./parser\n\n## Task 1.10 - Unrelated\n- Verify: wrong check\n"
	fields := planContractFor(plan, "1.1")
	for key, want := range map[string]string{
		"Files":  "parser.go\n  - parser_test.go",
		"Do":     "Cover both paths.\n  ```text\n  - preserve this nested instruction\n  ```",
		"Verify": "Run both checks:\n  - go test ./parser\n  - go vet ./parser",
	} {
		if fields[key] != want {
			t.Fatalf("%s = %q, want %q", key, fields[key], want)
		}
	}
}
