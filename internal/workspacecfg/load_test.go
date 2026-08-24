package workspacecfg

import (
	"bytes"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/schema"
)

// Valid UUIDv4 identifiers shared by fixtures and hand-built configs.
const (
	testWorkspaceID  = "0a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d"
	testControlID    = "11223344-5566-4777-8888-99aabbccdde0"
	testMemberAPIID  = "aaaabbbb-cccc-4ddd-8eee-000011112222"
	testMemberDocsID = "7777aaaa-bbbb-4ccc-8ddd-333344445555"
)

func mustLoad(t *testing.T, path string) Config {
	t.Helper()
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	return cfg
}

func TestLoadFullConfig(t *testing.T) {
	cfg := mustLoad(t, filepath.Join("testdata", "valid-full.toml"))

	if cfg.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", cfg.SchemaVersion)
	}
	if cfg.Workspace.ID != testWorkspaceID {
		t.Errorf("Workspace.ID = %q, want %q", cfg.Workspace.ID, testWorkspaceID)
	}
	if cfg.Workspace.Workflow != WorkflowTask {
		t.Errorf("Workspace.Workflow = %q, want %q", cfg.Workspace.Workflow, WorkflowTask)
	}
	if cfg.Control.Path != "." || cfg.Control.ID != testControlID {
		t.Errorf("Control = %+v", cfg.Control)
	}
	if got, want := cfg.Control.Remotes, []string{"git@example.com:acme/control.git"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Control.Remotes = %v, want %v", got, want)
	}

	// Canonical form sorts members by id: control(1122...) < docs(7777...)
	// < api(aaaa...), which differs from the fixture's declaration order.
	wantPaths := []string{".", "docs/notes", "services/api"}
	if len(cfg.Members) != 3 {
		t.Fatalf("len(Members) = %d, want 3", len(cfg.Members))
	}
	for i, want := range wantPaths {
		if cfg.Members[i].Path != want {
			t.Errorf("Members[%d].Path = %q, want %q", i, cfg.Members[i].Path, want)
		}
	}
	control := cfg.Members[0]
	if control.Kind != KindGit {
		t.Errorf("control member Kind = %q, want %q", control.Kind, KindGit)
	}
	// Checks are sorted by name: fixture declares unit before lint.
	if len(control.Verification) != 2 || control.Verification[0].Name != "lint" {
		t.Fatalf("control Verification = %+v, want lint first", control.Verification)
	}
	lint := control.Verification[0]
	unit := control.Verification[1]
	// Defaults: absent timeout materializes as 10m, absent working_dir as ".".
	if lint.Timeout != "10m" {
		t.Errorf("lint.Timeout = %q, want default %q", lint.Timeout, "10m")
	}
	if unit.WorkingDir != "." {
		t.Errorf("unit.WorkingDir = %q, want default %q", unit.WorkingDir, ".")
	}
	if unit.Timeout != "5m" {
		t.Errorf("unit.Timeout = %q, want %q", unit.Timeout, "5m")
	}
	if got, want := lint.Environment, []string{"CI", "GOFLAGS"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lint.Environment = %v, want %v", got, want)
	}
	if control.Paths == nil {
		t.Fatal("control member Paths = nil, want classes")
	}
	if got, want := control.Paths.Tests, []string{"**/*_test.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Paths.Tests = %v, want %v", got, want)
	}
	if cfg.Members[2].Kind != KindGit || len(cfg.Members[2].Verification) != 1 {
		t.Errorf("api member = %+v", cfg.Members[2])
	}
	if cfg.Members[1].Kind != KindNonGit {
		t.Errorf("docs member Kind = %q, want %q", cfg.Members[1].Kind, KindNonGit)
	}

	if cfg.Routes.Claude == nil || cfg.Routes.Claude.Explorer == nil {
		t.Fatal("Routes.Claude.Explorer = nil, want route")
	}
	if got := cfg.Routes.Claude.Explorer; got.Model != "claude-opus-4-5" || got.Effort != "high" || got.Variant != "" {
		t.Errorf("Routes.Claude.Explorer = %+v", got)
	}
	if cfg.Routes.Claude.Skeptic != nil {
		t.Error("Routes.Claude.Skeptic = non-nil, want absent")
	}
	if cfg.Routes.OpenCode == nil || cfg.Routes.OpenCode.Skeptic == nil || cfg.Routes.OpenCode.Skeptic.Model != "g-5" {
		t.Error("Routes.OpenCode.Skeptic missing or wrong")
	}
	if cfg.Integrations.CommitGenerated {
		t.Error("Integrations.CommitGenerated = true, want false")
	}
	if cfg.Update.Channel != ChannelStable {
		t.Errorf("Update.Channel = %q, want %q", cfg.Update.Channel, ChannelStable)
	}
}

func TestLoadMinimalConfig(t *testing.T) {
	cfg := mustLoad(t, filepath.Join("testdata", "valid-minimal.toml"))
	// Zero members are legal at load: init can be mid-flight.
	if len(cfg.Members) != 0 {
		t.Errorf("len(Members) = %d, want 0", len(cfg.Members))
	}
	if cfg.Workspace.Workflow != WorkflowChange {
		t.Errorf("Workflow = %q, want %q", cfg.Workspace.Workflow, WorkflowChange)
	}
	if cfg.Control.Path != "." {
		t.Errorf("Control.Path = %q, want %q", cfg.Control.Path, ".")
	}
	if cfg.Control.Remotes != nil {
		t.Errorf("Control.Remotes = %v, want nil", cfg.Control.Remotes)
	}
	if cfg.Routes.Claude != nil || cfg.Routes.OpenCode != nil {
		t.Error("Routes should be absent in the minimal config")
	}
	if cfg.Update.Channel != "" {
		t.Errorf("Update.Channel = %q, want empty", cfg.Update.Channel)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join("testdata", "does-not-exist.toml")); err == nil {
		t.Fatal("Load of a missing file returned nil error")
	}
}

func TestDecodeUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want string // dotted path fragment the error must name
	}{
		{"top level", "schema_version = 1\nbogus = 1\n", "bogus"},
		{"nested table", "schema_version = 1\n[workspace]\nbogus = 1\n", "workspace.bogus"},
		{"member sub-table", "schema_version = 1\n[[members]]\nid = \"x\"\n[members.paths]\nbogus = 2\n", "members[0].paths.bogus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(tt.toml))
			if err == nil {
				t.Fatalf("Decode accepted unknown field; want error naming %q", tt.want)
			}
			if !errors.Is(err, ErrUnknownField) {
				t.Errorf("err = %v, want errors.Is ErrUnknownField", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to name %q", err, tt.want)
			}
		})
	}
}

func TestDecodeSchemaVersion(t *testing.T) {
	tests := []struct {
		name       string
		toml       string
		wantErr    error
		wantTooNew bool
	}{
		{"missing", "[workspace]\nid = \"x\"\n", ErrMissingSchemaVersion, false},
		{"explicit zero", "schema_version = 0\n", ErrUnsupportedSchema, false},
		{"too new", "schema_version = 2\n", ErrUnsupportedSchema, true},
		{"negative", "schema_version = -1\n", ErrUnsupportedSchema, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(tt.toml))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want errors.Is %v", err, tt.wantErr)
			}
			if got := errors.Is(err, schema.ErrTooNew); got != tt.wantTooNew {
				t.Errorf("errors.Is(schema.ErrTooNew) = %v, want %v", got, tt.wantTooNew)
			}
		})
	}
}

func TestDecodeTypeMismatch(t *testing.T) {
	// A shell-style command string must be rejected: command is argv only.
	const doc = `schema_version = 1
[[members]]
id = "x"
command_like = 1
`
	_, err := Decode(strings.NewReader(doc + ""))
	if err == nil {
		t.Fatal("Decode accepted garbage member")
	}
	// The real type-mismatch case: a check command written as a string.
	const cmdString = `schema_version = 1
[[members]]
id = "x"
[[members.verification]]
name = "n"
command = "go test ./..."
`
	_, err = Decode(strings.NewReader(cmdString))
	if !errors.Is(err, ErrInvalidTOML) {
		t.Fatalf("err = %v, want errors.Is ErrInvalidTOML", err)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	for _, name := range []string{"valid-full.toml", "valid-minimal.toml"} {
		t.Run(name, func(t *testing.T) {
			cfg := mustLoad(t, filepath.Join("testdata", name))
			b1, err := Marshal(cfg)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			cfg2, err := Decode(bytes.NewReader(b1))
			if err != nil {
				t.Fatalf("Decode(Marshal(cfg)): %v\ncanonical:\n%s", err, b1)
			}
			b2, err := Marshal(cfg2)
			if err != nil {
				t.Fatalf("Marshal(re-decoded): %v", err)
			}
			if !bytes.Equal(b1, b2) {
				t.Errorf("Marshal is not a fixed point:\n--- first ---\n%s\n--- second ---\n%s", b1, b2)
			}
			if !reflect.DeepEqual(cfg, cfg2) {
				t.Errorf("Decode(Marshal(Load)) != Load:\n%+v\n%+v", cfg, cfg2)
			}
			if err := Validate("", cfg2); err != nil {
				t.Errorf("Validate(re-decoded): %v", err)
			}
		})
	}
}
