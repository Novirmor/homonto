package agentfm

import (
	"strings"
	"testing"
)

// TestMalformedExternalIntent: a local:/remote: agent renders through the same
// path as a builtin, so malformed intent must fail loudly instead of
// projecting a weakened agent (ADR 0096).
func TestMalformedExternalIntent(t *testing.T) {
	ctx := &RenderContext{Overrides: map[string]ModelSpec{"external/mine": {Model: "m/x"}}, Names: map[string]string{"external/mine": "mine"}}
	for _, tc := range []struct{ name, content, want string }{
		{"unknown intent field", "---\nname: mine\nhomonto:\n  read_only: true\n  sandbox: strict\n---\nbody\n", "malformed homonto block"},
		{"intent is not a mapping", "---\nname: mine\nhomonto: true\n---\nbody\n", "malformed homonto block"},
		{"unterminated fence", "---\nname: mine\nhomonto:\n  read_only: true\nbody\n", "malformed frontmatter fence"},
		{"own permission map", "---\nname: mine\npermission:\n  edit: allow\nhomonto:\n  read_only: true\n---\nbody\n", "declares permission in its frontmatter"},
		{"own model and steps", "---\nname: mine\nmodel: a/b\nsteps: 3\nhomonto:\n  read_only: true\n---\nbody\n", "declares model, steps"},
		{"name conflicts with the installed alias", "---\nname: theirs\nhomonto:\n  read_only: true\n---\nbody\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Render("external/mine", []byte(tc.content), "opencode", ctx)
			if tc.want == "" {
				// The installed alias wins: the rendered name is the config key.
				if err != nil || !strings.Contains(string(out), `name: "mine"`) {
					t.Fatalf("alias render: %v\n%s", err, out)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestExternalRenderKeyGainsNoBuiltinRole: the render identity of an external
// agent never matches a builtin role, so it gets no coordinator tools even when
// its own frontmatter claims a builtin's name.
func TestExternalRenderKeyGainsNoBuiltinRole(t *testing.T) {
	ctx := &RenderContext{Overrides: map[string]ModelSpec{"external/homonto": {Model: "m/x"}}, Names: map[string]string{"external/homonto": "homonto"}}
	out, err := Render("external/homonto", []byte("---\nname: homonto\nhomonto:\n  primary: true\n  bash_default: allow\n---\nbody\n"), "opencode", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "homonto_status") {
		t.Fatalf("external agent gained coordinator tools:\n%s", out)
	}
}
