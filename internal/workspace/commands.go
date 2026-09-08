package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var archiveNow = time.Now
var errArchiveChanged = errors.New("prepared archive changed")

// ArchivePlan is persisted with the write intent. Destinations are prepared
// once, not independently recomputed by the writer after gates or midnight.
type ArchivePlan struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Date        string `json:"date"`
	StateHash   string `json:"state_hash"`
}

type archiveContextKey struct{}
type preparedArchive struct {
	ArchivePlan
	layout     Layout
	sourceInfo os.FileInfo
	parent     string
	parentInfo os.FileInfo
}

// ArchiveTarget returns the prepared destination and date, checking the source
// directory's saved stat identity and state fingerprint before the first write.
// Legacy/unmanaged handlers retain their own selection when planned is false.
func ArchiveTarget(ctx context.Context, source string) (destination, date string, planned bool, err error) {
	if ctx == nil {
		return "", "", false, nil
	}
	p, ok := ctx.Value(archiveContextKey{}).(*preparedArchive)
	if !ok {
		return "", "", false, nil
	}
	destination = filepath.Join(p.layout.WorkflowRoot, p.Destination)
	if err := p.check(source, destination); err != nil {
		return "", "", true, fmt.Errorf("%w: %w", errArchiveChanged, err)
	}
	workflow := "onto"
	if strings.HasPrefix(p.Source, "tasks/") {
		workflow = "to"
	}
	state := p.Source + "/" + workflow + "-state.yaml"
	hash, err := historyFileHash(p.layout, state)
	if err != nil {
		return "", "", true, fmt.Errorf("%w: %w", errArchiveChanged, err)
	}
	if hash != p.StateHash {
		return "", "", true, fmt.Errorf("%w: source state changed after preparation", errArchiveChanged)
	}
	return destination, p.Date, true, nil
}

// CheckArchiveTarget rechecks directory identity and target absence immediately
// before the move, after intentional state/sidecar writes may have occurred.
func CheckArchiveTarget(ctx context.Context, source, destination string) error {
	if ctx == nil {
		return nil
	}
	if p, ok := ctx.Value(archiveContextKey{}).(*preparedArchive); ok {
		if err := p.check(source, destination); err != nil {
			return fmt.Errorf("%w: %w", errArchiveChanged, err)
		}
	}
	return nil
}

func (p *preparedArchive) check(source, destination string) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	if source != filepath.Join(p.layout.WorkflowRoot, p.Source) || destination != filepath.Join(p.layout.WorkflowRoot, p.Destination) {
		return fmt.Errorf("workspace archive: destination/source differs from prepared intent")
	}
	if err := fsutil.RequireRealParents(p.layout.WorkflowRoot, filepath.Dir(destination)); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() || !os.SameFile(p.sourceInfo, info) {
		return fmt.Errorf("workspace archive: source directory replaced after preparation")
	}
	info, err = os.Lstat(p.parent)
	if err != nil {
		return err
	}
	if !info.IsDir() || !os.SameFile(p.parentInfo, info) {
		return fmt.Errorf("workspace archive: destination parent replaced after preparation")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		if err != nil {
			return err
		}
		return fmt.Errorf("workspace archive: prepared destination already exists: %s", destination)
	}
	return nil
}

// AttachHistory wraps logical workflow mutations after all commands have been
// registered. Read-only handlers never enter the history boundary. Internal
// transitions remain unwrapped so multi-hop operations take only one lock.
func AttachHistory(root *cobra.Command, workflow string) {
	var attach func(*cobra.Command, string)
	attach = func(cmd *cobra.Command, path string) {
		for _, child := range cmd.Commands() {
			attach(child, strings.TrimSpace(path+" "+child.Name()))
		}
		if cmd.RunE == nil {
			return
		}
		label := workflow + " " + path
		var writeFlag string
		switch label {
		case "onto init", "onto new", "onto advance", "onto bypass", "onto close",
			"onto complete-integration", "onto abandon", "onto demote", "onto merge-deltas",
			"onto evidence record", "to init", "to new", "to phase", "to bypass",
			"to done", "to abandon", "to promote":
		case "onto scale":
			writeFlag = "set"
		case "onto handoff", "to handoff":
			writeFlag = "write"
		default:
			if workflow != "onto" || !strings.HasPrefix(path, "set ") {
				return
			}
		}
		run := cmd.RunE
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			if writeFlag != "" {
				write, err := cmd.Flags().GetBool(writeFlag)
				if err != nil {
					return err
				}
				// Onto's interactive JSON view takes precedence over --write;
				// to's JSON flag only changes output, not persistence.
				if label == "onto handoff" {
					asJSON, err := cmd.Flags().GetBool("json")
					if err != nil {
						return err
					}
					write = write && !asJSON
				}
				if !write {
					return run(cmd, args)
				}
			}
			dir, err := cmd.Flags().GetString("dir")
			if err != nil {
				return err
			}
			configRoot, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			// Legacy handlers own their scope validation and diagnostics. The
			// layout can identify existing-Git mode before repo validation fails.
			if layout, _ := LoadScopeRoot(configRoot, nil); layout.SchemaVersion < 2 && layout.GitMode == "existing" {
				return run(cmd, args)
			}
			var opErr error
			called := false
			previousContext := cmd.Context()
			defer cmd.SetContext(previousContext)
			err = runMutation(configRoot, label, func(l Layout) (MutationScope, error) {
				return commandMutationScope(l, cmd, workflow, path, args)
			}, func() error {
				called = true
				opErr = run(cmd, args)
				return opErr
			})
			if err != nil && called && opErr == nil {
				return fmt.Errorf("%s: operation completed, but workflow history failed; filesystem changes are not rolled back: %w", label, err)
			}
			return err
		}
	}
	attach(root, "")
}

// Resolve under the outer history lock, before the handler takes its own locks.
// Ordinary state changes never own neighboring Markdown files. A rename or
// conversion owns just its source and destination, not either namespace.
func commandMutationScope(l Layout, cmd *cobra.Command, workflow, operation string, args []string) (MutationScope, error) {
	s := MutationScope{}
	if operation == "init" {
		return s, nil
	} // directories and config markers only
	if len(args) == 0 {
		return s, fmt.Errorf("workspace history: missing change name")
	}
	name := args[0]
	validName := func(n string) bool {
		return safeHistoryPath(n) && !strings.Contains(n, "/") && n != "archive" && !strings.HasPrefix(n, ".")
	}
	if !validName(name) {
		return s, fmt.Errorf("workspace history: invalid change name %q", name)
	}
	tree := "changes"
	if workflow == "to" {
		tree = "tasks"
	}
	active := tree + "/" + name
	s.Paths = []string{active + "/" + workflow + "-state.yaml"}
	flag := func(name string) string { v, _ := cmd.Flags().GetString(name); return strings.TrimSpace(v) }
	switch operation {
	case "new":
		s = MutationScope{Trees: []string{active}}
	case "promote", "demote":
		target := flag("as")
		if target == "" {
			target = name
		}
		if !validName(target) {
			return s, fmt.Errorf("workspace history: invalid target name %q", target)
		}
		other := "tasks"
		if workflow == "to" {
			other = "changes"
		}
		s = MutationScope{Trees: []string{active, other + "/" + target}}
	case "handoff":
		s = MutationScope{Trees: []string{active + "/." + workflow + "/handoff"}}
	case "evidence record":
		s.Paths = []string{active + "/.onto/evidence.json"}
	case "merge-deltas":
		s.Paths = append(s.Paths, active+"/.onto/merge-receipt.json")
		entries, err := os.ReadDir(filepath.Join(l.WorkflowRoot, active, "specs"))
		if err != nil && !os.IsNotExist(err) {
			return s, err
		}
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".md") && !strings.EqualFold(e.Name(), "README.md") {
				s.Paths = append(s.Paths, "specs/"+strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))+".md")
			}
		}
	case "bypass":
		s.Paths = append(s.Paths, active+"/."+workflow+"/bypass.json")
	}
	archive := operation == "close" || operation == "done" || (workflow == "to" && operation == "abandon") || (operation == "bypass" && (flag("to") == "archive" || (workflow == "to" && flag("to") == "done")))
	if archive || operation == "complete-integration" {
		// Only fields that select a destination are decoded here; workflow
		// validation belongs to the handler (importing it would create a cycle).
		type identity struct {
			Change   string `yaml:"change"`
			Phase    string `yaml:"phase"`
			Finished string `yaml:"finished"`
		}
		date := archiveNow().Format("2006-01-02")
		if workflow == "to" {
			var st identity
			data, err := os.ReadFile(filepath.Join(l.WorkflowRoot, active, "to-state.yaml"))
			reuse := func(phase string) bool {
				return (phase == "done" && (operation == "done" || operation == "bypass")) || (phase == "abandoned" && operation == "abandon")
			}
			if err == nil && yaml.Unmarshal(data, &st) == nil && reuse(st.Phase) && st.Finished != "" {
				if _, err := time.Parse("2006-01-02", st.Finished); err != nil {
					return s, err
				}
				date = st.Finished
			}
		}
		if archive {
			base := tree + "/archive/" + date + "-" + name
			dest := base
			for n := 2; ; n++ {
				_, err := os.Lstat(filepath.Join(l.WorkflowRoot, dest))
				if os.IsNotExist(err) {
					break
				}
				if err != nil {
					return s, err
				}
				if workflow == "onto" && operation == "bypass" {
					break
				}
				dest = fmt.Sprintf("%s-%d", base, n)
			}
			s = MutationScope{Trees: []string{active, dest}}
			if info, err := os.Lstat(filepath.Join(l.WorkflowRoot, active)); err == nil {
				if !info.IsDir() {
					return s, fmt.Errorf("workspace archive: source must be a real directory")
				}
				hash, err := historyFileHash(l, active+"/"+workflow+"-state.yaml")
				if err != nil {
					return s, err
				}
				parent := filepath.Dir(filepath.Join(l.WorkflowRoot, dest))
				if err := fsutil.RequireRealParents(l.WorkflowRoot, parent); err != nil {
					return s, err
				}
				parentInfo, err := os.Lstat(parent)
				if os.IsNotExist(err) {
					parent = filepath.Dir(parent)
					parentInfo, err = os.Lstat(parent)
				}
				if err != nil {
					return s, err
				}
				p := &preparedArchive{ArchivePlan: ArchivePlan{Source: active, Destination: dest, Date: date, StateHash: hash}, layout: l, sourceInfo: info, parent: parent, parentInfo: parentInfo}
				if err := p.check(filepath.Join(l.WorkflowRoot, active), filepath.Join(l.WorkflowRoot, dest)); err != nil {
					return s, err
				}
				s.Archive = &p.ArchivePlan
				ctx := cmd.Context()
				if ctx == nil {
					ctx = context.Background()
				}
				cmd.SetContext(context.WithValue(ctx, archiveContextKey{}, p))
			} else if !os.IsNotExist(err) {
				return s, err
			}
		}
		_, activeErr := os.Lstat(filepath.Join(l.WorkflowRoot, active))
		if workflow == "onto" && ((operation == "close" && os.IsNotExist(activeErr)) || operation == "complete-integration") {
			entries, err := os.ReadDir(filepath.Join(l.WorkflowRoot, tree, "archive"))
			if err != nil && !os.IsNotExist(err) {
				return s, err
			}
			var matches []string
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				var st identity
				data, err := os.ReadFile(filepath.Join(l.WorkflowRoot, tree, "archive", e.Name(), "onto-state.yaml"))
				if (len(e.Name()) > 11 && e.Name()[11:] == name) || (err == nil && yaml.Unmarshal(data, &st) == nil && st.Change == name) {
					matches = append(matches, e.Name())
				}
			}
			sort.Slice(matches, func(i, j int) bool {
				generation := func(v string) (string, int) {
					if len(v) <= 11 {
						return v, 0
					}
					suffix := strings.TrimPrefix(v[11:], name)
					n, _ := strconv.Atoi(strings.TrimPrefix(suffix, "-"))
					return v[:10], n
				}
				di, ni := generation(matches[i])
				dj, nj := generation(matches[j])
				if di != dj {
					return di > dj
				}
				return ni > nj
			})
			if operation == "complete-integration" {
				s = MutationScope{}
			}
			if len(matches) > 0 {
				p := tree + "/archive/" + matches[0]
				s.Paths = append(s.Paths, p+"/onto-state.yaml", p+"/.onto/integration.json")
			}
		}
	}
	return s, s.validate()
}
