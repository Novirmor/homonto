package registry

import (
	"testing"

	"github.com/Novirmor/homonto/internal/adapter"
)

// TestBuiltins_BuildsTheHarnessAdapters verifies the built-in registry builds
// exactly the registered built-ins in order: OpenCode first, then Claude Code
// (M4.4); no removed adapter (codex) quietly re-registers.
func TestBuiltins_BuildsTheHarnessAdapters(t *testing.T) {
	adapters := Builtins().Build(Deps{Home: "/home/u", ContentDir: "/repo/content"})
	got := make([]string, len(adapters))
	for i, a := range adapters {
		got[i] = a.Name()
	}
	want := []string{"opencode", "claude"}
	if len(got) != len(want) {
		t.Fatalf("built %d adapters %v, want %v", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("adapter[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRegister_PanicsOnDuplicateID(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("Register of a duplicate id should panic")
		}
	}()
	r := New()
	r.Register("opencode", func(Deps) adapter.Adapter { return nil })
	r.Register("opencode", func(Deps) adapter.Adapter { return nil })
}
