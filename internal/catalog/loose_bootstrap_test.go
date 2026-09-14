package catalog

import (
	"strings"
	"testing"
)

// These checks cover shipped instructions, not host enforcement or agent compliance.
func TestLooseBootstrapDispatchers(t *testing.T) {
	for _, name := range []string{"onto", "to"} {
		t.Run(name, func(t *testing.T) {
			text := hPromptText(t, "skills/"+name+"/SKILL.md")
			for _, want := range []string{
				"Run `" + name + " version`",
				"missing, broken, or incompatible",
				"inspect PATH and known installed compatible binaries first",
				"[bootstrap policy](../homonto/references/autonomy.md#root-and-bootstrap)",
				"installation/build is already authorized, repair in-scope setup",
				"trusted source and compatible version at an inspected workspace-local destination",
				"Never silently overwrite global binaries, edit shell profiles, or install from an untrusted arbitrary source",
				"binary path, command, exit status, and error output",
				"ask for the specific setup decision needed when scope or authority is missing",
				"Re-run version checks and verify the framework-install gate at configRoot",
				"No workflow state mutations until both pass",
				"never use handwritten bookkeeping or fabricated installation directories as a fallback",
			} {
				if !strings.Contains(text, want) {
					t.Errorf("bootstrap contract missing %q", want)
				}
			}
			for _, stale := range []string{
				"On failure, STOP", "Tell the user to install/build it",
				"go build ./cmd/" + name, "warns, never halts",
			} {
				if strings.Contains(text, stale) {
					t.Errorf("obsolete bootstrap instruction retained: %q", stale)
				}
			}
		})
	}
}

func TestLooseBootstrapSharedSafety(t *testing.T) {
	text := hPromptText(t, "skills/homonto/references/autonomy.md")
	for _, want := range []string{
		"only scaffolds `homonto.toml`, `.gitignore`, and `.env.example`",
		"Create local skill directories only when adding explicitly declared local skills",
		"missing, broken, or incompatible, inspect PATH and known installed compatible binaries first",
		"distinguish command-not-found from an executable that fails",
		"explicit path or session-local PATH, not an unrelated same-name tool",
		"When installation/build is already authorized, repair in-scope setup",
		"workspace-local destination inside the existing write scope and directory grants",
		"Verify source provenance and the selected release/checksum or checkout revision",
		"Never silently overwrite global binaries, edit shell profiles, or install from an untrusted arbitrary source",
		"report the factual setup blocker and ask for the specific setup decision needed",
		"Implementers return such decisions to the coordinator and cannot perform its denied CLI calls",
		"declared and materialized by homonto",
		"No workflow state mutations until version checks and the framework-install gate pass",
		"Never fabricate installed catalog directories or use handwritten bookkeeping as a fallback",
		"Optional provider warnings remain non-blocking and do not authorize installing those providers",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("shared bootstrap contract missing %q", want)
		}
	}
}

func TestLooseShellPublicationRoleText(t *testing.T) {
	for _, file := range []string{
		"skills/homonto/references/autonomy.md",
		"skills/homonto/references/publication.md",
		"skills/h-resolve-issue/references/autonomy.md",
	} {
		t.Run(file, func(t *testing.T) {
			text := hPromptText(t, file)
			for _, want := range []string{
				"`git push`, GitHub publication, and raw `gh api` patterns ask for the coordinator but are denied for implementers",
				"The coordinator auto-allows local Git operations; `git push` still asks",
				"Implementers retain prompts for destructive commands",
				"A tool prompt cannot override role ownership or publication approval",
			} {
				if !strings.Contains(text, want) {
					t.Errorf("role-specific shell policy missing %q", want)
				}
			}
		})
	}
}
