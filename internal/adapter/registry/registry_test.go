package registry

import (
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
)

// TestBuiltins_BuildsTheBuiltinAdapters verifies the built-in registry builds
// exactly the registered built-ins in order — opencode (the default target)
// then claude (the opt-in target, ADR 0066) — and nothing else. The engine
// filters the built list down to the tools the config selects; the registry
// itself registers every built-in adapter.
func TestBuiltins_BuildsTheBuiltinAdapters(t *testing.T) {
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
