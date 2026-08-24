package checkpoint

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// Shared fixture identifiers (all canonical UUIDv4).
const (
	testWorkspaceID = identity.WorkspaceID("0a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d")
	testControlID   = identity.RepositoryID("11223344-5566-4777-8888-99aabbccdde0")
	testAPIID       = identity.RepositoryID("11111111-2222-4333-8444-555555555555")
	testDocsID      = identity.RepositoryID("aaaaaaa1-bbbb-4cc2-9ddd-eeeeeeeeeeee")
	testWorkID      = identity.WorkID("12345678-90ab-4cde-8f01-234567890abc")
	testToken       = identity.Token("ESIzRFVmd4iZqrvM3e7_ECEyQ1RldoeYqbrL3O3-DyA")
	testToken2      = identity.Token("ZR6tzJlu3E95T_uBUGANWLneGaxIYESSNlJeAxp7NJc")
	testDigest      = "d83306dd5bd697696fba8805fe3c02bbb1d9484cc7748823884484c566e6bfee"
	testDigest2     = "f26c5d15507a2a7e36d61c364bf25a6d3acfafc76d3698559bac7c80041c7334"
	testSourceFP    = "f0628fb519ab7e801bfe5dff612a110525e173d073e513f8d33c2233ea5c7ea1"
)

// validCfg builds a workspace configuration the checkpoint fixtures
// validate against.
func validCfg() workspacecfg.Config {
	return workspacecfg.Config{
		SchemaVersion: 1,
		Workspace:     workspacecfg.Workspace{ID: testWorkspaceID, Workflow: workspacecfg.WorkflowTask},
		Control:       workspacecfg.Control{ID: testControlID, Path: "."},
		Members: []workspacecfg.Member{
			{ID: testControlID, Path: ".", Kind: workspacecfg.KindGit},
			{ID: testAPIID, Path: "services/api", Kind: workspacecfg.KindGit},
			{ID: testDocsID, Path: "docs/notes", Kind: workspacecfg.KindNonGit},
		},
	}
}

// validCheckpoint returns a checkpoint consistent with validCfg.
func validCheckpoint() Checkpoint {
	return Checkpoint{
		SchemaVersion:     CurrentSchemaVersion,
		WorkspaceID:       testWorkspaceID,
		ConfigFingerprint: testDigest,
		Work: &Work{
			ID:         testWorkID,
			Name:       "retry-backoff",
			Workflow:   workspacecfg.WorkflowTask,
			Path:       "docs/homonto/tasks/retry-backoff.md",
			Phase:      "do",
			Generation: 1,
		},
		Members: []Member{
			{
				ID:                testAPIID,
				Kind:              workspacecfg.KindGit,
				BaseBranch:        "main",
				BaseCommit:        strings.Repeat("a", 40),
				IntegrationBranch: "homonto/retry-backoff",
				IntegrationCommit: "",
				SourceFingerprint: testSourceFP,
			},
			{
				ID:                testDocsID,
				Kind:              workspacecfg.KindNonGit,
				SourceFingerprint: testSourceFP,
			},
		},
		UnresolvedGates: []string{"approve-design"},
		Next:            &Next{Summary: "run implementer assignment for the retry budget change"},
		Handoff:         Handoff{State: HandoffLocal, Generation: 1},
	}
}

func TestValidateAccepts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Checkpoint)
	}{
		{"full checkpoint", nil},
		{"no active work", func(cp *Checkpoint) { cp.Work = nil }},
		{"no next hint", func(cp *Checkpoint) { cp.Next = nil }},
		{"no unresolved gates", func(cp *Checkpoint) { cp.UnresolvedGates = []string{} }},
		{"no members", func(cp *Checkpoint) { cp.Members = []Member{} }},
		{
			"transferable handoff",
			func(cp *Checkpoint) {
				cp.Handoff = Handoff{State: HandoffTransferable, Generation: 2, TransferID: testToken}
			},
		},
		{
			"consumed handoff",
			func(cp *Checkpoint) {
				cp.Handoff = Handoff{State: HandoffConsumed, Generation: 2, TransferID: testToken}
			},
		},
		{"git member with integration commit", func(cp *Checkpoint) {
			cp.Members[0].IntegrationCommit = strings.Repeat("b", 40)
		}},
		{"sha256 commit ids", func(cp *Checkpoint) {
			cp.Members[0].BaseCommit = strings.Repeat("c", 64)
			cp.Members[0].IntegrationCommit = strings.Repeat("d", 64)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validCfg()
			fp, err := workspacecfg.Fingerprint(cfg)
			if err != nil {
				t.Fatal(err)
			}
			cp := validCheckpoint()
			cp.ConfigFingerprint = fp
			if tt.mutate != nil {
				tt.mutate(&cp)
			}
			if err := Validate(cp, cfg); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Checkpoint, *workspacecfg.Config)
	}{
		{"wrong schema version", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.SchemaVersion = 2 }},
		{"zero schema version", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.SchemaVersion = 0 }},
		{"malformed workspace id", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.WorkspaceID = "not-a-uuid" }},
		{
			"workspace id mismatch",
			func(cp *Checkpoint, cfg *workspacecfg.Config) {
				cfg.Workspace.ID = identity.WorkspaceID("99999999-9999-4999-8999-999999999999")
			},
		},
		{"config fingerprint mismatch", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.ConfigFingerprint = testDigest2 }},
		{"malformed config fingerprint", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.ConfigFingerprint = "short" }},
		{"work id malformed", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Work.ID = "oops" }},
		{"work name not kebab", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Work.Name = "Not A Name" }},
		{"work name reserved", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Work.Name = "task" }},
		{"work workflow unknown", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Work.Workflow = "epic" }},
		{
			"work workflow disagrees with config",
			func(cp *Checkpoint, cfg *workspacecfg.Config) { cfg.Workspace.Workflow = workspacecfg.WorkflowChange },
		},
		{"work path absolute", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Work.Path = "/etc/passwd" }},
		{"work path escaping", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Work.Path = "../outside.md" }},
		{"work path empty", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Work.Path = "" }},
		{"work phase empty", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Work.Phase = "" }},
		{"work generation zero", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Work.Generation = 0 }},
		{
			"member unknown to config",
			func(cp *Checkpoint, _ *workspacecfg.Config) {
				cp.Members = append(cp.Members, Member{
					ID:                identity.RepositoryID("99999999-9999-4999-8999-999999999999"),
					Kind:              workspacecfg.KindGit,
					SourceFingerprint: testSourceFP,
				})
			},
		},
		{
			"member kind mismatch",
			func(cp *Checkpoint, cfg *workspacecfg.Config) {
				cfg.Members[1].Kind = workspacecfg.KindNonGit
			},
		},
		{
			"duplicate member ids",
			func(cp *Checkpoint, _ *workspacecfg.Config) {
				cp.Members = append(cp.Members, cp.Members[0])
			},
		},
		{
			"member id malformed",
			func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Members[0].ID = "nope" },
		},
		{
			"member kind unknown",
			func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Members[0].Kind = "svn" },
		},
		{
			"git member without base branch",
			func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Members[0].BaseBranch = "" },
		},
		{
			"git member without base commit",
			func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Members[0].BaseCommit = "" },
		},
		{
			"git member without integration branch",
			func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Members[0].IntegrationBranch = "" },
		},
		{
			"git member with malformed base commit",
			func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Members[0].BaseCommit = "zz" },
		},
		{
			"git member with non-hex commit",
			func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Members[0].IntegrationCommit = "ghijkl" },
		},
		{
			"non-git member with branches",
			func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Members[1].BaseBranch = "main" },
		},
		{
			"non-git member with commit",
			func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Members[1].BaseCommit = strings.Repeat("a", 40) },
		},
		{
			"member source fingerprint malformed",
			func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Members[0].SourceFingerprint = "beef" },
		},
		{
			"duplicate unresolved gates",
			func(cp *Checkpoint, _ *workspacecfg.Config) {
				cp.UnresolvedGates = []string{"approve-design", "approve-design"}
			},
		},
		{
			"blank unresolved gate",
			func(cp *Checkpoint, _ *workspacecfg.Config) { cp.UnresolvedGates = []string{"  "} },
		},
		{"next hint empty", func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Next = &Next{Summary: ""} }},
		{
			"handoff state unknown",
			func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Handoff.State = "limbo" },
		},
		{
			"handoff generation zero",
			func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Handoff.Generation = 0 },
		},
		{
			"local handoff carries transfer id",
			func(cp *Checkpoint, _ *workspacecfg.Config) { cp.Handoff.TransferID = testToken },
		},
		{
			"transferable handoff without transfer id",
			func(cp *Checkpoint, _ *workspacecfg.Config) {
				cp.Handoff = Handoff{State: HandoffTransferable, Generation: 2}
			},
		},
		{
			"transferable handoff with malformed transfer id",
			func(cp *Checkpoint, _ *workspacecfg.Config) {
				cp.Handoff = Handoff{State: HandoffTransferable, Generation: 2, TransferID: "short"}
			},
		},
		{
			"consumed handoff without transfer id",
			func(cp *Checkpoint, _ *workspacecfg.Config) {
				cp.Handoff = Handoff{State: HandoffConsumed, Generation: 2}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validCfg()
			fp, err := workspacecfg.Fingerprint(cfg)
			if err != nil {
				t.Fatal(err)
			}
			cp := validCheckpoint()
			cp.ConfigFingerprint = fp
			tt.mutate(&cp, &cfg)
			if err := Validate(cp, cfg); err == nil {
				t.Errorf("Validate accepted an invalid checkpoint")
			}
		})
	}
}

func TestValidateTransition(t *testing.T) {
	local := func(gen uint64) Checkpoint {
		cp := validCheckpoint()
		cp.Handoff = Handoff{State: HandoffLocal, Generation: gen}
		return cp
	}
	transferable := func(gen uint64, tok identity.Token) Checkpoint {
		cp := validCheckpoint()
		cp.Handoff = Handoff{State: HandoffTransferable, Generation: gen, TransferID: tok}
		return cp
	}
	consumed := func(gen uint64, tok identity.Token) Checkpoint {
		cp := validCheckpoint()
		cp.Handoff = Handoff{State: HandoffConsumed, Generation: gen, TransferID: tok}
		return cp
	}
	withWorkspace := func(id identity.WorkspaceID, cp Checkpoint) Checkpoint {
		cp.WorkspaceID = id
		return cp
	}
	tests := []struct {
		name string
		prev Checkpoint
		next Checkpoint
		ok   bool
	}{
		{"local update keeps generation", local(1), local(1), true},
		{"local update bumps generation illegally", local(1), local(2), false},
		{"prepare transfer bumps generation", local(1), transferable(2, testToken), true},
		{"prepare transfer keeps generation illegally", local(1), transferable(1, testToken), false},
		{"consume transfer keeps generation", transferable(2, testToken), consumed(2, testToken), true},
		{"consume transfer changes generation illegally", transferable(2, testToken), consumed(3, testToken), false},
		{"consume transfer changes transfer id illegally", transferable(2, testToken), consumed(2, testToken2), false},
		{"transferable update keeps generation", transferable(2, testToken), transferable(2, testToken), true},
		{"consumed update keeps generation", consumed(2, testToken), consumed(2, testToken), true},
		{"backwards without generation bump refused", consumed(2, testToken), local(2), false},
		{"backwards with generation bump is forced takeover", consumed(2, testToken), local(3), true},
		{"cancel transferable backwards without bump refused", transferable(2, testToken), local(2), false},
		{"cancel transferable with bump allowed", transferable(2, testToken), local(3), true},
		{"local cannot jump straight to consumed", local(1), consumed(2, testToken), false},
		{"workspace identity cannot change mid-transition", local(1), withWorkspace(
			identity.WorkspaceID("99999999-9999-4999-8999-999999999999"), transferable(2, testToken)), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTransition(tt.prev, tt.next)
			if tt.ok && err != nil {
				t.Errorf("ValidateTransition rejected a legal transition: %v", err)
			}
			if !tt.ok && err == nil {
				t.Errorf("ValidateTransition accepted an illegal transition")
			}
		})
	}
}

// forbiddenNameTokens are the substrings no portable checkpoint field name
// may carry: recovery tokens, secrets, raw report text, command output, or
// anything recovery related must never gain a place in the checkpoint.
var forbiddenNameTokens = []string{"token", "secret", "output", "report", "recovery"}

// TestCheckpointCarriesNoSecrets walks the checkpoint schema via reflection
// and fails if any JSON field name carries a forbidden token. The detector
// itself is proven by TestForbiddenNameDetectorFires.
func TestCheckpointCarriesNoSecrets(t *testing.T) {
	var names []string
	collectJSONNames(reflect.TypeOf(Checkpoint{}), nil, &names)
	if len(names) == 0 {
		t.Fatal("reflection walked the schema and found no JSON names; the guard is broken")
	}
	for _, name := range names {
		if bad, ok := forbiddenNameMatch(name); ok {
			t.Errorf("checkpoint field %q matches forbidden name %q", name, bad)
		}
	}
}

// TestForbiddenNameDetectorFires is the positive control for
// TestCheckpointCarriesNoSecrets: a synthetic schema carrying a tagged
// secret field, a mixed-case one, and an untagged one must have all three
// collected and flagged. Without it the negative test only proves the real
// schema is currently clean, not that the detector fires.
func TestForbiddenNameDetectorFires(t *testing.T) {
	type leaky struct {
		SessionToken string `json:"session_token"`
		RecoveryKey  string `json:"Recovery_Key"`
		Secret       string
	}
	var names []string
	collectJSONNames(reflect.TypeOf(leaky{}), nil, &names)
	if len(names) != 3 {
		t.Fatalf("collector visited %d fields (%v), want 3: untagged fields must not slip through", len(names), names)
	}
	for _, name := range names {
		if _, ok := forbiddenNameMatch(name); !ok {
			t.Errorf("field %q escaped the forbidden-name detector", name)
		}
	}
}

// forbiddenNameMatch reports which forbidden token a collected field name
// carries, comparing lowercased substrings so spellings like
// "Recovery_Key" cannot evade the guard.
func forbiddenNameMatch(name string) (bad string, ok bool) {
	lower := strings.ToLower(name)
	for _, bad := range forbiddenNameTokens {
		if strings.Contains(lower, bad) {
			return bad, true
		}
	}
	return "", false
}

func TestEncodeOmitsInactiveWorkAndNext(t *testing.T) {
	cp := validCheckpoint()
	cp.Work = nil
	cp.Next = nil
	b, err := Encode(cp)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(`"work"`)) {
		t.Error("encoded checkpoint mentions \"work\" with no active work")
	}
	if bytes.Contains(b, []byte(`"next"`)) {
		t.Error("encoded checkpoint mentions \"next\" with no hint")
	}
	cp.Work = &Work{ID: testWorkID, Name: "retry-backoff", Workflow: workspacecfg.WorkflowTask,
		Path: "docs/homonto/tasks/retry-backoff.md", Phase: "do", Generation: 1}
	b, err = Encode(cp)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"work"`)) {
		t.Error("encoded checkpoint omits \"work\" with active work present")
	}
}

// TestLoadMissingFile exercises the error path of package-level Load.
func TestLoadMissingFile(t *testing.T) {
	if _, _, err := Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("Load accepted a missing file")
	}
}

// collectJSONNames recursively walks ty (structs, pointers, slices, arrays,
// maps) and appends every JSON field name declared along the way.
func collectJSONNames(ty reflect.Type, seen map[reflect.Type]bool, names *[]string) {
	if ty == nil {
		return
	}
	for ty.Kind() == reflect.Pointer {
		ty = ty.Elem()
	}
	switch ty.Kind() {
	case reflect.Slice, reflect.Array:
		collectJSONNames(ty.Elem(), seen, names)
	case reflect.Map:
		collectJSONNames(ty.Key(), seen, names)
		collectJSONNames(ty.Elem(), seen, names)
	case reflect.Struct:
		if seen[ty] {
			return
		}
		if seen == nil {
			seen = map[reflect.Type]bool{}
		}
		seen[ty] = true
		for i := 0; i < ty.NumField(); i++ {
			f := ty.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name := strings.Split(tag, ",")[0]
			if name == "" {
				// An untagged field marshals under its Go field name.
				name = f.Name
			}
			*names = append(*names, name)
			collectJSONNames(f.Type, seen, names)
		}
	}
}
