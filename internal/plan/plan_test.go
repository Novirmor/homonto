package plan

import (
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
)

func TestRenderShowsChangesNotNoops(t *testing.T) {
	sets := []adapter.ChangeSet{{
		Tool: "claude",
		Changes: []adapter.Change{
			{Action: "update", Key: "settings.model", Old: `"sonnet"`, New: `"opus"`},
			{Action: "create", Key: "mcp.brave", New: `{"command":["npx"]}`},
			{Action: "noop", Key: "mcp.codegraph"},
		},
	}}
	out := Render(sets)
	if !strings.Contains(out, "~ settings.model") || !strings.Contains(out, `"sonnet" -> "opus"`) {
		t.Fatalf("update line missing:\n%s", out)
	}
	if !strings.Contains(out, "+ mcp.brave") {
		t.Fatalf("create line missing:\n%s", out)
	}
	if strings.Contains(out, "codegraph") {
		t.Fatalf("noop should be hidden:\n%s", out)
	}
	if !HasChanges(sets) {
		t.Fatal("HasChanges should be true")
	}
}

func TestRenderNeverResolvesSecrets(t *testing.T) {
	sets := []adapter.ChangeSet{{Tool: "claude", Changes: []adapter.Change{
		{Action: "create", Key: "mcp.brave.env", New: `{"BRAVE_API_KEY":"${pass:ai/brave}"}`},
	}}}
	if !strings.Contains(Render(sets), "${pass:ai/brave}") {
		t.Fatal("plan must show the unresolved token verbatim")
	}
}

func TestRenderShowsDeletesAndHasChangesCountsThem(t *testing.T) {
	sets := []adapter.ChangeSet{{Tool: "claude", Changes: []adapter.Change{
		{Action: "delete", Key: "mcp.brave", Old: adapter.SecretRedaction},
	}}}
	if out := Render(sets); !strings.Contains(out, "- mcp.brave") {
		t.Fatalf("delete line missing:\n%s", out)
	}
	if !HasChanges(sets) {
		t.Fatal("HasChanges must be true for a delete-only set")
	}
}

func TestHasChangesFalseWhenAllNoop(t *testing.T) {
	sets := []adapter.ChangeSet{{Tool: "claude", Changes: []adapter.Change{{Action: "noop", Key: "x"}}}}
	if HasChanges(sets) {
		t.Fatal("expected no changes")
	}
}

func TestHasAdoptionsTrueWhenAdoptPresent(t *testing.T) {
	sets := []adapter.ChangeSet{{Tool: "claude", Changes: []adapter.Change{
		{Action: "noop", Key: "x"},
		{Action: "adopt", Key: "mcp.brave"},
	}}}
	if !HasAdoptions(sets) {
		t.Fatal("HasAdoptions should be true when an adopt change is present")
	}
}

func TestHasAdoptionsFalseWithoutAdopt(t *testing.T) {
	sets := []adapter.ChangeSet{{Tool: "claude", Changes: []adapter.Change{
		{Action: "noop", Key: "x"},
		{Action: "create", Key: "mcp.brave", New: `{"command":["npx"]}`},
	}}}
	if HasAdoptions(sets) {
		t.Fatal("HasAdoptions should be false when no adopt change is present")
	}
}

func TestHasChangesFalseForAdoptOnly(t *testing.T) {
	sets := []adapter.ChangeSet{{Tool: "claude", Changes: []adapter.Change{
		{Action: "noop", Key: "x"},
		{Action: "adopt", Key: "mcp.brave", New: `{"command":["npx"]}`},
	}}}
	if HasChanges(sets) {
		t.Fatal("HasChanges must be false for an adopt-only set (adopt is state-only, renders nothing)")
	}
}

func TestHasChangesTrueForEachVisibleAction(t *testing.T) {
	for _, action := range []adapter.Action{"create", "update", "delete"} {
		sets := []adapter.ChangeSet{{Tool: "claude", Changes: []adapter.Change{
			{Action: "adopt", Key: "mcp.brave"},
			{Action: action, Key: "mcp.other"},
		}}}
		if !HasChanges(sets) {
			t.Fatalf("HasChanges must be true when a %q change is present", action)
		}
	}
}

func TestRenderAdoptProducesNoLine(t *testing.T) {
	sets := []adapter.ChangeSet{{Tool: "claude", Changes: []adapter.Change{
		{Action: "adopt", Key: "mcp.brave"},
	}}}
	if out := Render(sets); out != "" {
		t.Fatalf("adopt change must render nothing, got:\n%s", out)
	}
}

// TestRenderMarksReleases: a release is not a deletion — the entry stays on
// disk and becomes the user's (ADR 0085), so plan must not show it as "-".
func TestRenderMarksReleases(t *testing.T) {
	out := Render([]adapter.ChangeSet{{Tool: "opencode", Changes: []adapter.Change{
		{Action: "delete", Key: "projmcp.codegraph", Cause: adapter.CauseRelease},
		{Action: "delete", Key: "skill.gone", Cause: adapter.CauseRemove},
	}}})
	if !strings.Contains(out, "  > projmcp.codegraph (released; left in place, now yours)") {
		t.Fatalf("release not marked:\n%s", out)
	}
	if strings.Contains(out, "- projmcp.codegraph") || !strings.Contains(out, "  - skill.gone") {
		t.Fatalf("release rendered as a delete, or delete lost:\n%s", out)
	}
	if !HasChanges([]adapter.ChangeSet{{Changes: []adapter.Change{{Action: "delete", Key: "k", Cause: adapter.CauseRelease}}}}) {
		t.Fatal("a pending release must still count as a change")
	}
}
