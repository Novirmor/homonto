package workspacecfg

import (
	"fmt"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
)

// Fingerprint domains. Internal constants; colon-free per the fingerprint
// package contract.
const (
	domainConfig       = "workspacecfg.config"
	domainMembership   = "workspacecfg.membership"
	domainVerification = "workspacecfg.verification"
	domainPathClass    = "workspacecfg.pathclass"
)

// Fingerprint digests the whole configuration in canonical form (see the
// package doc): every field, members sorted by id, checks sorted by name,
// defaults materialized.
func Fingerprint(cfg Config) (fingerprint.Digest, error) {
	c := normalizedCopy(cfg)
	return fingerprint.CanonicalJSON(domainConfig, struct {
		SchemaVersion int          `json:"schema_version"`
		Workspace     Workspace    `json:"workspace"`
		Control       Control      `json:"control"`
		Members       []Member     `json:"members"`
		Routes        Routes       `json:"routes"`
		Integrations  Integrations `json:"integrations"`
		Update        Update       `json:"update"`
	}{
		SchemaVersion: c.SchemaVersion,
		Workspace:     c.Workspace,
		Control:       c.Control,
		Members:       c.Members,
		Routes:        c.Routes,
		Integrations:  c.Integrations,
		Update:        c.Update,
	})
}

// MembershipFingerprint digests only the fields that define which
// repositories constitute the workspace: workspace id+workflow, control
// id+path, and each member's id+path+kind. Verification checks, path
// classes, routes, integrations, and update policy never move it.
func MembershipFingerprint(cfg Config) fingerprint.Digest {
	c := normalizedCopy(cfg)
	type memberCore struct {
		ID   identity.RepositoryID `json:"id"`
		Path string                `json:"path"`
		Kind MemberKind            `json:"kind"`
	}
	members := make([]memberCore, len(c.Members))
	for i, m := range c.Members {
		members[i] = memberCore{ID: m.ID, Path: m.Path, Kind: m.Kind}
	}
	d, err := fingerprint.CanonicalJSON(domainMembership, struct {
		Workspace struct {
			ID       identity.WorkspaceID `json:"id"`
			Workflow Workflow             `json:"workflow"`
		} `json:"workspace"`
		Control struct {
			ID   identity.RepositoryID `json:"id"`
			Path string                `json:"path"`
		} `json:"control"`
		Members []memberCore `json:"members"`
	}{
		Workspace: struct {
			ID       identity.WorkspaceID `json:"id"`
			Workflow Workflow             `json:"workflow"`
		}{ID: c.Workspace.ID, Workflow: c.Workspace.Workflow},
		Control: struct {
			ID   identity.RepositoryID `json:"id"`
			Path string                `json:"path"`
		}{ID: c.Control.ID, Path: c.Control.Path},
		Members: members,
	})
	if err != nil {
		// Unreachable: every type above is JSON-marshalable by construction.
		return ""
	}
	return d
}

// VerificationFingerprint digests one member's checks (canonical: sorted by
// name, defaults materialized), prefixed by the member id so two members with
// identical checks never share a digest.
func VerificationFingerprint(cfg Config, repo identity.RepositoryID) (fingerprint.Digest, error) {
	m, ok := findMember(cfg, repo)
	if !ok {
		return "", fmt.Errorf("workspacecfg: member %s: %w", repo, ErrUnknownMember)
	}
	return fingerprint.CanonicalJSON(domainVerification, struct {
		Member identity.RepositoryID `json:"member"`
		Checks []Check               `json:"checks"`
	}{Member: m.ID, Checks: m.Verification})
}

// PathClassFingerprint digests one member's path classes. A nil class table
// and an explicitly empty one digest identically.
func PathClassFingerprint(cfg Config, repo identity.RepositoryID) (fingerprint.Digest, error) {
	m, ok := findMember(cfg, repo)
	if !ok {
		return "", fmt.Errorf("workspacecfg: member %s: %w", repo, ErrUnknownMember)
	}
	classes := m.Paths
	if classes == nil {
		classes = &PathClasses{}
	}
	return fingerprint.CanonicalJSON(domainPathClass, struct {
		Member  identity.RepositoryID `json:"member"`
		Classes PathClasses           `json:"classes"`
	}{Member: m.ID, Classes: *classes})
}

// findMember locates repo in cfg's canonicalized member list.
func findMember(cfg Config, repo identity.RepositoryID) (Member, bool) {
	c := normalizedCopy(cfg)
	for _, m := range c.Members {
		if m.ID == repo {
			return m, true
		}
	}
	return Member{}, false
}
