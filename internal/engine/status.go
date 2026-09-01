package engine

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/agentfm"
	"github.com/noviopenworks/homonto/internal/catalog"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/remote"
	"github.com/noviopenworks/homonto/internal/resourcepath"
)

// Status reports two independent facts about the managed surface:
//
//   - drift: state-recorded keys whose CURRENT on-disk value diverges from the
//     value last written by apply (Entry.Applied), or that are missing from disk
//     entirely. Drift comes ONLY from each adapter's ObserveHashes vs Applied —
//     never from the desired-vs-disk Plan comparison — so a pure homonto.toml
//     edit is never mistaken for disk drift.
//   - pending: visible config changes (create/update/delete) that Plan derived
//     from the current desired config and are still awaiting apply, EXCLUDING
//     any key already accounted for as drift.
//
// Plan also populates e.Warnings; a per-adapter ObserveHashes failure is
// appended there and that tool's keys are skipped rather than failing the run.
func (e *Engine) Status() (drift []string, pending int, err error) {
	sets, err := e.Plan()
	if err != nil {
		return nil, 0, err
	}

	// drifted tracks tool -> key -> true for every key reported as drift, so the
	// pending count can exclude them (a drifted key's Plan change is a reset, not
	// pending config work).
	drifted := map[string]map[string]bool{}
	mark := func(tool, key string) {
		if drifted[tool] == nil {
			drifted[tool] = map[string]bool{}
		}
		drifted[tool][key] = true
	}

	for _, a := range e.Adapters {
		observed, oerr := a.ObserveHashes(e.State)
		if oerr != nil {
			e.Warnings = append(e.Warnings, fmt.Sprintf("%s drift skipped: %v", a.Name(), oerr))
			continue
		}
		for _, key := range e.State.Keys(a.Name()) {
			h, ok := observed[key]
			if !ok {
				drift = append(drift, fmt.Sprintf("%s %s missing (deleted out of band)", a.Name(), key))
				mark(a.Name(), key)
				continue
			}
			entry, _ := e.State.Get(a.Name(), key)
			if h != entry.Applied {
				drift = append(drift, fmt.Sprintf("%s %s drifted (will reset on apply)", a.Name(), key))
				mark(a.Name(), key)
			}
		}
	}

	for _, cs := range sets {
		for _, c := range cs.Changes {
			switch c.Action {
			case "create", "update", "delete":
				if drifted[cs.Tool][c.Key] {
					continue
				}
				pending++
			}
		}
	}

	sort.Strings(drift)
	return drift, pending, nil
}

// Doctor runs environment health checks.
func (e *Engine) Doctor() []string {
	var out []string
	if cl, cerr := catalog.New(); cerr == nil {
		if f, pending := catalogUpgradeFinding(e.State.CatalogVersionRecorded(), cl.Version()); pending {
			out = append(out, f)
		}
	}
	if _, err := exec.LookPath("pass"); err != nil {
		out = append(out, "warn: `pass` not found on PATH (pass: references will fail)")
	} else {
		out = append(out, "ok: pass found")
	}
	for _, loc := range []struct{ label, path string }{
		{".config/opencode (OpenCode)", filepath.Join(e.Home, ".config", "opencode")},
	} {
		if _, err := os.Stat(loc.path); err != nil {
			out = append(out, fmt.Sprintf("warn: %s config location %s not found", loc.label, loc.path))
		} else {
			out = append(out, fmt.Sprintf("ok: %s config location present", loc.label))
		}
	}
	// Declared [repos] are load-validated; doctor re-reports the current
	// filesystem facts (a repo deleted or de-git'd after load is a finding,
	// not a silent pass) — ADR 0024 stage 1.
	for _, name := range sortedRepos(e.Cfg.RepoDirs()) {
		dir := e.Cfg.RepoDirs()[name]
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			out = append(out, fmt.Sprintf("warn: declared repo %s: %s is missing (was present at load)", name, dir))
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			out = append(out, fmt.Sprintf("warn: declared repo %s: %s is no longer a git worktree", name, dir))
			continue
		}
		out = append(out, fmt.Sprintf("ok: declared repo %s at %s", name, dir))
	}
	opencodeSkills, oerr := e.Cfg.ExpandedSkillEntriesForTool("opencode")
	if oerr != nil {
		out = append(out, fmt.Sprintf("warn: cannot expand opencode skills: %v", oerr))
	} else {
		out = append(out, e.doctorSkills("opencode", opencodeSkills)...)
	}
	opencodeCommands, ocerr := e.Cfg.ExpandedCommandEntriesForTool("opencode")
	if ocerr != nil {
		out = append(out, fmt.Sprintf("warn: cannot expand opencode commands: %v", ocerr))
	} else {
		out = append(out, e.doctorCommands("opencode", opencodeCommands)...)
	}
	opencodeSubagents, osaerr := e.Cfg.ExpandedSubagentEntriesForTool("opencode")
	if osaerr != nil {
		out = append(out, fmt.Sprintf("warn: cannot expand opencode subagents: %v", osaerr))
	} else {
		out = append(out, e.doctorSubagents("opencode", opencodeSubagents)...)
	}
	out = append(out, e.doctorTooling()...)
	out = append(out, e.doctorRemoteDigests()...)
	return out
}

// doctorRemoteDigests verifies that each locked remote subagent's materialized
// content still matches its pinned digest, catching on-disk tampering that the
// name-based link check cannot see (F30). It re-hashes the cache-backed content
// and compares the active materialized file's bytes against it.
func (e *Engine) doctorRemoteDigests() []string {
	lock, err := remote.LoadLock(e.remoteLockPath())
	if err != nil {
		return []string{fmt.Sprintf("warn: remote lock unreadable: %v", err)}
	}
	cache := &remote.Cache{Root: e.RemoteCacheRoot}
	var names []string
	for _, entry := range lock.Entries {
		if entry.Kind == "subagent" {
			names = append(names, entry.Name)
		}
	}
	sort.Strings(names)
	var out []string
	for _, name := range names {
		entry, _ := lock.Get("subagent", name)
		pin, perr := remote.ParseDigest(entry.Digest)
		if perr != nil {
			out = append(out, fmt.Sprintf("warn: remote subagent %q: unparseable locked digest: %v", name, perr))
			continue
		}
		if verr := cache.VerifyContent(pin); verr != nil {
			out = append(out, fmt.Sprintf("warn: remote subagent %q: %v", name, verr))
			continue
		}
		active := filepath.Join(e.remoteSubagentDir(), name+".md")
		ab, aerr := os.ReadFile(active)
		if aerr != nil {
			out = append(out, fmt.Sprintf("warn: remote subagent %q: materialized content missing (%v)", name, aerr))
			continue
		}
		cb, cerr := os.ReadFile(filepath.Join(cache.Dir(pin), name+".md"))
		if cerr != nil {
			out = append(out, fmt.Sprintf("warn: remote subagent %q: cached content missing (%v)", name, cerr))
			continue
		}
		if !bytes.Equal(ab, cb) {
			out = append(out, fmt.Sprintf("warn: remote subagent %q: materialized content does not match locked digest %s", name, pin))
			continue
		}
		out = append(out, fmt.Sprintf("ok: remote subagent %q digest verified", name))
	}
	return out
}

// doctorOp parameterizes the per-resource health probe so the three near-
// identical doctor{Skills,Commands,Subagents} methods share one walk. Each
// kind supplies its own sourcePath (where the content lives) and linkPath
// (where it is symlinked into the tool).
type doctorOp struct {
	kind       string // "skill" | "command" | "subagent"
	entries    []config.NamedResource
	sourcePath func(e *Engine, entry config.NamedResource, tool string) (path string, skip bool)
	linkPath   func(e *Engine, tool string, entry config.NamedResource) string
}

// doctorResource walks op.entries and reports per-entry ok (content present and
// linked into the tool) or warn (content missing or link stale). skip=true on
// the sourcePath call suppresses the entry entirely — used for the subagent
// "deliberately not projected here" case so a by-design absence is never
// reported as a fixable finding.
func (e *Engine) doctorResource(tool string, op doctorOp) []string {
	var out []string
	for _, entry := range op.entries {
		name := entry.Name
		p, skip := op.sourcePath(e, entry, tool)
		if skip {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			out = append(out, fmt.Sprintf("warn: %s %q missing from %s (run apply)", op.kind, name, p))
			continue
		}
		dst := op.linkPath(e, tool, entry)
		if target, err := os.Readlink(dst); err == nil && target == p {
			out = append(out, fmt.Sprintf("ok: %s %q linked (%s)", op.kind, name, tool))
		} else {
			out = append(out, fmt.Sprintf("warn: %s %q content present, not linked for %s (run apply)", op.kind, name, tool))
		}
	}
	return out
}

// sourceDir is the shared source-path rule for a directory-style resource
// (skills): builtin:<name> resolves under the catalog root; local:<name> (or
// any other source) resolves under <contentDir>/<plural>/<source-or-entry-name>.
func (e *Engine) sourceDir(entry config.NamedResource, plural, catalogDir string) string {
	if strings.HasPrefix(entry.Resource.Source, "builtin:") {
		return filepath.Join(catalogDir, strings.TrimPrefix(entry.Resource.Source, "builtin:"))
	}
	sourceName := entry.Name
	if strings.HasPrefix(entry.Resource.Source, "local:") {
		sourceName = strings.TrimPrefix(entry.Resource.Source, "local:")
	}
	return filepath.Join(e.ContentDir, plural, sourceName)
}

// doctorSkills reports, per skill, whether its content is present at the right
// source (builtin: from the materialized catalog, local: from the content dir)
// and whether it is linked into the tool's skills directory.
func (e *Engine) doctorSkills(tool string, entries []config.NamedResource) []string {
	return e.doctorResource(tool, doctorOp{
		kind:    "skill",
		entries: entries,
		sourcePath: func(e *Engine, entry config.NamedResource, _ string) (string, bool) {
			return e.sourceDir(entry, "skills", e.CatalogDir()), false
		},
		linkPath: func(e *Engine, tool string, entry config.NamedResource) string {
			return filepath.Join(resourcepath.Dir(resourcepath.Skill, tool, entry.Resource.Scope, e.Home, e.ProjectRoot), entry.Name)
		},
	})
}

// doctorCommands reports, per command, whether its content file is present at
// the right source (builtin: from the materialized command root, local: from
// the content dir) and whether it is linked into the tool's command directory.
func (e *Engine) doctorCommands(tool string, entries []config.NamedResource) []string {
	return e.doctorResource(tool, doctorOp{
		kind:    "command",
		entries: entries,
		sourcePath: func(e *Engine, entry config.NamedResource, _ string) (string, bool) {
			return e.sourceDir(entry, "commands", e.CommandDir()) + ".md", false
		},
		linkPath: func(e *Engine, tool string, entry config.NamedResource) string {
			return filepath.Join(resourcepath.Dir(resourcepath.Command, tool, entry.Resource.Scope, e.Home, e.ProjectRoot), entry.Name+".md")
		},
	})
}

// doctorSubagents reports, per subagent, whether its content file is present at
// the right source (builtin: from the materialized subagent root, local: from
// the content dir) and whether it is linked into the tool's agent directory.
func (e *Engine) doctorSubagents(tool string, entries []config.NamedResource) []string {
	return e.doctorResource(tool, doctorOp{
		kind:    "subagent",
		entries: entries,
		sourcePath: func(e *Engine, entry config.NamedResource, tool string) (string, bool) {
			switch {
			case strings.HasPrefix(entry.Resource.Source, "builtin:"):
				// Mirror the adapter's per-tool variant preference: a subagent
				// with a neutral homonto: block is materialized as
				// <name>.<tool>.md and linked from there; fall back to the
				// shared <name>.md otherwise.
				base := strings.TrimPrefix(entry.Resource.Source, "builtin:")
				variant := filepath.Join(e.SubagentDir(), base+"."+tool+".md")
				shared := filepath.Join(e.SubagentDir(), base+".md")
				if fsutil.FileExists(variant) {
					return variant, false
				}
				// No variant. Mirror the adapters' skip rule too: an agent
				// that renders nothing for this tool (the Claude side of an
				// OpenCode-primary agent) is deliberately not projected here,
				// so its absent link is correct — warning about it would be a
				// permanent finding no apply could ever clear.
				if data, rerr := os.ReadFile(shared); rerr == nil && agentfm.NeedsTransform(data) {
					if projects, perr := agentfm.ProjectsFor(data, tool); perr == nil && !projects {
						return "", true
					}
				}
				return shared, false
			case strings.HasPrefix(entry.Resource.Source, "remote:"):
				return filepath.Join(e.remoteSubagentDir(), entry.Name+".md"), false
			default:
				return e.sourceDir(entry, "subagents", e.SubagentDir()) + ".md", false
			}
		},
		linkPath: func(e *Engine, tool string, entry config.NamedResource) string {
			return filepath.Join(resourcepath.Dir(resourcepath.Subagent, tool, entry.Resource.Scope, e.Home, e.ProjectRoot), entry.Name+".md")
		},
	})
}

// sortedRepos returns the declared repo names in deterministic order, so
// doctor findings list repos the same way every run.
func sortedRepos(repos map[string]string) []string {
	names := make([]string, 0, len(repos))
	for name := range repos {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
