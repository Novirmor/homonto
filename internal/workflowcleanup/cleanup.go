// Package workflowcleanup owns terminal-record cleanup independently of agent
// prompts. Cleanup never completes, abandons, verifies or integrates source work.
package workflowcleanup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/applylock"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workcli"
	"github.com/noviopenworks/homonto/internal/workflowstatus"
	"github.com/noviopenworks/homonto/internal/workspace"
)

const SchemaVersion = 1

// Plan is an observation, not a transferable authorization. Apply requires the
// exact generation identity and rechecks the live record and every worktree.
type Plan struct {
	SchemaVersion int                         `json:"schema_version"`
	Workflow      string                      `json:"workflow"`
	Change        string                      `json:"change"`
	Identity      string                      `json:"identity"`
	Lifecycle     string                      `json:"lifecycle"`
	Status        string                      `json:"status"`
	Record        string                      `json:"record"`
	Archive       string                      `json:"archive,omitempty"`
	Worktrees     []workspace.WorktreeRemoval `json:"worktrees"`
	// Tmp is the change's own scratch folder (<tmp>/<workflow>/<change>),
	// deleted with the rest of the cleanup (ADR 0086).
	Tmp      string   `json:"tmp,omitempty"`
	Blockers []string `json:"blockers"`
}

type Inventory struct {
	SchemaVersion int                      `json:"schema_version"`
	ConfigPath    string                   `json:"config_path"`
	WorkflowRoot  string                   `json:"workflow_root"`
	Items         []Plan                   `json:"items"`
	Findings      []workflowstatus.Finding `json:"findings"`
}

// Receipt is separate from immutable archives and from engine success state.
// Pending survives partial worktree removal or an interrupted record move.
type Receipt struct {
	SchemaVersion int    `json:"schema_version"`
	ConfigPath    string `json:"config_path"`
	WorkflowRoot  string `json:"workflow_root"`
	Workflow      string `json:"workflow"`
	Change        string `json:"change"`
	Identity      string `json:"identity"`
	Status        string `json:"status"`
}

func List(l workspace.Layout, workflow string) (Inventory, error) {
	if workflow != "" && workflow != "onto" && workflow != "to" {
		return Inventory{}, fmt.Errorf("cleanup: workflow must be onto or to")
	}
	s := workflowstatus.ReadConfig(l.ConfigPath)
	out := Inventory{SchemaVersion: SchemaVersion, ConfigPath: l.ConfigPath, WorkflowRoot: l.WorkflowRoot, Items: []Plan{}, Findings: s.Findings}
	for _, c := range s.Changes {
		if workflow != "" && c.Workflow != workflow {
			continue
		}
		out.Items = append(out.Items, inspect(l, s, c, false))
	}
	return out, nil
}

func Preview(l workspace.Layout, workflow, change, identity string) (Plan, error) {
	return preview(l, workflow, change, identity, false)
}

func preview(l workspace.Layout, workflow, change, identity string, ownsHistory bool) (Plan, error) {
	if err := validateSelection(workflow, change); err != nil {
		return Plan{}, err
	}
	s := workflowstatus.ReadConfig(l.ConfigPath)
	var matches []workflowstatus.Change
	for _, c := range s.Changes {
		if c.Workflow == workflow && c.Name == change && (identity == "" || c.Identity == identity) {
			matches = append(matches, c)
		}
	}
	if len(matches) != 1 {
		return Plan{}, fmt.Errorf("cleanup: expected one %s/%s generation, found %d; use cleanup list --json and select --identity", workflow, change, len(matches))
	}
	return inspect(l, s, matches[0], ownsHistory), nil
}

func inspect(l workspace.Layout, s workflowstatus.Snapshot, c workflowstatus.Change, ownsHistory bool) Plan {
	p := Plan{SchemaVersion: SchemaVersion, Workflow: c.Workflow, Change: c.Name, Identity: c.Identity, Lifecycle: c.Status, Record: c.Path, Status: "cleanup-pending", Worktrees: []workspace.WorktreeRemoval{}, Blockers: []string{}}
	block := func(err error) Plan {
		p.Status = "blocked"
		p.Blockers = append(p.Blockers, err.Error())
		return p
	}
	if err := validateSelection(c.Workflow, c.Name); err != nil {
		return block(err)
	}
	for _, f := range s.Findings {
		// RunLayoutMutation holds the history lock and has just written its own
		// intent. Only internal rechecks inside that boundary may ignore it.
		if ownsHistory && f.Workflow == "workspace" && f.Message == "workflow history pending; run homonto workspace recover" {
			continue
		}
		if f.Workflow == "workspace" || (f.Workflow == c.Workflow && (f.Change == "" || f.Change == c.Name)) {
			p.Blockers = append(p.Blockers, f.Message)
		}
	}
	for _, other := range s.Changes {
		if other.Workflow == c.Workflow && other.Identity == c.Identity && other.Path != c.Path {
			p.Blockers = append(p.Blockers, "duplicate generation identity; inspect both records before cleanup")
		}
	}
	dir, err := recordDir(l, c.Workflow, c.Name, c.Path)
	if err != nil {
		return block(err)
	}
	if err := eligible(dir, c.Workflow); err != nil {
		p.Status = "ineligible"
		p.Blockers = append(p.Blockers, err.Error())
		return p
	}
	receipt, err := readReceipt(l, p)
	if err != nil {
		return block(err)
	}
	if !strings.Contains(c.Path, "/archive/") {
		p.Archive, err = archiveDestination(l, c.Workflow, c.Name)
		if err != nil {
			return block(err)
		}
		if err := recordsClean(l, dir); err != nil {
			p.Blockers = append(p.Blockers, err.Error())
		}
	}
	p.Worktrees, err = workspace.InspectWorkflowRemovals(l, c.Workflow, c.Name, dir)
	if err != nil {
		return block(err)
	}
	sortRemovals(p.Worktrees)
	if p.Tmp, err = changeTmp(l, c.Workflow, c.Name); err != nil {
		p.Blockers = append(p.Blockers, err.Error())
	}
	// Scratch has a name, not a generation token. Once that name is reused,
	// no generation can prove exclusive ownership; leave it to tmp clean.
	for _, other := range s.Changes {
		if other.Workflow == c.Workflow && other.Name == c.Name && other.Identity != c.Identity {
			p.Tmp = ""
		}
	}
	for _, w := range p.Worktrees {
		if w.Blocker != "" {
			p.Blockers = append(p.Blockers, w.Path+": "+w.Blocker)
		}
	}
	if len(p.Blockers) != 0 {
		p.Status = "blocked"
	} else if p.Archive == "" && len(p.Worktrees) == 0 && p.Tmp == "" && (receipt == nil || receipt.Status == "cleanup-complete") {
		p.Status = "cleanup-complete"
	}
	return p
}

// Apply is explicitly generation-bound and never accepts a plan file from a
// caller. --yes acknowledges cleanup, not permission to bypass any safety check.
func Apply(ctx context.Context, l workspace.Layout, workflow, change, identity string, yes bool) (Plan, error) {
	if !yes || identity == "" {
		return Plan{}, fmt.Errorf("cleanup apply: --yes and an exact --identity from cleanup list/preview are required")
	}
	p, err := Preview(l, workflow, change, identity)
	if err != nil {
		return p, err
	}
	if p.Status == "blocked" || p.Status == "ineligible" {
		return p, fmt.Errorf("cleanup refused: %s", strings.Join(p.Blockers, "; "))
	}
	if p.Status == "cleanup-complete" {
		return p, nil
	}
	receipt := receiptPath(l, p)
	scope := workspace.MutationScope{Paths: []string{receipt}}
	if p.Archive != "" {
		scope.Trees = []string{p.Record, p.Archive}
	}
	err = workspace.RunLayoutMutation(l, workflow+" cleanup "+change, func() error {
		lockPath := filepath.Join(l.WorkflowRoot, ".workflow", "cleanup.lock")
		if err := fsutil.RequireRealParents(l.WorkflowRoot, filepath.Dir(lockPath)); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
			return err
		}
		lock, err := applylock.AcquirePath(lockPath, filepath.Join(l.ConfigRoot, ".homonto"))
		if err != nil {
			return err
		}
		defer func() { _ = lock.Release() }()
		fresh, err := preview(l, workflow, change, identity, true)
		if err != nil {
			return err
		}
		if fresh.Status == "blocked" || fresh.Status == "ineligible" || fresh.Record != p.Record || fresh.Archive != p.Archive {
			return fmt.Errorf("cleanup: selection or safety checks changed; preview again: %s", strings.Join(fresh.Blockers, "; "))
		}
		if err := writeReceipt(l, p, "cleanup-pending"); err != nil {
			return err
		}
		for _, w := range fresh.Worktrees {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := workspace.RemoveWorktreeRole(l, workflow, change, w.Repo, w.Role, true, w.StateID); err != nil {
				return fmt.Errorf("cleanup pending; rerun cleanup apply for this identity: %w", err)
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := finishCleanup(l, p); err != nil {
			return fmt.Errorf("cleanup pending; inspect and retry this identity: %w", err)
		}
		return nil
	}, scope)
	updated, previewErr := Preview(l, workflow, change, identity)
	if previewErr == nil {
		p = updated
	}
	return p, err
}

func finishCleanup(l workspace.Layout, p Plan) error {
	tree := recordTree(p.Workflow)
	unlock, err := workcli.LockWorkspace(p.Workflow, l.ConfigRoot, filepath.Join(l.WorkflowRoot, tree, "."+p.Workflow+".lock"))
	if err != nil {
		return err
	}
	defer unlock()
	unlockNames, err := workspace.LockLifecycle(l)
	if err != nil {
		return err
	}
	defer unlockNames()
	fresh, err := preview(l, p.Workflow, p.Change, p.Identity, true)
	if err != nil {
		return err
	}
	if fresh.Record != p.Record || fresh.Archive != p.Archive || len(fresh.Worktrees) != 0 || len(fresh.Blockers) != 0 {
		return fmt.Errorf("record or bindings changed; preview again")
	}
	// Recheck and remove while the lifecycle/name lock still excludes a new
	// generation. Never delete from the earlier, unlocked preview.
	if fresh.Tmp != "" {
		if err := removeTmp(l, fresh.Tmp); err != nil {
			return fmt.Errorf("scratch %s not removed: %w", fresh.Tmp, err)
		}
	}
	if p.Archive == "" {
		return writeReceipt(l, p, "cleanup-complete")
	}
	source, err := recordDir(l, p.Workflow, p.Change, p.Record)
	if err != nil {
		return err
	}
	destination := filepath.Join(l.WorkflowRoot, p.Archive)
	if err := fsutil.RequireRealParents(l.WorkflowRoot, filepath.Dir(destination)); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("cleanup: archive destination is no longer free")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if err := fsutil.RenameDurable(source, destination); err != nil {
		return err
	}
	return writeReceipt(l, p, "cleanup-complete")
}

// changeTmp returns the change's existing scratch folder, or "" when [tmp] is
// undeclared or the folder is absent.
func changeTmp(l workspace.Layout, workflow, change string) (string, error) {
	root, err := config.ReadTmp(l.ConfigPath)
	if err != nil || root == "" {
		return "", err
	}
	dir := filepath.Join(root, workflow, change)
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	return dir, nil
}

// removeTmp deletes one scratch entry below the config root. A symlink is
// removed, never followed, and a link in any parent refuses the removal.
func removeTmp(l workspace.Layout, path string) error {
	if err := removeTmpParents(l, filepath.Dir(path)); err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func removeTmpParents(l workspace.Layout, dir string) error {
	return fsutil.RequireRealParents(l.ConfigRoot, dir)
}

func validateSelection(workflow, change string) error {
	if workflow != "onto" && workflow != "to" {
		return fmt.Errorf("cleanup: workflow must be onto or to")
	}
	return (workcli.Framework{NamePrefix: "cleanup", ReservedNames: []string{"archive"}}).ValidChangeName(change)
}

func eligible(dir, workflow string) error {
	if workflow == "onto" {
		st, err := ontostate.LoadChange(dir)
		if err != nil {
			return err
		}
		if st.Abandoned || (st.Archived && st.Phase == "close" && ontostate.ArchiveIntegrationComplete(dir, st)) {
			return nil
		}
		return fmt.Errorf("cleanup: onto change is active or integration is incomplete; resume it or explicitly abandon it first")
	}
	st, err := tostate.Load(filepath.Join(dir, tostate.FileName))
	if err != nil {
		return err
	}
	if !st.Terminal() {
		return fmt.Errorf("cleanup: to change is active; resume it or explicitly abandon it first")
	}
	return st.Validate()
}

func recordTree(workflow string) string {
	if workflow == "to" {
		return "tasks"
	}
	return "changes"
}

func recordDir(l workspace.Layout, workflow, change, rel string) (string, error) {
	tree := recordTree(workflow)
	if rel != tree+"/"+change && !(strings.HasPrefix(rel, tree+"/archive/") && filepath.Base(rel) != "archive" && strings.Count(rel, "/") == 2) {
		return "", fmt.Errorf("cleanup: unexpected record path %q", rel)
	}
	dir := filepath.Join(l.WorkflowRoot, filepath.FromSlash(rel))
	if err := fsutil.RequireRealParents(l.WorkflowRoot, dir); err != nil {
		return "", err
	}
	// Renaming preserves regular content, but never transports symlinks, special
	// files or nested Git checkouts as if they were ordinary workflow artifacts.
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" || (!d.IsDir() && !d.Type().IsRegular()) {
			return fmt.Errorf("cleanup: unsupported artifact at %s; inspect before cleanup", path)
		}
		return nil
	})
	return dir, err
}

func recordsClean(l workspace.Layout, dir string) error {
	root, _, err := workspace.GitIdentity(l.WorkflowRoot)
	if err != nil {
		return fmt.Errorf("cleanup: cannot establish records cleanliness: %w", err)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	out, err := workspace.Git(context.Background(), root, "--literal-pathspecs", "status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching", "--", rel)
	if err != nil {
		return err
	}
	if out != "" {
		return fmt.Errorf("cleanup: workflow records have uncommitted or ignored content; preserve/checkpoint it before archiving: %s", out)
	}
	flags, err := workspace.Git(context.Background(), root, "--literal-pathspecs", "ls-files", "-v", "-z", "--", rel)
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(flags, "\x00") {
		if entry != "" && (entry[0] == 'S' || (entry[0] >= 'a' && entry[0] <= 'z')) {
			return fmt.Errorf("cleanup: workflow record has hidden index flags; inspect before archiving: %s", entry)
		}
	}
	return nil
}

func archiveDestination(l workspace.Layout, workflow, change string) (string, error) {
	base := recordTree(workflow) + "/archive/" + time.Now().Format("2006-01-02") + "-" + change
	for rel, n := base, 2; ; rel, n = fmt.Sprintf("%s-%d", base, n), n+1 {
		if err := fsutil.RequireRealParents(l.WorkflowRoot, filepath.Dir(filepath.Join(l.WorkflowRoot, rel))); err != nil {
			return "", err
		}
		if _, err := os.Lstat(filepath.Join(l.WorkflowRoot, rel)); os.IsNotExist(err) {
			return rel, nil
		} else if err != nil {
			return "", err
		}
	}
}

func receiptPath(_ workspace.Layout, p Plan) string {
	sum := sha256.Sum256([]byte(p.Workflow + "\x00" + p.Change + "\x00" + p.Identity))
	return ".workflow/cleanup/" + hex.EncodeToString(sum[:]) + ".json"
}

func readReceipt(l workspace.Layout, p Plan) (*Receipt, error) {
	path := filepath.Join(l.WorkflowRoot, receiptPath(l, p))
	if err := fsutil.RequireRealParents(l.WorkflowRoot, filepath.Dir(path)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return nil, fmt.Errorf("cleanup: invalid receipt file %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Receipt
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF || r.SchemaVersion != SchemaVersion || r.ConfigPath != l.ConfigPath || r.WorkflowRoot != l.WorkflowRoot || r.Workflow != p.Workflow || r.Change != p.Change || r.Identity != p.Identity || (r.Status != "cleanup-pending" && r.Status != "cleanup-complete") {
		return nil, fmt.Errorf("cleanup: receipt version, identity or status mismatch; inspect before recovery")
	}
	return &r, nil
}

func writeReceipt(l workspace.Layout, p Plan, status string) error {
	r := Receipt{SchemaVersion: SchemaVersion, ConfigPath: l.ConfigPath, WorkflowRoot: l.WorkflowRoot, Workflow: p.Workflow, Change: p.Change, Identity: p.Identity, Status: status}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteControlPlaneWithin(l.WorkflowRoot, filepath.Join(l.WorkflowRoot, receiptPath(l, p)), append(data, '\n'), 0o600)
}

// Stable ordering makes JSON inventories deterministic even when registry order
// came from an interrupted older writer.
func sortRemovals(removals []workspace.WorktreeRemoval) {
	sort.Slice(removals, func(i, j int) bool {
		return removals[i].Repo+"/"+removals[i].Role < removals[j].Repo+"/"+removals[j].Role
	})
}
