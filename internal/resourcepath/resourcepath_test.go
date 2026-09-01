package resourcepath

import "testing"

func TestDir(t *testing.T) {
	cases := []struct {
		name          string
		kind          Kind
		tool, scope   string
		home, project string
		want          string
	}{
		{"opencode skill user", Skill, "opencode", "user", "/h", "/p", "/h/.config/opencode/skills"},
		{"opencode skill project", Skill, "opencode", "project", "/h", "/p", "/p/.opencode/skills"},

		{"opencode command user", Command, "opencode", "user", "/h", "/p", "/h/.config/opencode/command"},
		{"opencode command project", Command, "opencode", "project", "/h", "/p", "/p/.opencode/command"},

		{"opencode subagent user", Subagent, "opencode", "user", "/h", "/p", "/h/.config/opencode/agent"},
		{"opencode subagent project", Subagent, "opencode", "project", "/h", "/p", "/p/.opencode/agent"},

		// unknown kind / tool
		{"unknown kind", Kind("nope"), "opencode", "user", "/h", "/p", ""},
		{"unknown tool", Skill, "vscode", "user", "/h", "/p", ""},
		{"subagent unknown tool", Subagent, "vscode", "user", "/h", "/p", ""},

		// Any scope other than "project" is treated as "user".
		{"empty scope falls back to user", Skill, "opencode", "", "/h", "/p", "/h/.config/opencode/skills"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Dir(tc.kind, tc.tool, tc.scope, tc.home, tc.project)
			if got != tc.want {
				t.Fatalf("Dir(%s, %q, %q, %q, %q) = %q, want %q", tc.kind, tc.tool, tc.scope, tc.home, tc.project, got, tc.want)
			}
		})
	}
}

func TestOtherScope(t *testing.T) {
	cases := []struct{ in, want string }{
		{"project", "user"},
		{"user", "project"},
		{"", "project"},
		{"bogus", "project"},
	}
	for _, tc := range cases {
		if got := OtherScope(tc.in); got != tc.want {
			t.Errorf("OtherScope(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
