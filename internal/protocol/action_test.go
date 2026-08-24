package protocol

import (
	"testing"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// Shared fixture identifiers.
const (
	testAPIRepoID  = identity.RepositoryID("11111111-2222-4333-8444-555555555555")
	testControlID  = identity.RepositoryID("11223344-5566-4777-8888-99aabbccdde0")
	testAction1    = identity.ActionID("23456789-01ab-4cde-9f01-234567890abc")
	testAction2    = identity.ActionID("3456789a-01ab-4cde-af01-234567890abc")
	testAction3    = identity.ActionID("456789ab-01ab-4cde-8f01-234567890abc")
	testSessionID  = identity.SessionID("56789abc-01ab-4cde-9f01-234567890abc")
	testToken      = identity.Token("ESIzRFVmd4iZqrvM3e7_ECEyQ1RldoeYqbrL3O3-DyA")
	testToken2     = identity.Token("ZR6tzJlu3E95T_uBUGANWLneGaxIYESSNlJeAxp7NJc")
	testToken3     = identity.Token("voGi0hBZWih60fCG-LQCaAy2j4vXkEg6RQbYBOzMszM")
	testDigestHex  = "d83306dd5bd697696fba8805fe3c02bbb1d9484cc7748823884484c566e6bfee"
	testDigestHex2 = "f26c5d15507a2a7e36d61c364bf25a6d3acfafc76d3698559bac7c80041c7334"
)

// validAssignment builds a structurally valid assignment action.
func validAssignment() Action {
	return Action{
		ID:             testAction1,
		Kind:           KindAssignment,
		FreshnessToken: testToken,
		Workflow:       workspacecfg.WorkflowTask,
		Path:           "docs/homonto/tasks/retry-backoff.md",
		Phase:          "do",
		Reason:         "implement the retry budget change",
		Role:           RoleImplementer,
		Prompt:         "Implement the exponential backoff budget from the plan.",
		Repository: RepositoryRef{
			ID:   testAPIRepoID,
			Path: "services/api",
		},
		WorkingDirectory: ".",
		WriteScope: WriteScope{
			ReadOnly: false,
			Paths:    []string{"internal/retry"},
		},
		InputFingerprints: []fingerprint.Digest{fingerprint.Digest(testDigestHex)},
		ExpectedReport:    &ExpectedReport{Kind: RoleImplementer, SchemaVersion: CurrentVersion},
	}
}

// validDecisionAction builds a structurally valid decision action.
func validDecisionAction() Action {
	a := validAssignment()
	a.ID = testAction2
	a.FreshnessToken = testToken3
	a.Kind = KindDecision
	a.Role = ""
	a.Workflow = workspacecfg.WorkflowChange
	a.Path = "docs/homonto/changes/parallel-importer.md"
	a.Phase = "fix"
	a.Reason = "reviewer reported a blocking finding that needs a human decision"
	a.Prompt = "The skeptic found a race in the parallel importer. Decide whether to accept the finding."
	a.Repository = RepositoryRef{ID: testControlID, Path: "."}
	a.ParallelGroupID = ""
	a.Dependencies = nil
	a.WriteScope = WriteScope{ReadOnly: true, Paths: []string{}}
	a.InputFingerprints = []fingerprint.Digest{fingerprint.Digest(testDigestHex2)}
	a.ExpectedReport = nil
	a.Decision = &DecisionSchema{
		Kind:   DecisionAcceptFinding,
		Prompt: "Accept finding F-2 and open a repair round?",
		Choices: []Choice{
			{Value: "accept", Label: "Accept the finding and order a repair", RequiresRationale: true},
			{Value: "reject", Label: "Reject the finding as a false positive", RequiresRationale: true},
		},
		FindingID: "F-2",
	}
	return a
}

func TestActionValidateAccepts(t *testing.T) {
	tests := []struct {
		name string
		a    Action
	}{
		{"assignment", validAssignment()},
		{"decision", validDecisionAction()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.a.Validate(); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}
}

func TestActionValidateRejects(t *testing.T) {
	// Each mutation targets one base kind: assignment (a), decision (d).
	tests := []struct {
		name   string
		base   byte
		mutate func(*Action)
	}{
		{"malformed id", 'a', func(a *Action) { a.ID = "nope" }},
		{"unknown kind", 'a', func(a *Action) { a.Kind = "wish" }},
		{"malformed token", 'a', func(a *Action) { a.FreshnessToken = "short" }},
		{"unknown workflow", 'a', func(a *Action) { a.Workflow = "epic" }},
		{"empty path", 'a', func(a *Action) { a.Path = "" }},
		{"absolute path", 'a', func(a *Action) { a.Path = "/etc/passwd" }},
		{"escaping path", 'a', func(a *Action) { a.Path = "../up.md" }},
		{"empty phase", 'a', func(a *Action) { a.Phase = "" }},
		{"empty reason", 'a', func(a *Action) { a.Reason = "" }},
		{"empty prompt", 'a', func(a *Action) { a.Prompt = "" }},
		{"malformed repository id", 'a', func(a *Action) { a.Repository.ID = "bad" }},
		{"empty repository path", 'a', func(a *Action) { a.Repository.Path = "" }},
		{"unclean repository path", 'a', func(a *Action) { a.Repository.Path = "services//api" }},
		{"empty working directory", 'a', func(a *Action) { a.WorkingDirectory = "" }},
		{"escaping working directory", 'a', func(a *Action) { a.WorkingDirectory = ".." }},
		{
			"writable scope without paths",
			'a',
			func(a *Action) { a.WriteScope = WriteScope{ReadOnly: false, Paths: nil} },
		},
		{
			"read-only scope with paths",
			'a',
			func(a *Action) {
				a.WriteScope = WriteScope{ReadOnly: true, Paths: []string{"x"}}
			},
		},
		{
			"blank write scope path",
			'a',
			func(a *Action) { a.WriteScope = WriteScope{ReadOnly: false, Paths: []string{" "}} },
		},
		{
			"escaping write scope path",
			'a',
			func(a *Action) { a.WriteScope = WriteScope{ReadOnly: false, Paths: []string{"../out"}} },
		},
		{
			"duplicate write scope paths",
			'a',
			func(a *Action) { a.WriteScope = WriteScope{ReadOnly: false, Paths: []string{"a", "a"}} },
		},
		{"blank parallel group id", 'a', func(a *Action) { a.ParallelGroupID = "  " }},
		{
			"malformed dependency id",
			'a',
			func(a *Action) { a.Dependencies = []identity.ActionID{"bad"} },
		},
		{
			"self dependency",
			'a',
			func(a *Action) { a.Dependencies = []identity.ActionID{testAction1} },
		},
		{
			"duplicate dependencies",
			'a',
			func(a *Action) {
				a.Dependencies = []identity.ActionID{testAction2, testAction2}
			},
		},
		{
			"malformed input fingerprint",
			'a',
			func(a *Action) { a.InputFingerprints = []fingerprint.Digest{"beef"} },
		},
		{
			"duplicate input fingerprints",
			'a',
			func(a *Action) {
				a.InputFingerprints = []fingerprint.Digest{fingerprint.Digest(testDigestHex), fingerprint.Digest(testDigestHex)}
			},
		},
		{
			"assignment without role",
			'a',
			func(a *Action) { a.Role = "" },
		},
		{
			"assignment with unknown role",
			'a',
			func(a *Action) { a.Role = "oracle" },
		},
		{
			"assignment without expected report",
			'a',
			func(a *Action) { a.ExpectedReport = nil },
		},
		{
			"assignment expected report kind disagrees with role",
			'a',
			func(a *Action) { a.ExpectedReport = &ExpectedReport{Kind: RoleReviewer, SchemaVersion: CurrentVersion} },
		},
		{
			"assignment expected report wrong schema version",
			'a',
			func(a *Action) { a.ExpectedReport = &ExpectedReport{Kind: RoleImplementer, SchemaVersion: 2} },
		},
		{
			"assignment carries a decision",
			'a',
			func(a *Action) {
				a.Decision = &DecisionSchema{Kind: DecisionApproveDesign, Prompt: "p?", Choices: []Choice{{Value: "y", Label: "Yes"}}}
			},
		},
		{
			"decision carries a role",
			'd',
			func(a *Action) { a.Role = RoleSkeptic },
		},
		{
			"decision without schema",
			'd',
			func(a *Action) { a.Decision = nil },
		},
		{
			"decision carries an expected report",
			'd',
			func(a *Action) {
				a.ExpectedReport = &ExpectedReport{Kind: RoleSkeptic, SchemaVersion: CurrentVersion}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := validAssignment()
			if tt.base == 'd' {
				a = validDecisionAction()
			}
			tt.mutate(&a)
			if err := a.Validate(); err == nil {
				t.Errorf("Validate accepted invalid action (%s)", tt.name)
			}
		})
	}
}

func TestNextResponseValidate(t *testing.T) {
	ready := func(n int) NextResponse {
		resp := NextResponse{ProtocolVersion: CurrentVersion, State: NextReady}
		for i := 0; i < n; i++ {
			a := validAssignment()
			a.ID = testAction1
			a.Dependencies = nil
			resp.Actions = append(resp.Actions, a)
		}
		return resp
	}
	decision := func() NextResponse {
		return NextResponse{
			ProtocolVersion: CurrentVersion,
			State:           NextBlocked,
			Actions:         []Action{validDecisionAction()},
		}
	}
	complete := NextResponse{ProtocolVersion: CurrentVersion, State: NextComplete, Actions: []Action{}}

	tests := []struct {
		name string
		resp NextResponse
		ok   bool
	}{
		{"ready with one action", ready(1), true},
		{"ready with parallel group", ready(2), true},
		{"ready with no actions", ready(0), false},
		{"blocked with one decision", decision(), true},
		{"blocked with no actions", NextResponse{ProtocolVersion: CurrentVersion, State: NextBlocked}, false},
		{
			"blocked with two actions",
			func() NextResponse {
				r := decision()
				r.Actions = append(r.Actions, validAssignment())
				return r
			}(),
			false,
		},
		{"complete with no actions", complete, true},
		{
			"complete with actions",
			func() NextResponse {
				r := complete
				r.Actions = []Action{validAssignment()}
				return r
			}(),
			false,
		},
		{
			"wrong protocol version",
			func() NextResponse { r := complete; r.ProtocolVersion = 2; return r }(),
			false,
		},
		{
			"unknown state",
			func() NextResponse { r := complete; r.State = NextState("paused"); return r }(),
			false,
		},
		{
			"invalid action inside response",
			func() NextResponse {
				r := ready(1)
				r.Actions[0].FreshnessToken = "bad"
				return r
			}(),
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.resp.Validate()
			if tt.ok && err != nil {
				t.Errorf("Validate: %v", err)
			}
			if !tt.ok && err == nil {
				t.Error("Validate accepted an invalid response")
			}
		})
	}
}
