package deltamerge

import (
	"strings"
	"testing"
)

const living = `# Auth

Living spec.

## Requirements

### Requirement: Login

The system SHALL authenticate a user with a valid password.

#### Scenario: valid login

- **WHEN** a user submits a valid password
- **THEN** a session is created

### Requirement: Logout

The system SHALL end a session on logout.

#### Scenario: logout

- **WHEN** a user logs out
- **THEN** the session is destroyed
`

func mustMerge(t *testing.T, cap, liv, delta string) string {
	t.Helper()
	out, err := Merge(cap, liv, delta)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if f := Lint(out); len(f) != 0 {
		t.Fatalf("merged spec has lint findings %v:\n%s", f, out)
	}
	return out
}

func TestMerge_Added(t *testing.T) {
	delta := `# Delta

## ADDED Requirements

### Requirement: Reset Password

The system SHALL let a user reset a forgotten password.

#### Scenario: reset

- **WHEN** a user requests a reset
- **THEN** a reset link is emailed
`
	out := mustMerge(t, "auth", living, delta)
	if !strings.Contains(out, "### Requirement: Reset Password") {
		t.Errorf("added requirement missing:\n%s", out)
	}
	// Existing ones preserved, no delta headings leaked.
	if !strings.Contains(out, "### Requirement: Login") || strings.Contains(out, "## ADDED") {
		t.Errorf("merge dropped existing or leaked delta heading:\n%s", out)
	}
}

func TestMerge_ModifiedReplacesEntirely(t *testing.T) {
	delta := `## MODIFIED Requirements

### Requirement: Login

The system SHALL authenticate a user with a password AND a second factor.

#### Scenario: 2fa login

- **WHEN** a user submits password + OTP
- **THEN** a session is created
`
	out := mustMerge(t, "auth", living, delta)
	if !strings.Contains(out, "second factor") || strings.Contains(out, "valid password") {
		t.Errorf("MODIFIED must replace the whole requirement, not append:\n%s", out)
	}
	if strings.Count(out, "### Requirement: Login") != 1 {
		t.Errorf("MODIFIED duplicated the requirement:\n%s", out)
	}
}

func TestMerge_Removed(t *testing.T) {
	delta := "## REMOVED Requirements\n\n### Requirement: Logout\n\nNo longer a separate flow.\n"
	out := mustMerge(t, "auth", living, delta)
	if strings.Contains(out, "### Requirement: Logout") {
		t.Errorf("REMOVED requirement still present:\n%s", out)
	}
	if !strings.Contains(out, "### Requirement: Login") {
		t.Errorf("REMOVED dropped an unrelated requirement:\n%s", out)
	}
}

func TestMerge_RenamedThenModifiedNewName(t *testing.T) {
	// RENAMED applies before MODIFIED, so a MODIFIED targeting the new name resolves.
	delta := `## RENAMED Requirements

- FROM: Login
  TO: Sign In

## MODIFIED Requirements

### Requirement: Sign In

The system SHALL authenticate via SSO.

#### Scenario: sso

- **WHEN** a user signs in via SSO
- **THEN** a session is created
`
	out := mustMerge(t, "auth", living, delta)
	if !strings.Contains(out, "### Requirement: Sign In") || strings.Contains(out, "### Requirement: Login") {
		t.Errorf("rename not applied:\n%s", out)
	}
	if !strings.Contains(out, "SSO") {
		t.Errorf("modified-after-rename not applied:\n%s", out)
	}
}

func TestMerge_EmptyLivingCreatesSpec(t *testing.T) {
	delta := "## ADDED Requirements\n\n### Requirement: First\n\nThe system SHALL do X.\n\n#### Scenario: s\n\n- **WHEN** x\n- **THEN** y\n"
	out := mustMerge(t, "billing", "", delta)
	if !strings.HasPrefix(out, "# billing") || !strings.Contains(out, "## Requirements") || !strings.Contains(out, "### Requirement: First") {
		t.Errorf("new living spec malformed:\n%s", out)
	}
}

func TestMerge_Idempotentish_MissingTargetsError(t *testing.T) {
	if _, err := Merge("auth", living, "## MODIFIED Requirements\n\n### Requirement: Nope\n\nThe system SHALL x.\n"); err == nil {
		t.Error("MODIFIED of an absent requirement must error")
	}
	if _, err := Merge("auth", living, "## REMOVED Requirements\n\n### Requirement: Ghost\n"); err == nil {
		t.Error("REMOVED of an absent requirement must error")
	}
	if _, err := Merge("auth", living, "## ADDED Requirements\n\n### Requirement: Login\n\nThe system SHALL x.\n"); err == nil {
		t.Error("ADDED of an existing name must error (would duplicate)")
	}
}

func TestMerge_RejectsMalformedOrVacuousDelta(t *testing.T) {
	for _, delta := range []string{
		"",
		"# Delta only\n",
		"## ADDED Requirements\n",
		"## CHANGED Requirements\n\n### Requirement: A\n",
		"### Requirement: A\n\nSHALL a.\n",
		"## RENAMED Requirements\n\n- FROM: Login\n",
		"## RENAMED Requirements\n\n- FROM: Login\n- FROM: Logout\n  TO: Sign Out\n",
	} {
		if _, err := Merge("auth", living, delta); err == nil {
			t.Errorf("Merge accepted malformed delta %q", delta)
		}
	}
}

func TestLint_CatchesLeakAndDuplicate(t *testing.T) {
	bad := "# X\n\n## Requirements\n\n### Requirement: A\n\nSHALL.\n\n### Requirement: A\n\nSHALL.\n\n## ADDED Requirements\n"
	f := Lint(bad)
	if len(f) < 2 {
		t.Errorf("lint should catch the duplicate AND the leaked heading, got %v", f)
	}
}

// A living spec may carry trailing level-2 sections after its requirements
// (Rationale, Notes, Examples, etc.). The merge must preserve them verbatim —
// silently dropping prose is data loss. See F5.
func TestMerge_PreservesTrailingSections(t *testing.T) {
	const livingWithTrailer = `# Auth

Living spec.

## Requirements

### Requirement: Login

The system SHALL authenticate a user with a valid password.

## Rationale

Security review notes live here.

## References

- [OAuth 2.0](https://example.org)
`
	delta := `## ADDED Requirements

### Requirement: Reset Password

The system SHALL let a user reset a forgotten password.
`
	out := mustMerge(t, "auth", livingWithTrailer, delta)
	if !strings.Contains(out, "## Rationale") {
		t.Errorf("trailing Rationale section dropped:\n%s", out)
	}
	if !strings.Contains(out, "Security review notes live here.") {
		t.Errorf("trailing Rationale body dropped:\n%s", out)
	}
	if !strings.Contains(out, "## References") {
		t.Errorf("trailing References section dropped:\n%s", out)
	}
	if !strings.Contains(out, "[OAuth 2.0]") {
		t.Errorf("trailing References body dropped:\n%s", out)
	}
	if !strings.Contains(out, "### Requirement: Reset Password") {
		t.Errorf("ADDED requirement missing:\n%s", out)
	}
}

// Applied must also recognize a POST-state that still carries trailing prose,
// so crash recovery does not flag a legitimately-merged spec as needing re-merge.
func TestApplied_TrailingSectionsPreserved(t *testing.T) {
	const livingWithTrailer = `# Auth

## Requirements

### Requirement: Login

SHALL authenticate.

### Requirement: Reset

SHALL reset.

## Notes

Background prose.
`
	delta := "## ADDED Requirements\n\n### Requirement: Reset\n\nSHALL reset.\n"
	if !Applied("auth", livingWithTrailer, delta) {
		t.Errorf("Applied must hold when trailing sections survive the merge:\n%s", livingWithTrailer)
	}
}
