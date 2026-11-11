package config

import (
	"strings"
	"testing"
)

func TestFinalSkepticPolicy(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		invalid     bool
	}{
		{"", "one", false},
		{"final_skeptic='off'", "off", false},
		{"final_skeptic='one'", "one", false},
		{"final_skeptic='two'", "two", false},
		{"final_skeptic='auto'", "auto", false},
		{"final_skeptic=''", "", true},
		{"final_skeptic='always'", "", true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			c, err := Load(writeConfig(t, "[workflow]\n"+tc.input+"\n"))
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "workflow.final_skeptic") {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := c.Workflow.FinalSkepticOrDefault(); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFinalSkepticEngineDefaults(t *testing.T) {
	w := Workflow{}
	for engine, want := range map[string]string{"to": "one", "onto": "two", "no": "two"} {
		if got := w.FinalSkepticFor(engine); got != want {
			t.Errorf("%s: got %q, want %q", engine, got, want)
		}
	}
	v := "off"
	w.FinalSkeptic = &v
	for _, engine := range []string{"to", "onto", "no"} {
		if got := w.FinalSkepticFor(engine); got != "off" {
			t.Errorf("%s: got %q", engine, got)
		}
	}
}

func TestNoSkepticModelFallback(t *testing.T) {
	reviewer := ModelRoute{Model: "provider/reviewer", Variant: "high"}
	for _, tc := range []struct {
		skeptic        ModelRoute
		model, variant string
	}{
		{ModelRoute{}, "provider/reviewer", "high"},
		{ModelRoute{Variant: "low"}, "provider/reviewer", "low"},
		{ModelRoute{Model: "provider/other"}, "provider/other", ""},
	} {
		got := SkepticFallback(tc.skeptic, reviewer)
		if got.Model != tc.model || got.Variant != tc.variant {
			t.Errorf("got %+v", got)
		}
	}
}
