package workflowcleanup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/workflowstatus"
	"github.com/noviopenworks/homonto/internal/workspace"
)

// TmpReport lists scratch that belongs to no live change (ADR 0086). Without
// --yes it is a preview; Removed then names what apply would delete.
type TmpReport struct {
	SchemaVersion int      `json:"schema_version"`
	Tmp           string   `json:"tmp"`
	Applied       bool     `json:"applied"`
	Kept          []string `json:"kept"`
	Removed       []string `json:"removed"`
}

// TmpClean empties the declared [tmp] directory except the scratch folders of
// live onto and to changes (<tmp>/<workflow>/<change>). A change is live while
// it is active or interrupted, or when its record could not be read.
func TmpClean(l workspace.Layout, yes bool) (TmpReport, error) {
	r := TmpReport{SchemaVersion: SchemaVersion, Applied: yes, Kept: []string{}, Removed: []string{}}
	root, err := config.ReadTmp(l.ConfigPath)
	if err != nil {
		return r, err
	}
	if root == "" {
		return r, fmt.Errorf("tmp clean: [tmp] is not declared in %s", l.ConfigPath)
	}
	r.Tmp = root
	if err := workspaceRealDir(l, root); err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return r, err
	}
	s := workflowstatus.ReadConfig(l.ConfigPath)
	live := map[string]bool{}
	for _, c := range s.Changes {
		if c.Status == "active" || c.Status == "interrupted" {
			live[c.Workflow+"/"+c.Name] = true
		}
	}
	for _, f := range s.Findings {
		if f.Change != "" {
			live[f.Workflow+"/"+f.Change] = true
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return r, err
	}
	for _, e := range entries {
		if (e.Name() == "onto" || e.Name() == "to") && e.Type().IsDir() {
			children, err := os.ReadDir(filepath.Join(root, e.Name()))
			if err != nil {
				return r, err
			}
			for _, child := range children {
				rel := e.Name() + "/" + child.Name()
				if live[rel] {
					r.Kept = append(r.Kept, rel)
				} else {
					r.Removed = append(r.Removed, rel)
				}
			}
			continue
		}
		r.Removed = append(r.Removed, e.Name())
	}
	sort.Strings(r.Kept)
	sort.Strings(r.Removed)
	if !yes {
		return r, nil
	}
	for _, rel := range r.Removed {
		if err := removeTmp(l, filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return r, fmt.Errorf("tmp clean: %s: %w", rel, err)
		}
	}
	return r, nil
}

// workspaceRealDir requires dir to be a real directory below the config root.
func workspaceRealDir(l workspace.Layout, dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("tmp clean: %s is not a directory", dir)
	}
	return removeTmpParents(l, dir)
}
