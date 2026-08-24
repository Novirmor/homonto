package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// FindingKind classifies a reconciliation finding.
type FindingKind string

// Reconciliation findings. Each is a hard error for using the workspace;
// the human resolves them (the tool never tidies, per ADR 0024).
const (
	// FindingMissingMember: a configured member does not exist on disk.
	FindingMissingMember FindingKind = "missing_member"
	// FindingKindMismatch: declared member kind disagrees with disk (git
	// without .git, non_git with one).
	FindingKindMismatch FindingKind = "kind_mismatch"
	// FindingScanMismatch: a discovered candidate classifies the member
	// differently than the manifest declares.
	FindingScanMismatch FindingKind = "scan_mismatch"
	// FindingControlIDMismatch: the member listed at the control path is
	// not the control repository (variant a: member at "." with a foreign
	// id).
	FindingControlIDMismatch FindingKind = "control_id_mismatch"
	// FindingControlPathMismatch: the control repository id is listed at
	// a path other than the control path (variant b).
	FindingControlPathMismatch FindingKind = "control_path_mismatch"
)

// Finding is one discrepancy between the manifest and the disk.
type Finding struct {
	// Kind classifies the discrepancy.
	Kind FindingKind
	// Member is the offending member's repository id.
	Member string
	// Path is the member's declared workspace-relative path.
	Path string
	// Detail explains the discrepancy in full.
	Detail string
}

// Reconcile compares the configured workspace membership against disk and
// discovery, returning every discrepancy. It checks that each configured
// member exists as a directory of its declared kind, that discovery
// classifies it the same way, and that the control identity is coherent:
// the member at the control path must be the control repository, and the
// control repository id may not be listed anywhere else.
func Reconcile(root string, cfg workspacecfg.Config, discovered []Candidate) []Finding {
	canon, err := CanonicalPath(root)
	if err != nil {
		return []Finding{{Kind: FindingMissingMember, Detail: fmt.Sprintf("workspace root: %v", err)}}
	}
	byPath := map[string]Candidate{}
	for _, c := range discovered {
		byPath[c.Path] = c
	}

	var findings []Finding
	emit := func(kind FindingKind, member workspacecfg.Member, format string, args ...any) {
		findings = append(findings, Finding{
			Kind:   kind,
			Member: string(member.ID),
			Path:   member.Path,
			Detail: fmt.Sprintf(format, args...),
		})
	}

	for _, m := range cfg.Members {
		abs := filepath.Join(canon, filepath.FromSlash(m.Path))
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			emit(FindingMissingMember, m, "member %s at %s is not a directory on disk", m.ID, m.Path)
			continue
		}
		hasGit := hasGitEntry(abs)
		if m.Kind == workspacecfg.KindGit && !hasGit {
			emit(FindingKindMismatch, m, "member %s declared git but %s has no .git", m.ID, m.Path)
		}
		if m.Kind == workspacecfg.KindNonGit && hasGit {
			emit(FindingKindMismatch, m, "member %s declared non_git but %s has .git", m.ID, m.Path)
		}
		if c, ok := byPath[abs]; ok && c.Kind != m.Kind {
			emit(FindingScanMismatch, m, "member %s declared %s but discovery classifies %s at %s", m.ID, m.Kind, c.Kind, m.Path)
		}

		if m.Path == cfg.Control.Path && m.ID != cfg.Control.ID {
			emit(FindingControlIDMismatch, m, "member %s at control path %s is not control repository %s", m.ID, m.Path, cfg.Control.ID)
		}
		if m.ID == cfg.Control.ID && m.Path != cfg.Control.Path {
			emit(FindingControlPathMismatch, m, "control repository %s listed at %s, not at control path %s", m.ID, m.Path, cfg.Control.Path)
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Kind < findings[j].Kind
	})
	return findings
}

// hasGitEntry reports whether dir has a .git entry.
func hasGitEntry(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
