// Package integrationrecord persists the post-archive Git integration step.
// The record carries one entry per repository in the change's scope (the
// config repository plus every declared alias), because a cross-repo change is
// integrated only when each repository's branch has actually landed.
package integrationrecord

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/noviopenworks/homonto/internal/fsutil"
)

const SchemaVersion = 2

const (
	StatusPending  = "pending"
	StatusComplete = "complete"
)

// Entry is the integration provenance of one repository. Alias is empty for
// the implicit config repository and otherwise a declared [repos] name.
type Entry struct {
	Alias        string `json:"alias,omitempty"`
	BaseBranch   string `json:"baseBranch"`
	BaseCommit   string `json:"baseCommit"`
	SourceBranch string `json:"sourceBranch"`
	SourceCommit string `json:"sourceCommit"`
	Receipt      string `json:"receipt,omitempty"`
}

// Record is the versioned sidecar saved at close and completed per repository.
// Status is aggregate: complete only once every entry carries a receipt.
type Record struct {
	SchemaVersion int     `json:"schemaVersion"`
	Change        string  `json:"change"`
	Mode          string  `json:"mode"`
	BaseBranch    string  `json:"baseBranch"`
	Status        string  `json:"status"`
	Repositories  []Entry `json:"repositories"`
}

func Path(changeDir string) string {
	return filepath.Join(changeDir, ".onto", "integration.json")
}

// NewPending builds a pending record from the per-repository entries captured
// at close time. The entries must already name real commits and branches; this
// package only checks shape.
func NewPending(change, mode, baseBranch string, entries []Entry) Record {
	return Record{
		SchemaVersion: SchemaVersion, Change: change, Mode: mode,
		BaseBranch: baseBranch, Status: StatusPending, Repositories: entries,
	}
}

func (r Record) Validate(change string) error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("integration record schemaVersion %d is unsupported", r.SchemaVersion)
	}
	if r.Change != change {
		return fmt.Errorf("integration record change %q does not match %q", r.Change, change)
	}
	if r.Mode != "merge" && r.Mode != "pr" {
		return fmt.Errorf("integration record mode %q is not merge|pr", r.Mode)
	}
	if strings.TrimSpace(r.BaseBranch) == "" {
		return fmt.Errorf("integration record requires baseBranch")
	}
	if len(r.Repositories) == 0 {
		return fmt.Errorf("integration record requires at least one repository entry")
	}
	seen := map[string]bool{}
	for _, e := range r.Repositories {
		if e.Alias != "" && seen[e.Alias] {
			return fmt.Errorf("integration record has duplicate repository entry %q", e.Alias)
		}
		seen[e.Alias] = true
		if e.BaseBranch != r.BaseBranch {
			return fmt.Errorf("integration record entry %q base branch %q does not match record base branch %q", e.Alias, e.BaseBranch, r.BaseBranch)
		}
		if strings.TrimSpace(e.BaseBranch) == "" || strings.TrimSpace(e.SourceBranch) == "" || strings.TrimSpace(e.BaseCommit) == "" || strings.TrimSpace(e.SourceCommit) == "" {
			return fmt.Errorf("integration record entry %q requires baseBranch, baseCommit, sourceBranch, and sourceCommit", e.Alias)
		}
		if e.Receipt != "" {
			if err := validateReceipt(r.Mode, e.Receipt); err != nil {
				return fmt.Errorf("integration record entry %q: %w", e.Alias, err)
			}
		}
	}
	if r.Status != StatusPending && r.Status != StatusComplete {
		return fmt.Errorf("integration record status %q is not pending|complete", r.Status)
	}
	if r.Status == StatusComplete {
		for _, e := range r.Repositories {
			if e.Receipt == "" {
				return fmt.Errorf("complete integration record is missing repository %q receipt", e.Alias)
			}
		}
	}
	return nil
}

// CompleteFor records receipt against the entry named alias ("" selects the
// config repository). Replaying the same receipt is idempotent; replacing a
// recorded receipt is refused. The aggregate status flips to complete only
// when every repository carries a receipt.
func (r Record) CompleteFor(alias, receipt string) (Record, error) {
	for i := range r.Repositories {
		if r.Repositories[i].Alias != alias {
			continue
		}
		if err := validateReceipt(r.Mode, receipt); err != nil {
			return Record{}, err
		}
		if r.Repositories[i].Receipt == receipt {
			return r, nil
		}
		if r.Repositories[i].Receipt != "" {
			return Record{}, fmt.Errorf("repository %q is already complete with a different receipt", displayName(alias))
		}
		r.Repositories[i].Receipt = receipt
		if r.allRecorded() {
			r.Status = StatusComplete
		}
		return r, nil
	}
	return Record{}, fmt.Errorf("integration record has no repository entry %q", displayName(alias))
}

func displayName(alias string) string {
	if alias == "" {
		return "config"
	}
	return alias
}

func (r Record) allRecorded() bool {
	for _, e := range r.Repositories {
		if e.Receipt == "" {
			return false
		}
	}
	return true
}

func Load(changeDir, change string) (Record, bool, error) {
	path := Path(changeDir)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("inspecting integration record: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Record{}, false, fmt.Errorf("integration record must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		return Record{}, false, fmt.Errorf("opening integration record: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var record Record
	if err := dec.Decode(&record); err != nil {
		return Record{}, false, fmt.Errorf("decoding integration record: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("unexpected data after JSON document")
		}
		return Record{}, false, fmt.Errorf("decoding integration record: %w", err)
	}
	if err := record.Validate(change); err != nil {
		return Record{}, false, err
	}
	return record, true, nil
}

func Save(changeDir string, record Record) error {
	record.SchemaVersion = SchemaVersion
	if err := record.Validate(record.Change); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding integration record: %w", err)
	}
	data = append(data, '\n')
	if err := fsutil.WriteControlPlaneWithin(changeDir, Path(changeDir), data, 0o644); err != nil {
		return fmt.Errorf("writing integration record: %w", err)
	}
	return nil
}

var mergeReceipt = regexp.MustCompile(`^merge:[0-9a-fA-F]{7,64}$`)

func validateReceipt(mode, receipt string) error {
	switch mode {
	case "merge":
		if !mergeReceipt.MatchString(receipt) {
			return fmt.Errorf("merge integration receipt must be merge:<commit-sha>")
		}
	case "pr":
		value, ok := strings.CutPrefix(receipt, "pr:")
		if !ok {
			return fmt.Errorf("PR integration receipt must be pr:<https-url>")
		}
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("PR integration receipt must be pr:<https-url>")
		}
	default:
		return fmt.Errorf("integration mode %q is unsupported", mode)
	}
	return nil
}
