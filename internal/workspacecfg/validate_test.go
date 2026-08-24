package workspacecfg

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// validCfg mirrors testdata/valid-full.toml as an in-code value for
// mutation-based validation tests.
func validCfg() Config {
	return Config{
		SchemaVersion: 1,
		Workspace:     Workspace{ID: testWorkspaceID, Workflow: WorkflowTask},
		Control:       Control{ID: testControlID, Path: ".", Remotes: []string{"git@example.com:acme/control.git"}},
		Members: []Member{
			{
				ID: testControlID, Path: ".", Kind: KindGit,
				Remotes: []string{"git@example.com:acme/control.git"},
				Verification: []Check{
					{Name: "lint", Command: []string{"golangci-lint", "run"}, WorkingDir: "cmd/onto", Environment: []string{"CI", "GOFLAGS"}, Timeout: "10m"},
					{Name: "unit", Command: []string{"go", "test", "./..."}, WorkingDir: ".", Timeout: "5m"},
				},
				Paths: &PathClasses{
					Tests:     []string{"**/*_test.go"},
					Generated: []string{"**/zz_generated*.go"},
					Vendored:  []string{"vendor/**"},
				},
			},
			{
				ID: testMemberAPIID, Path: "services/api", Kind: KindGit,
				Remotes:      []string{"git@example.com:acme/api.git"},
				Verification: []Check{{Name: "test", Command: []string{"make", "test"}, WorkingDir: ".", Timeout: "10m"}},
			},
			{ID: testMemberDocsID, Path: "docs/notes", Kind: KindNonGit},
		},
		Routes: Routes{
			Claude: &ToolRoutes{
				Explorer:    &RoleRoute{Model: "claude-opus-4-5", Effort: "high"},
				Implementer: &RoleRoute{Model: "claude-sonnet-4-5"},
			},
			OpenCode: &ToolRoutes{Skeptic: &RoleRoute{Model: "g-5", Variant: "plan"}},
		},
		Integrations: Integrations{CommitGenerated: false},
		Update:       Update{Channel: ChannelStable},
	}
}

func TestValidateAccepts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"full config", nil},
		{"with workspace root", nil}, // same config, validated against a root below
		{"boundary timeout 1s", func(c *Config) { c.Members[0].Verification[0].Timeout = "1s" }},
		{"boundary timeout 24h", func(c *Config) { c.Members[0].Verification[0].Timeout = "24h" }},
		{"absent timeout means default", func(c *Config) { c.Members[0].Verification[0].Timeout = "" }},
		{"absent working_dir means default", func(c *Config) { c.Members[0].Verification[0].WorkingDir = "" }},
		{"unset update channel", func(c *Config) { c.Update.Channel = "" }},
		{"control is a member at .", func(c *Config) { c.Members[0].Path = "." }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validCfg()
			if tt.mutate != nil {
				tt.mutate(&cfg)
			}
			if err := Validate("", cfg); err != nil {
				t.Errorf("Validate: %v", err)
			}
			root := filepath.Join(t.TempDir(), "ws")
			if err := Validate(root, cfg); err != nil {
				t.Errorf("Validate(root): %v", err)
			}
		})
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Config)
		match    string // substring of the error message
		sentinel error  // optional errors.Is target
	}{
		{"schema zero treated as missing", func(c *Config) { c.SchemaVersion = 0 }, "schema_version", ErrMissingSchemaVersion},
		{"schema too new", func(c *Config) { c.SchemaVersion = 2 }, "schema_version", ErrUnsupportedSchema},
		{"workspace id not a uuid", func(c *Config) { c.Workspace.ID = "not-a-uuid" }, "workspace.id", nil},
		{"workflow missing", func(c *Config) { c.Workspace.Workflow = "" }, "workflow", nil},
		{"workflow unknown", func(c *Config) { c.Workspace.Workflow = "sprint" }, "workflow", nil},
		{"control id not a uuid", func(c *Config) { c.Control.ID = "nope" }, "control.id", nil},
		{"control path empty", func(c *Config) { c.Control.Path = "" }, "control.path", ErrInvalidPath},
		{"control path absolute", func(c *Config) { c.Control.Path = "/ws" }, "control.path", ErrInvalidPath},
		{"control path parent", func(c *Config) { c.Control.Path = ".." }, "control.path", ErrInvalidPath},
		{"control path unclean double slash", func(c *Config) { c.Control.Path = "a//b" }, "control.path", ErrInvalidPath},
		{"control path unclean dot segment", func(c *Config) { c.Control.Path = "a/./b" }, "control.path", ErrInvalidPath},
		{"control path backslash", func(c *Config) { c.Control.Path = `a\b` }, "control.path", ErrInvalidPath},
		{"control remote empty", func(c *Config) { c.Control.Remotes = []string{""} }, "remote", nil},
		{"member id not a uuid", func(c *Config) { c.Members[1].ID = "nope" }, "members[1].id", nil},
		{"member id duplicate", func(c *Config) { c.Members[1].ID = c.Members[2].ID }, "duplicate", ErrDuplicateMemberID},
		{"member path empty", func(c *Config) { c.Members[1].Path = "" }, "members[1].path", ErrInvalidPath},
		{"member path dot when not control", func(c *Config) { c.Members[1].Path = "." }, "members[1].path", ErrInvalidPath},
		{"member path absolute", func(c *Config) { c.Members[1].Path = "/etc" }, "members[1].path", ErrInvalidPath},
		{"member path parent prefix", func(c *Config) { c.Members[1].Path = "../api" }, "members[1].path", ErrInvalidPath},
		{"member path inner parent", func(c *Config) { c.Members[1].Path = "a/../b" }, "members[1].path", ErrInvalidPath},
		{"member path trailing slash", func(c *Config) { c.Members[1].Path = "services/api/" }, "members[1].path", ErrInvalidPath},
		{"member path duplicate", func(c *Config) { c.Members[1].Path = c.Members[2].Path }, "duplicate", ErrDuplicateMemberPath},
		{"member kind missing", func(c *Config) { c.Members[1].Kind = "" }, "kind", nil},
		{"member kind unknown", func(c *Config) { c.Members[1].Kind = "svn" }, "kind", nil},
		{"member remote empty", func(c *Config) { c.Members[1].Remotes = []string{" "} }, "remote", nil},
		{"check name empty", func(c *Config) { c.Members[0].Verification[0].Name = "" }, "name", nil},
		{"check name duplicate", func(c *Config) { c.Members[0].Verification[1].Name = c.Members[0].Verification[0].Name }, "duplicate", nil},
		{"check command empty", func(c *Config) { c.Members[0].Verification[0].Command = []string{} }, "command", nil},
		{"check command empty element", func(c *Config) { c.Members[0].Verification[0].Command = []string{"", "x"} }, "command", nil},
		{"check working dir absolute", func(c *Config) { c.Members[0].Verification[0].WorkingDir = "/abs" }, "working_dir", ErrInvalidPath},
		{"check working dir parent", func(c *Config) { c.Members[0].Verification[0].WorkingDir = ".." }, "working_dir", ErrInvalidPath},
		{"check env empty", func(c *Config) { c.Members[0].Verification[0].Environment = []string{""} }, "environment", ErrInvalidEnvName},
		{"check env leading digit", func(c *Config) { c.Members[0].Verification[0].Environment = []string{"1BAD"} }, "environment", ErrInvalidEnvName},
		{"check env hyphen", func(c *Config) { c.Members[0].Verification[0].Environment = []string{"BAD-NAME"} }, "environment", ErrInvalidEnvName},
		{"check env space", func(c *Config) { c.Members[0].Verification[0].Environment = []string{"WITH SPACE"} }, "environment", ErrInvalidEnvName},
		{"check timeout unparseable", func(c *Config) { c.Members[0].Verification[0].Timeout = "fast" }, "timeout", ErrInvalidTimeout},
		{"check timeout sub-second", func(c *Config) { c.Members[0].Verification[0].Timeout = "500ms" }, "timeout", ErrInvalidTimeout},
		{"check timeout zero", func(c *Config) { c.Members[0].Verification[0].Timeout = "0s" }, "timeout", ErrInvalidTimeout},
		{"check timeout negative", func(c *Config) { c.Members[0].Verification[0].Timeout = "-1s" }, "timeout", ErrInvalidTimeout},
		{"check timeout over 24h", func(c *Config) { c.Members[0].Verification[0].Timeout = "24h1s" }, "timeout", ErrInvalidTimeout},
		{"glob empty", func(c *Config) { c.Members[0].Paths.Tests = []string{""} }, "tests[0]", ErrInvalidGlob},
		{"glob absolute", func(c *Config) { c.Members[0].Paths.Tests = []string{"/etc/**"} }, "tests[0]", ErrInvalidGlob},
		{"glob parent segment", func(c *Config) { c.Members[0].Paths.Tests = []string{"a/../b"} }, "tests[0]", ErrInvalidGlob},
		{"glob backslash", func(c *Config) { c.Members[0].Paths.Tests = []string{`a\b`} }, "tests[0]", ErrInvalidGlob},
		{"glob generated invalid", func(c *Config) { c.Members[0].Paths.Generated = []string{"../gen/**"} }, "generated[0]", ErrInvalidGlob},
		{"route without model", func(c *Config) { c.Routes.Claude.Explorer.Model = "" }, "routes.claude.explorer.model", nil},
		{"route effort only", func(c *Config) { c.Routes.OpenCode.Skeptic = &RoleRoute{Effort: "high"} }, "routes.opencode.skeptic.model", nil},
		{"update channel unknown", func(c *Config) { c.Update.Channel = "beta" }, "channel", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validCfg()
			tt.mutate(&cfg)
			err := Validate("", cfg)
			if err == nil {
				t.Fatalf("Validate accepted invalid config (%s)", tt.name)
			}
			if !strings.Contains(err.Error(), tt.match) {
				t.Errorf("err = %v, want message containing %q", err, tt.match)
			}
			if tt.sentinel != nil && !errors.Is(err, tt.sentinel) {
				t.Errorf("err = %v, want errors.Is %v", err, tt.sentinel)
			}
		})
	}
}
