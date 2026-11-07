package gitenv

import (
	"slices"
	"strings"
	"testing"
)

func TestEnvDropsCallerGitOverrides(t *testing.T) {
	t.Setenv("GIT_DIR", "/elsewhere")
	t.Setenv("GIT_INDEX_FILE", "/elsewhere/index")
	t.Setenv("HOMONTO_GITENV_KEEP", "kept")
	env := Env()
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_") && entry != "GIT_OPTIONAL_LOCKS=0" && entry != "GIT_TERMINAL_PROMPT=0" {
			t.Errorf("caller override survived: %s", entry)
		}
	}
	for _, want := range []string{"HOMONTO_GITENV_KEEP=kept", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0"} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s", want)
		}
	}
}
