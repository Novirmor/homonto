// Package workflowstatus reads active and archived onto and to records for external
// observers. It is deliberately read-only: workflow transitions remain owned
// by the onto and to binaries.
package workflowstatus

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/bypasslog"
	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/integrationrecord"
	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workspace"
)

// Snapshot is the stable, machine-readable view consumed by the OpenCode
// workflow bridge. Empty slices encode as [] so consumers need no null cases.
type Snapshot struct {
	ConfigPath   string    `json:"configPath,omitempty"`
	WorkflowRoot string    `json:"workflowRoot,omitempty"`
	Changes      []Change  `json:"changes"`
	Findings     []Finding `json:"findings"`
}

// Change identifies a workflow generation, including its terminal disposition.
type Change struct {
	Identity       string   `json:"identity"`
	Status         string   `json:"status"`
	Path           string   `json:"path"`
	Workflow       string   `json:"workflow"`
	Name           string   `json:"name"`
	Phase          string   `json:"phase"`
	DerivedPhase   string   `json:"derivedPhase,omitempty"`
	TasksCompleted int      `json:"tasksCompleted"`
	TasksTotal     int      `json:"tasksTotal"`
	VerifyResult   string   `json:"verifyResult,omitempty"`
	Integration    string   `json:"integration,omitempty"`
	Pending        []string `json:"pending"`
}

// Finding is an observer-safe health problem. It is intentionally concise: the
// workflow doctor remains the detailed recovery interface.
type Finding struct {
	Workflow string `json:"workflow"`
	Change   string `json:"change,omitempty"`
	Message  string `json:"message"`
}

// Read returns workflow state for homonto.toml below root. Missing workflow trees are
// healthy empty state; malformed records become findings rather than preventing
// observers from reporting other active changes.
func Read(root string) Snapshot {
	return ReadConfig(filepath.Join(root, "homonto.toml"))
}

// ReadConfig preserves the selected filename. An invalid/missing configuration
// is a finding, never a fallback to a different, apparently healthy workspace.
func ReadConfig(configPath string) Snapshot {
	out := Snapshot{Changes: []Change{}, Findings: []Finding{}}
	out.ConfigPath, _ = filepath.Abs(configPath)
	l, err := workspace.Load(configPath)
	if err != nil {
		out.Findings = append(out.Findings, Finding{Workflow: "workspace", Message: err.Error()})
		return out
	}
	out.WorkflowRoot = l.WorkflowRoot
	if info, err := os.Stat(l.WorkflowRoot); os.IsNotExist(err) {
		return out // a valid configuration need not have initialized records yet
	} else if err != nil {
		out.Findings = append(out.Findings, Finding{Workflow: "workspace", Message: err.Error()})
		return out
	} else if err == nil && !info.IsDir() {
		out.Findings = append(out.Findings, Finding{Workflow: "workspace", Message: "workflow root is not a directory"})
		return out
	}
	if l.GitMode == "managed" {
		history, err := workspace.InspectHistory(l)
		if err != nil {
			out.Findings = append(out.Findings, Finding{Workflow: "workspace", Message: err.Error()})
		} else if history.Pending {
			out.Findings = append(out.Findings, Finding{Workflow: "workspace", Message: "workflow history pending; run homonto workspace recover"})
		}
	}
	readOnto(filepath.Join(l.WorkflowRoot, "changes"), false, &out)
	readTo(filepath.Join(l.WorkflowRoot, "tasks"), false, &out)
	sort.Slice(out.Changes, func(i, j int) bool {
		if out.Changes[i].Workflow == out.Changes[j].Workflow {
			if out.Changes[i].Name != out.Changes[j].Name {
				return out.Changes[i].Name < out.Changes[j].Name
			}
			return out.Changes[i].Path < out.Changes[j].Path
		}
		return out.Changes[i].Workflow < out.Changes[j].Workflow
	})
	sort.Slice(out.Findings, func(i, j int) bool {
		if out.Findings[i].Workflow == out.Findings[j].Workflow {
			return out.Findings[i].Change < out.Findings[j].Change
		}
		return out.Findings[i].Workflow < out.Findings[j].Workflow
	})
	return out
}

func readOnto(dir string, archived bool, out *Snapshot) {
	if err := fsutil.RequireRealParents(out.WorkflowRoot, dir); err != nil {
		out.Findings = append(out.Findings, Finding{Workflow: "onto", Message: err.Error()})
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			out.Findings = append(out.Findings, Finding{Workflow: "onto", Message: fmt.Sprintf("cannot read changes: %v", err)})
		}
		return
	}
	for _, entry := range entries {
		if !archived && entry.Name() == "archive" {
			readOnto(filepath.Join(dir, entry.Name()), true, out)
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			out.Findings = append(out.Findings, Finding{Workflow: "onto", Change: entry.Name(), Message: "symlinked workflow record"})
			continue
		}
		if !entry.IsDir() {
			continue
		}
		changeDir := filepath.Join(dir, entry.Name())
		state, class, classErr := ontostate.Classify(changeDir)
		if class == "valid" && !archived {
			retired, err := migrationrecord.IsRetired(out.WorkflowRoot, changeDir, state.ID)
			if err != nil {
				out.Findings = append(out.Findings, Finding{Workflow: "onto", Change: entry.Name(), Message: "retired migration record: " + err.Error()})
				continue
			}
			if retired {
				continue
			}
		}
		if class == "valid" && !archived && state.Change != entry.Name() {
			class, classErr = "invalid", fmt.Errorf("state identity does not match directory")
		}
		if class != "valid" {
			message := class
			if classErr != nil {
				message += ": " + classErr.Error()
			}
			out.Findings = append(out.Findings, Finding{Workflow: "onto", Change: entry.Name(), Message: message})
			continue
		}
		completed, total := checkboxProgress(filepath.Join(changeDir, "tasks.md"))
		derived := ontostate.DeriveWorkingPhase(changeDir, state)
		pending := ontoPending(state, completed, total)
		status := "active"
		if state.Abandoned {
			status, pending = "abandoned", []string{}
		} else if archived {
			pending = []string{}
			bypassed, err := bypasslog.ArchiveBypassed(changeDir, state.Change, "onto")
			switch {
			case err != nil:
				status = "unknown"
				out.Findings = append(out.Findings, Finding{Workflow: "onto", Change: state.Change, Message: err.Error()})
			case bypassed:
				status = "bypassed"
			case !state.Archived || state.Phase != "close":
				status = "interrupted"
				pending = append(pending, "recover interrupted archive")
			case ontostate.ArchiveIntegrationComplete(changeDir, state):
				status = "completed"
			default:
				status = "integration-pending"
				pending = append(pending, "complete integration")
				r, exists, err := integrationrecord.Load(changeDir, state.Change)
				if err != nil || !exists || r.Status == integrationrecord.StatusComplete {
					out.Findings = append(out.Findings, Finding{Workflow: "onto", Change: state.Change, Message: "missing, invalid, or mismatched integration record"})
				}
			}
		} else if state.Archived {
			status = "interrupted"
			pending = append(pending, "recover interrupted archive")
		}
		if status == "completed" || status == "abandoned" || status == "bypassed" {
			pending = []string{}
		} else if state.Verify.Result == "fail" {
			pending = append(pending, "resolve verification failure")
		}
		out.Changes = append(out.Changes, Change{
			Identity: changeIdentity(changeDir, state.ID, state.Change, state.Created), Status: status, Path: recordPath(out, changeDir),
			Workflow: "onto", Name: state.Change, Phase: state.Phase, DerivedPhase: derived,
			TasksCompleted: completed, TasksTotal: total, VerifyResult: state.Verify.Result,
			Integration: state.Integration, Pending: pending,
		})
	}
}

func readTo(dir string, archived bool, out *Snapshot) {
	if err := fsutil.RequireRealParents(out.WorkflowRoot, dir); err != nil {
		out.Findings = append(out.Findings, Finding{Workflow: "to", Message: err.Error()})
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			out.Findings = append(out.Findings, Finding{Workflow: "to", Message: fmt.Sprintf("cannot read tasks: %v", err)})
		}
		return
	}
	for _, entry := range entries {
		if !archived && entry.Name() == "archive" {
			readTo(filepath.Join(dir, entry.Name()), true, out)
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			out.Findings = append(out.Findings, Finding{Workflow: "to", Change: entry.Name(), Message: "symlinked workflow record"})
			continue
		}
		if !entry.IsDir() {
			continue
		}
		changeDir := filepath.Join(dir, entry.Name())
		state, err := tostate.Load(filepath.Join(changeDir, tostate.FileName))
		if err == nil {
			err = state.Validate()
		}
		if err == nil && !archived && state.Change != entry.Name() {
			err = fmt.Errorf("state identity does not match directory")
		}
		if err != nil {
			out.Findings = append(out.Findings, Finding{Workflow: "to", Change: entry.Name(), Message: err.Error()})
			continue
		}
		completed, total := checkboxProgress(filepath.Join(changeDir, "plan.md"))
		pending := []string{}
		if state.Phase == tostate.PhaseDo && completed < total {
			pending = append(pending, "complete planned tasks")
		}
		status := "active"
		if archived {
			switch {
			case state.Phase == tostate.PhaseAbandoned:
				status = "abandoned"
			case state.Phase == tostate.PhaseDone && state.Verified:
				status = "completed"
			case state.Phase == tostate.PhaseDone:
				status = "bypassed"
			default:
				status = "interrupted"
				out.Findings = append(out.Findings, Finding{Workflow: "to", Change: state.Change, Message: "non-terminal state in archive"})
			}
		} else if state.Phase == tostate.PhaseAbandoned {
			status = "abandoned"
		} else if state.Terminal() {
			status = "interrupted"
			pending = append(pending, "complete interrupted archive")
		}
		out.Changes = append(out.Changes, Change{
			Identity: changeIdentity(changeDir, state.ID, state.Change, state.Created), Status: status, Path: recordPath(out, changeDir),
			Workflow: "to", Name: state.Change, Phase: state.Phase,
			TasksCompleted: completed, TasksTotal: total, Pending: pending,
		})
	}
}

func recordPath(out *Snapshot, dir string) string {
	p, _ := filepath.Rel(out.WorkflowRoot, dir)
	return filepath.ToSlash(p)
}

// The directory inode survives state-file replacement and archive moves. Legacy
// states have only date-resolution creation times, insufficient for name reuse.
func changeIdentity(dir, id, name, created string) string {
	if id != "" {
		return id
	}
	if info, err := os.Stat(dir); err == nil {
		v := reflect.Indirect(reflect.ValueOf(info.Sys()))
		if v.IsValid() && v.Kind() == reflect.Struct {
			dev, ino := v.FieldByName("Dev"), v.FieldByName("Ino")
			if dev.IsValid() && ino.IsValid() {
				return fmt.Sprintf("legacy:%s:%s:%v:%v", name, created, dev.Interface(), ino.Interface())
			}
		}
	}
	return "legacy:" + name + ":" + created + ":" + dir
}

func ontoPending(state ontostate.State, completed, total int) []string {
	pending := []string{}
	if state.Workflow == "full" && state.Phase == "open" && state.ProposalApproved == "" {
		pending = append(pending, "approve proposal")
	}
	if state.Workflow == "full" && state.Phase == "design" && state.ApproachConfirmed == "" {
		pending = append(pending, "confirm approach")
	}
	if state.Phase == "build" && total > 0 && completed < total {
		pending = append(pending, "complete tasks")
	}
	if state.Phase == "close" && state.CloseConfirmed == "" {
		pending = append(pending, "confirm close")
	}
	return pending
}

func checkboxProgress(path string) (completed, total int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "- [ ] ") || strings.HasPrefix(line, "* [ ] ") {
			total++
		}
		if strings.HasPrefix(line, "- [x] ") || strings.HasPrefix(line, "- [X] ") || strings.HasPrefix(line, "* [x] ") || strings.HasPrefix(line, "* [X] ") {
			total++
			completed++
		}
	}
	return completed, total
}
