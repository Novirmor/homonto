// Command releaseevidence keeps a release's evidence manifest (M5.6): every
// check a tag needs, keyed to the exact release commit. A key that was not
// run stays missing, and a skipped live check is visibly incomplete; nothing
// is inferred from green unit tests.
//
//	go run ./scripts/releaseevidence init   --version v1.0.0 --out evidence.json
//	go run ./scripts/releaseevidence record evidence.json <id> pass|fail|skip|n/a [--note text] [--log path]
//	go run ./scripts/releaseevidence check  evidence.json
//
// The required keys are the release gate's own plus, for every supported
// host × harness cell (internal/starter), the evidence that cell declares.
// The manifest lives outside the tagged commit, which cannot contain
// evidence about itself: attach it to the GitHub release.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/Novirmor/homonto/internal/starter"
)

// Statuses.
const (
	StatusMissing = "missing"
	StatusPass    = "pass"
	StatusFail    = "fail"
	StatusSkip    = "skip"
	StatusNA      = "n/a"
)

// Requirement is one key the manifest must carry.
type Requirement struct {
	ID    string
	Check string
	// NotApplicable lets the key be satisfied by "n/a" with a note, for a
	// check that cannot apply to this release (no earlier 1.x release to
	// upgrade from). Cell evidence never allows it.
	NotApplicable bool
}

// gateRequirements are the release gate's keys outside the cell matrix
// (docs/release-checklist.md).
var gateRequirements = []Requirement{
	{ID: "gate/full", Check: "./scripts/gate.sh ends with ALL GATE CHECKS PASSED"},
	{ID: "compat/fixtures", Check: "task compat-fixtures against the previous release tag"},
	{ID: "compat/upgrade", Check: "upgrade in place from the previous 1.x release (release checklist, Compatibility)", NotApplicable: true},
	{ID: "live/opencode-host", Check: "release checklist 2.1: OpenCode 2 host check"},
	{ID: "live/draft-approval", Check: "release checklist 2.2: draft denial and native approval"},
	{ID: "extension/published", Check: "the published homonto-reference-set CI passes against the release commit"},
	{ID: "docs/limitations", Check: "release notes list the accepted Claude Code limitations (ADR 0105) and the verified cells"},
}

// Requirements returns every key a release manifest needs, in order.
func Requirements() []Requirement {
	out := slices.Clone(gateRequirements)
	seen := map[string]bool{}
	for _, r := range out {
		seen[r.ID] = true
	}
	for _, c := range starter.Cells() {
		for _, e := range starter.CellEvidence(c[0], c[1]) {
			if !seen[e.ID] {
				seen[e.ID] = true
				out = append(out, Requirement{ID: e.ID, Check: e.Check})
			}
		}
	}
	return out
}

// Entry is one key's recorded outcome.
type Entry struct {
	ID     string `json:"id"`
	Check  string `json:"check"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
	Log    string `json:"log,omitempty"`
	At     string `json:"at,omitempty"`
}

// Manifest is a release's evidence.
type Manifest struct {
	Version string  `json:"version"`
	Commit  string  `json:"commit"`
	Created string  `json:"created"`
	Entries []Entry `json:"entries"`
}

func (m *Manifest) entry(id string) *Entry {
	for i := range m.Entries {
		if m.Entries[i].ID == id {
			return &m.Entries[i]
		}
	}
	return nil
}

// Unsatisfied lists the keys that do not yet allow the tag.
func (m Manifest) Unsatisfied() []Entry {
	var out []Entry
	for _, r := range Requirements() {
		e := m.entry(r.ID)
		switch {
		case e == nil:
			out = append(out, Entry{ID: r.ID, Check: r.Check, Status: StatusMissing})
		case e.Status == StatusPass:
		case e.Status == StatusNA && r.NotApplicable && e.Note != "":
		default:
			out = append(out, *e)
		}
	}
	return out
}

// Claimable lists the host × harness cells whose every key passed.
func (m Manifest) Claimable() [][2]string {
	var out [][2]string
	for _, c := range starter.Cells() {
		ok := true
		for _, e := range starter.CellEvidence(c[0], c[1]) {
			if got := m.entry(e.ID); got == nil || got.Status != StatusPass {
				ok = false
			}
		}
		if ok {
			out = append(out, c)
		}
	}
	return out
}

// git is the repository probe; tests replace it.
var git = func(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return strings.TrimSpace(string(out)), err
}

func head() (string, error) {
	commit, err := git("rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("releaseevidence: git rev-parse HEAD: %w", err)
	}
	dirty, err := git("status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return "", fmt.Errorf("releaseevidence: git status: %w", err)
	}
	if dirty != "" {
		return "", errors.New("releaseevidence: the working tree has uncommitted changes; evidence is keyed to a commit, so commit or stash them first")
	}
	return commit, nil
}

func load(path string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("releaseevidence: %s: %w", path, err)
	}
	return m, nil
}

func save(path string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: releaseevidence init|record|check …")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	switch args[0] {
	case "init":
		fs := flag.NewFlagSet("init", flag.ContinueOnError)
		version := fs.String("version", "", "the release tag, for example v1.0.0")
		out := fs.String("out", "", "manifest path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *version == "" || *out == "" {
			return errors.New("releaseevidence init: --version and --out are required")
		}
		if _, err := os.Stat(*out); err == nil {
			return fmt.Errorf("releaseevidence init: %s exists; a manifest is never reset", *out)
		}
		commit, err := head()
		if err != nil {
			return err
		}
		m := Manifest{Version: *version, Commit: commit, Created: now}
		for _, r := range Requirements() {
			m.Entries = append(m.Entries, Entry{ID: r.ID, Check: r.Check, Status: StatusMissing})
		}
		if err := save(*out, m); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "created %s for %s at %s (%d keys)\n", *out, *version, commit, len(m.Entries))
		return nil
	case "record":
		if len(args) < 4 {
			return errors.New("usage: releaseevidence record <manifest> <id> pass|fail|skip|n/a [--note text] [--log path]")
		}
		path, id, status := args[1], args[2], args[3]
		fs := flag.NewFlagSet("record", flag.ContinueOnError)
		note := fs.String("note", "", "what was run and what it showed")
		log := fs.String("log", "", "where the output is kept")
		if err := fs.Parse(args[4:]); err != nil {
			return err
		}
		if !slices.Contains([]string{StatusPass, StatusFail, StatusSkip, StatusNA}, status) {
			return fmt.Errorf("releaseevidence record: status %q is not pass, fail, skip or n/a", status)
		}
		m, err := load(path)
		if err != nil {
			return err
		}
		commit, err := head()
		if err != nil {
			return err
		}
		if commit != m.Commit {
			return fmt.Errorf("releaseevidence record: HEAD is %s but the manifest is for %s; evidence from another commit does not count", commit, m.Commit)
		}
		e := m.entry(id)
		if e == nil {
			return fmt.Errorf("releaseevidence record: %q is not a key of this manifest", id)
		}
		if status == StatusNA {
			req := slices.IndexFunc(Requirements(), func(r Requirement) bool { return r.ID == id })
			if req < 0 || !Requirements()[req].NotApplicable {
				return fmt.Errorf("releaseevidence record: %s cannot be n/a; run it, or record skip", id)
			}
			if *note == "" {
				return fmt.Errorf("releaseevidence record: n/a needs a --note saying why %s does not apply", id)
			}
		}
		e.Status, e.Note, e.Log, e.At = status, *note, *log, now
		if err := save(path, m); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s: %s\n", id, status)
		return nil
	case "check":
		if len(args) != 2 {
			return errors.New("usage: releaseevidence check <manifest>")
		}
		m, err := load(args[1])
		if err != nil {
			return err
		}
		commit, err := head()
		if err != nil {
			return err
		}
		if commit != m.Commit {
			return fmt.Errorf("releaseevidence check: HEAD is %s but the manifest is for %s; evidence from another commit does not allow this tag", commit, m.Commit)
		}
		fmt.Fprintf(stdout, "%s at %s\n", m.Version, m.Commit)
		for _, c := range m.Claimable() {
			host := c[0]
			if host == "" {
				host = "none"
			}
			fmt.Fprintf(stdout, "claimable: host %s, harness %s\n", host, c[1])
		}
		missing := m.Unsatisfied()
		for _, e := range missing {
			fmt.Fprintf(stdout, "%-8s %s — %s\n", e.Status, e.ID, e.Check)
		}
		if len(missing) > 0 {
			return fmt.Errorf("releaseevidence: %d of %d keys are not satisfied; %s cannot be tagged", len(missing), len(Requirements()), m.Version)
		}
		fmt.Fprintf(stdout, "every key passed: %s may be tagged at %s\n", m.Version, m.Commit)
		return nil
	}
	return fmt.Errorf("releaseevidence: unknown command %q", args[0])
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
