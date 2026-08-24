package handoff

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/noviopenworks/homonto/internal/checkpoint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// ProposalStatus classifies how well a checkpoint member could be matched
// to the candidates discovered on the attaching machine.
type ProposalStatus string

const (
	// StatusExact: exactly one candidate sits at the member's
	// workspace-relative path with the declared kind.
	StatusExact ProposalStatus = "exact"
	// StatusChanged: no path-and-kind match, but exactly one candidate
	// shares a declared remote — the member moved and needs confirmation.
	StatusChanged ProposalStatus = "changed"
	// StatusAmbiguous: multiple candidates match equally; the human must
	// choose.
	StatusAmbiguous ProposalStatus = "ambiguous"
	// StatusMissing: no candidate matches the path, kind, or remotes.
	StatusMissing ProposalStatus = "missing"
)

// Proposal is the mapping proposal for one checkpoint member. Repository
// IDs are workspace-assigned UUIDs; paths and remotes are evidence for
// remapping, never identity by themselves (the committed logical ID is the
// identity), so every non-exact status carries reasons a human can act on.
type Proposal struct {
	// RepositoryID is the checkpoint member's workspace-assigned id.
	RepositoryID identity.RepositoryID
	// Candidates are the discovered directories this proposal matched
	// (empty for missing).
	Candidates []workspace.Candidate
	// Status classifies the match.
	Status ProposalStatus
	// Reasons explain a non-exact (or noteworthy) classification.
	Reasons []string
}

// ProposeMappings matches every checkpoint member except the control
// repository (attach receives the control root explicitly) against the
// discovered candidates, using the configuration's path and remote
// evidence:
//
//   - exact: exactly one candidate's path ends, segment-aligned, with the
//     member's configured workspace-relative path AND its kind matches.
//   - changed: no exact match, but exactly one candidate shares a declared
//     remote URL.
//   - ambiguous: several candidates match equally at either tier.
//   - missing: nothing matches at all.
//
// Proposals are sorted by repository id. The function is pure: it reads
// neither disk nor journal; candidates carry everything it inspects.
func ProposeMappings(cp checkpoint.Checkpoint, cfg workspacecfg.Config, candidates []workspace.Candidate) []Proposal {
	byID := make(map[identity.RepositoryID]workspacecfg.Member, len(cfg.Members))
	for _, m := range cfg.Members {
		byID[m.ID] = m
	}

	members := append([]checkpoint.Member(nil), cp.Members...)
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	proposals := make([]Proposal, 0, len(members))
	for _, m := range members {
		if m.ID == cfg.Control.ID {
			continue // the control repository is located by the attach request
		}
		cfgMember, ok := byID[m.ID]
		if !ok {
			proposals = append(proposals, Proposal{
				RepositoryID: m.ID,
				Status:       StatusMissing,
				Reasons:      []string{fmt.Sprintf("member %s is not configured", m.ID)},
			})
			continue
		}
		proposals = append(proposals, proposeMember(cfgMember, candidates))
	}
	return proposals
}

// proposeMember classifies one configured member against the candidates.
func proposeMember(m workspacecfg.Member, candidates []workspace.Candidate) Proposal {
	p := Proposal{RepositoryID: m.ID}

	var pathMatch, remoteMatch []workspace.Candidate
	var kindMismatched []workspace.Candidate
	for _, c := range candidates {
		if hasWorkspacePath(c.Path, m.Path) {
			if c.Kind == m.Kind {
				pathMatch = append(pathMatch, c)
				continue
			}
			kindMismatched = append(kindMismatched, c)
			if remotesIntersect(m.Remotes, c.Git) {
				remoteMatch = append(remoteMatch, c)
			}
			continue
		}
		if remotesIntersect(m.Remotes, c.Git) {
			remoteMatch = append(remoteMatch, c)
		}
	}

	switch {
	case len(pathMatch) == 1:
		p.Status = StatusExact
		p.Candidates = pathMatch
		if len(m.Remotes) > 0 && !remotesIntersect(m.Remotes, pathMatch[0].Git) {
			p.Reasons = []string{fmt.Sprintf(
				"declared remotes %v not observed on the path-matching candidate", m.Remotes)}
		}
	case len(pathMatch) > 1:
		p.Status = StatusAmbiguous
		p.Candidates = pathMatch
		p.Reasons = []string{fmt.Sprintf(
			"%d candidates match workspace path %q and kind %q equally: %s",
			len(pathMatch), m.Path, m.Kind, candidatePaths(pathMatch))}
	case len(remoteMatch) == 1:
		p.Status = StatusChanged
		p.Candidates = remoteMatch
		c := remoteMatch[0]
		if hasWorkspacePath(c.Path, m.Path) {
			p.Reasons = []string{fmt.Sprintf(
				"remotes intersect but the candidate kind %s differs from the declared kind %q at workspace path %q",
				c.Kind, m.Kind, m.Path)}
		} else {
			p.Reasons = []string{fmt.Sprintf(
				"remotes intersect but the candidate path %s differs from workspace path %q",
				c.Path, m.Path)}
		}
	case len(remoteMatch) > 1:
		p.Status = StatusAmbiguous
		p.Candidates = remoteMatch
		p.Reasons = []string{fmt.Sprintf(
			"%d candidates share the declared remotes at different paths: %s",
			len(remoteMatch), candidatePaths(remoteMatch))}
	default:
		p.Status = StatusMissing
		p.Reasons = []string{fmt.Sprintf(
			"no candidate matches workspace path %q, kind %q, or declared remotes %v",
			m.Path, m.Kind, m.Remotes)}
		for _, c := range kindMismatched {
			p.Reasons = append(p.Reasons, fmt.Sprintf(
				"candidate at %s has kind %q, want %q", c.Path, c.Kind, m.Kind))
		}
	}
	return p
}

// hasWorkspacePath reports whether absPath ends, segment-aligned, with the
// configured workspace-relative path rel: the candidate sits at the
// member's position under some workspace root. On the attaching machine the
// root is the clone's location; the comparison deliberately tolerates the
// root moving between machines, which is the entire point of remapping.
func hasWorkspacePath(absPath, rel string) bool {
	if rel == "." {
		return false // the control root is never proposed
	}
	suffix := filepath.FromSlash(rel)
	sep := string(filepath.Separator)
	return absPath == suffix || len(absPath) > len(suffix)+1 &&
		absPath[len(absPath)-len(suffix):] == suffix &&
		absPath[len(absPath)-len(suffix)-1] == sep[0]
}

// remotesIntersect reports whether any declared remote URL also appears in
// the candidate repository's git remotes.
func remotesIntersect(declared []string, git *gitx.Repository) bool {
	if len(declared) == 0 || git == nil {
		return false
	}
	urls := make(map[string]bool, len(git.Remotes))
	for _, r := range git.Remotes {
		urls[r.URL] = true
	}
	for _, d := range declared {
		if urls[d] {
			return true
		}
	}
	return false
}

// candidatePaths renders the candidate paths for a reason string.
func candidatePaths(cs []workspace.Candidate) string {
	out := ""
	for i, c := range cs {
		if i > 0 {
			out += ", "
		}
		out += c.Path
	}
	return out
}
