package convert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workcli"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func auditSource(t *testing.T, root, direction, name string) string {
	t.Helper()
	if direction == Promote {
		seedToChange(t, root, name)
		return filepath.Join(root, "docs", "tasks", name)
	}
	dir := filepath.Join(root, "docs", "changes", name)
	if err := ontostate.Save(filepath.Join(dir, "onto-state.yaml"), ontostate.State{Change: name, ID: "original-id", Workflow: "full", Phase: "build"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDemoteLegacyStateFilenameFreshAndResumed(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "resumed"}[resume], func(t *testing.T) {
			root := t.TempDir()
			src := auditSource(t, root, Demote, "legacy")
			if err := os.Rename(filepath.Join(src, "onto-state.yaml"), filepath.Join(src, "state.yaml")); err != nil {
				t.Fatal(err)
			}
			before, _ := digestActive(src)
			ops := testOps()
			if resume {
				stg, m, _, err := findOrStage(specs[Demote], root, filepath.Join(root, "docs"), src, "legacy", "legacy", ops)
				if err != nil {
					t.Fatal(err)
				}
				if err := buildWork(specs[Demote], filepath.Join(stg, "work"), src, m); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Run(Demote, root, "legacy", "legacy", ops); err != nil {
				t.Fatal(err)
			}
			restored, err := Run(Promote, root, "legacy", "legacy", ops)
			if err != nil {
				t.Fatal(err)
			}
			after, err := digestActive(restored)
			if err != nil || before != after {
				t.Fatalf("legacy snapshot changed: %s != %s, %v", before, after, err)
			}
		})
	}
}

func TestConversionNamesAndConsumedReceipts(t *testing.T) {
	for _, direction := range []string{Promote, Demote} {
		for _, scenario := range []string{"sibling", "target", "archive", "reused-source", "reused-identical-source", "retry", "staged-reused-source"} {
			t.Run(direction+"/"+scenario, func(t *testing.T) {
				root := t.TempDir()
				src := auditSource(t, root, direction, "source")
				before, _ := digestActive(src)
				target := "destination"
				spec := specs[direction]
				ops := testOps()
				switch scenario {
				case "sibling":
					auditSource(t, root, direction, target)
				case "target":
					provenanceWrite(t, filepath.Join(root, "docs", spec.to.dir, target, "keep"), "unrelated")
				case "archive":
					target = "archive"
				case "reused-source", "reused-identical-source", "retry":
					if _, err := Run(direction, root, "source", target, ops); err != nil {
						t.Fatal(err)
					}
					if scenario != "retry" {
						auditSource(t, root, direction, "source")
					}
					if scenario == "reused-source" {
						if direction == Promote {
							path := filepath.Join(src, "to-state.yaml")
							st, err := tostate.Load(path)
							if err != nil {
								t.Fatal(err)
							}
							st.ID = "new-generation"
							if err := tostate.Save(path, st); err != nil {
								t.Fatal(err)
							}
						} else {
							path := filepath.Join(src, "onto-state.yaml")
							st, err := ontostate.Load(path)
							if err != nil {
								t.Fatal(err)
							}
							st.ID = "new-generation"
							if err := ontostate.Save(path, st); err != nil {
								t.Fatal(err)
							}
						}
						before, _ = digestActive(src)
					}
				case "staged-reused-source":
					stg, m, _, err := findOrStage(spec, root, filepath.Join(root, "docs"), src, "source", target, ops)
					if err != nil {
						t.Fatal(err)
					}
					if err := buildWork(spec, filepath.Join(stg, "work"), src, m); err != nil {
						t.Fatal(err)
					}
					auditSource(t, root, direction, "source")
				}
				_, err := Run(direction, root, "source", target, ops)
				if scenario == "retry" {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				if err == nil {
					t.Fatal("collision or reused source accepted")
				}
				after, err := digestActive(src)
				if err != nil || before != after {
					t.Fatalf("blocked conversion changed source: %v", err)
				}
			})
		}
	}
}

func TestRestoreEveryHistoryMoveBoundary(t *testing.T) {
	for _, boundary := range []string{"manifest", "work", "partial-snapshot", "lifted-partial-snapshot", "snapshot", "events", "lineage", "control", "updated-lineage", "receipt"} {
		for _, tamper := range []bool{false, true} {
			t.Run(boundary+map[bool]string{false: "", true: "/tampered"}[tamper], func(t *testing.T) {
				root := t.TempDir()
				seedToChange(t, root, "old")
				original, _ := digestActive(filepath.Join(root, "docs", "tasks", "old"))
				ops := testOps()
				if _, err := Run(Promote, root, "old", "new", ops); err != nil {
					t.Fatal(err)
				}
				stg, snap := stageRestore(t, root)
				m, _, err := readManifest(specs[Demote], stg)
				if err != nil {
					t.Fatal(err)
				}
				m.At = "2026-09-08T12:34:56.123456789Z"
				if err := writeJSON(filepath.Join(stg, manifestFile), m); err != nil {
					t.Fatal(err)
				}
				work, restored := filepath.Join(stg, "work"), filepath.Join(stg, "restored")
				ctl, newCtl := filepath.Join(work, controlDir), filepath.Join(restored, controlDir)
				if boundary == "manifest" {
					if err := os.Rename(work, filepath.Join(root, "docs", "changes", "new")); err != nil {
						t.Fatal(err)
					}
					snap = filepath.Join(root, "docs", "changes", "new", controlDir, snapshotsDir, m.SourceOperationID, "to")
				} else if boundary != "work" {
					if err := os.MkdirAll(restored, 0755); err != nil {
						t.Fatal(err)
					}
					if boundary == "partial-snapshot" || boundary == "lifted-partial-snapshot" {
						if err := os.Rename(filepath.Join(snap, "plan.md"), filepath.Join(restored, "plan.md")); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := moveContents(snap, restored); err != nil {
							t.Fatal(err)
						}
					}
				}
				if boundary == "events" || boundary == "lineage" {
					if err := os.MkdirAll(newCtl, 0755); err != nil {
						t.Fatal(err)
					}
					name := eventsDir
					if boundary == "lineage" {
						name = lineageFile
					}
					if err := os.Rename(filepath.Join(ctl, name), filepath.Join(newCtl, name)); err != nil {
						t.Fatal(err)
					}
				}
				if boundary == "control" || boundary == "updated-lineage" || boundary == "receipt" || boundary == "lifted-partial-snapshot" {
					if err := os.Rename(ctl, newCtl); err != nil {
						t.Fatal(err)
					}
				}
				if boundary == "updated-lineage" || boundary == "receipt" {
					lin, _, err := loadLineage(restored)
					if err != nil {
						t.Fatal(err)
					}
					lin.Events = append(lin.Events, m.OperationID)
					lin.CurrentWorkflow = "to"
					if err := writeJSON(filepath.Join(newCtl, lineageFile), lin); err != nil {
						t.Fatal(err)
					}
					if boundary == "receipt" {
						if err := writeJSON(filepath.Join(newCtl, eventsDir, m.OperationID+".json"), event{OperationID: m.OperationID}); err != nil {
							t.Fatal(err)
						}
					}
				}
				if tamper {
					path := filepath.Join(restored, "plan.md")
					if boundary == "manifest" || boundary == "work" {
						path = filepath.Join(snap, "plan.md")
					}
					provenanceWrite(t, path, "tampered")
				}
				target, err := Run(Demote, root, "new", "old", ops)
				if tamper {
					if err == nil {
						t.Fatal("tampered intermediate restored")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				got, err := digestActive(target)
				if err != nil || got != original {
					t.Fatalf("restored bytes: %s %v", got, err)
				}
				lin, _, _ := loadLineage(target)
				e, ok, err := latestEvent(target)
				if err != nil || !ok || len(lin.Events) != 2 || e.At != m.At || !e.Restored {
					t.Fatalf("restore receipt: %+v, lineage %+v, %v", e, lin, err)
				}
				if _, err := Run(Demote, root, "new", "old", ops); err != nil {
					t.Fatal(err)
				}
				again, _, _ := latestEvent(target)
				if again.At != m.At {
					t.Fatal("retry changed restore time")
				}
			})
		}
	}
}

func TestRestoreTimeBelongsToNewOperation(t *testing.T) {
	root := t.TempDir()
	seedToChange(t, root, "old")
	ops := testOps()
	created, err := Run(Promote, root, "old", "new", ops)
	if err != nil {
		t.Fatal(err)
	}
	e, _, _ := latestEvent(created)
	e.At = "2000-01-01T00:00:00Z"
	if err := writeJSON(filepath.Join(created, controlDir, eventsDir, e.OperationID+".json"), e); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	target, err := Run(Demote, root, "new", "old", ops)
	if err != nil {
		t.Fatal(err)
	}
	restored, _, _ := latestEvent(target)
	at, err := time.Parse(time.RFC3339Nano, restored.At)
	if err != nil || at.Before(start) || at.After(time.Now().UTC()) {
		t.Fatalf("restore time = %s: %v", restored.At, err)
	}
}

func TestDemotionRequiresRealCanonicalContracts(t *testing.T) {
	const tasks = "- [ ] 1.1 Add parser [trace #4]\n- [x] 1.10 Validate parser [trace #9]\n"
	const plan = "## Task 1.10 - Validate parser\n- Owner: implementer\n- Repo: app\n- Cwd: `/src/app`\n- Files: parser_test.go\n- Do: Test parser failures and successes\n- Verify: `go test ./...`\n\n## Task 1.1 - Add parser\n- Owner: implementer\n- Repo: app\n- Cwd: `/src/app`\n- Files: parser.go\n- Do: Implement the parser from design section 2\n- Verify: `go test ./parser -run Parse`\n"
	for _, scenario := range []string{"canonical", "explicit-final", "missing-plan", "missing-owner", "missing-repo", "missing-cwd", "relative-cwd", "missing-files", "missing-do", "missing-verify", "placeholder", "wrong-heading", "duplicate-heading", "partial-task", "malformed-task"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			src := auditSource(t, root, Demote, "parser")
			p, ts := plan, tasks
			switch scenario {
			case "explicit-final":
				p += "\nFinal Verify: `make test`\n"
			case "missing-plan":
				p = ""
			case "missing-owner":
				p = strings.Replace(p, "- Owner: implementer\n", "", 1)
			case "missing-repo":
				p = strings.Replace(p, "- Repo: app\n", "", 1)
			case "missing-cwd":
				p = strings.Replace(p, "- Cwd: `/src/app`\n", "", 1)
			case "relative-cwd":
				p = strings.Replace(p, "`/src/app`", "`app`", 1)
			case "missing-files":
				p = strings.Replace(p, "- Files: parser.go\n", "", 1)
			case "missing-do":
				p = strings.Replace(p, "- Do: Implement the parser from design section 2\n", "", 1)
			case "missing-verify":
				p = strings.Replace(p, "- Verify: `go test ./parser -run Parse`\n", "", 1)
			case "placeholder":
				p = strings.Replace(p, "parser.go", "<exact paths>", 1)
			case "wrong-heading":
				p = strings.Replace(p, "## Task 1.1 -", "## Task 1.100 -", 1)
			case "duplicate-heading":
				p += plan
			case "partial-task":
				ts += "- [ ] 2.1 Missing contract\n"
			case "malformed-task":
				ts += "- [ ] unnumbered task\n"
			}
			provenanceWrite(t, filepath.Join(src, "tasks.md"), ts)
			provenanceWrite(t, filepath.Join(src, "plan.md"), p)
			target, err := Run(Demote, root, "parser", "parser", testOps())
			if err != nil {
				t.Fatal(err)
			}
			st, err := tostate.Load(filepath.Join(target, "to-state.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "canonical" && scenario != "explicit-final" {
				if st.Phase != "plan" {
					t.Fatalf("incomplete contract entered %s", st.Phase)
				}
				return
			}
			if st.Phase != "do" {
				t.Fatalf("canonical contract entered %s", st.Phase)
			}
			b, _ := os.ReadFile(filepath.Join(target, "plan.md"))
			final := "Final Verify: `go test ./...`"
			if scenario == "explicit-final" {
				final = "Final Verify: `make test`"
			}
			for _, want := range []string{"- [ ] #4 Add parser\n  - Owner: implementer\n  - Repo: app\n  - Cwd: `/src/app`\n  - Files: parser.go\n  - Change: Implement the parser from design section 2\n  - Verify: `go test ./parser -run Parse`", "- [x] #9 Validate parser", final} {
				if !strings.Contains(string(b), want) {
					t.Fatalf("missing %q in %s", want, b)
				}
			}
		})
	}
}

func TestDemoteResumesOldDoManifestConservatively(t *testing.T) {
	root := t.TempDir()
	src := auditSource(t, root, Demote, "incomplete")
	provenanceWrite(t, filepath.Join(src, "tasks.md"), "- [ ] 1.1 Implement something\n")
	provenanceWrite(t, filepath.Join(src, "plan.md"), "## Task 1.1 - Implement something\n- Files: parser.go\n- Do: Implement the parser\n- Verify: go test ./...\n")
	stg, m, _, err := findOrStage(specs[Demote], root, filepath.Join(root, "docs"), src, "incomplete", "incomplete", testOps())
	if err != nil {
		t.Fatal(err)
	}
	m.TargetIdent.Phase = "do"
	if err := writeJSON(filepath.Join(stg, manifestFile), m); err != nil {
		t.Fatal(err)
	}
	if err := buildWork(specs[Demote], filepath.Join(stg, "work"), src, m); err != nil {
		t.Fatal(err)
	}
	target, err := Run(Demote, root, "incomplete", "incomplete", testOps())
	if err != nil {
		t.Fatal(err)
	}
	st, err := tostate.Load(filepath.Join(target, "to-state.yaml"))
	if err != nil || st.Phase != "plan" {
		t.Fatalf("resumed phase: %+v %v", st, err)
	}
	e, _, err := latestEvent(target)
	if err != nil || e.To.Phase != "plan" || e.TargetIdentity.Phase != "plan" {
		t.Fatalf("receipt phase: %+v %v", e, err)
	}
}

func TestRestoreRejectsUnexpectedStagingAndReusedPreMoveSource(t *testing.T) {
	for _, scenario := range []string{"extra", "reused-source"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			seedToChange(t, root, "old")
			ops := testOps()
			if _, err := Run(Promote, root, "old", "new", ops); err != nil {
				t.Fatal(err)
			}
			stg, _ := stageRestore(t, root)
			preserve := filepath.Join(stg, "unknown")
			if scenario == "reused-source" {
				src := filepath.Join(root, "docs", "changes", "new")
				if err := os.Rename(filepath.Join(stg, "work"), src); err != nil {
					t.Fatal(err)
				}
				preserve = filepath.Join(src, "onto-state.yaml")
				st, err := ontostate.Load(preserve)
				if err != nil {
					t.Fatal(err)
				}
				st.ID = "reused-generation"
				if err := ontostate.Save(preserve, st); err != nil {
					t.Fatal(err)
				}
			} else {
				provenanceWrite(t, preserve, "unknown bytes")
			}
			before, err := os.ReadFile(preserve)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Run(Demote, root, "new", "old", ops); err == nil {
				t.Fatal("unsafe restore accepted")
			}
			after, err := os.ReadFile(preserve)
			if err != nil || string(before) != string(after) {
				t.Fatalf("blocked restore moved or modified user bytes: %v", err)
			}
		})
	}
}

func TestConversionAndWorktreeShareLifecycleReservation(t *testing.T) {
	l, st := provenanceWorkspace(t)
	unlock, err := workcli.LockChangeNames(l.ConfigRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Promote, l.ConfigRoot, st.Change, st.Change, testOps()); err == nil {
		t.Fatal("conversion ignored name lock")
	}
	if _, err := workspace.CreateWorktree(l, "to", st.Change, "a", "main", "work/test"); err == nil {
		t.Fatal("create ignored name lock")
	}
	unlock()
	start := make(chan struct{})
	converted, allocated := make(chan error, 1), make(chan error, 1)
	go func() {
		<-start
		_, err := Run(Promote, l.ConfigRoot, st.Change, st.Change, testOps())
		converted <- err
	}()
	go func() {
		<-start
		_, err := workspace.CreateWorktree(l, "to", st.Change, "a", "main", "work/test")
		allocated <- err
	}()
	close(start)
	convertErr, createErr := <-converted, <-allocated
	if convertErr == nil && createErr == nil {
		t.Fatal("conversion and source binding both installed")
	}
	if createErr == nil {
		if _, err := workspace.ListWorktrees(l); err != nil {
			t.Fatalf("stranded binding: %v", err)
		}
	} else if convertErr == nil {
		if _, err := os.Stat(filepath.Join(l.WorkflowRoot, "changes", st.Change, "onto-state.yaml")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConversionRefusesReceiverBindingWithoutRebinding(t *testing.T) {
	l, st := provenanceWorkspace(t)
	provenanceGit(t, l.Repos["a"], "switch", "-c", "original-work")
	w, err := workspace.ReceiverWorktree(l, "to", st.Change, "a")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(l.WorkflowRoot, "tasks", st.Change)
	before, err := digestActive(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Promote, l.ConfigRoot, st.Change, st.Change, testOps()); err == nil || !strings.Contains(err.Error(), "registered worktree binding") {
		t.Fatalf("bound conversion: %v", err)
	}
	after, err := digestActive(src)
	if err != nil || before != after {
		t.Fatalf("blocked conversion changed source: %v", err)
	}
	entries, err := workspace.ListWorktrees(l)
	if err != nil || len(entries) != 1 || entries[0] != w {
		t.Fatalf("receiver was rebound: %+v %v", entries, err)
	}
}
