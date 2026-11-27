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

func TestFinalSkepticEngineOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, input   string
		want          map[string]string
		errorContains string
	}{
		{"independent", "[workflow.final_skeptic_by_engine]\nno='off'\nto='one'\nonto='two'\n", map[string]string{"no": "off", "to": "one", "onto": "two"}, ""},
		{"fallback to shared", "[workflow]\nfinal_skeptic='auto'\n[workflow.final_skeptic_by_engine]\nno='off'\n", map[string]string{"no": "off", "to": "auto", "onto": "auto"}, ""},
		{"fallback to defaults", "[workflow.final_skeptic_by_engine]\nto='two'\n", map[string]string{"no": "two", "to": "two", "onto": "two"}, ""},
		{"invalid override", "[workflow.final_skeptic_by_engine]\nno=''\n", nil, "workflow.final_skeptic_by_engine.no"},
		{"invalid shared even when overridden", "[workflow]\nfinal_skeptic='bad'\n[workflow.final_skeptic_by_engine]\nto='one'\nonto='two'\nno='off'\n", nil, "workflow.final_skeptic"},
		{"unknown engine", "[workflow.final_skeptic_by_engine]\ntypo='one'\n", nil, "typo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Load(writeConfig(t, tc.input))
			if tc.errorContains != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errorContains) {
					t.Fatalf("error = %v, want %q", err, tc.errorContains)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for engine, want := range tc.want {
				if got := c.Workflow.FinalSkepticFor(engine); got != want {
					t.Errorf("%s: got %q, want %q", engine, got, want)
				}
			}
		})
	}
}

func TestFinalSkepticOverrideValues(t *testing.T) {
	for _, engine := range []string{"to", "onto", "no"} {
		for _, value := range []string{"off", "one", "two", "auto", "invalid"} {
			t.Run(engine+"/"+value, func(t *testing.T) {
				c, err := Load(writeConfig(t, "[workflow.final_skeptic_by_engine]\n"+engine+"='"+value+"'\n"))
				if value == "invalid" {
					if err == nil || !strings.Contains(err.Error(), "workflow.final_skeptic_by_engine."+engine) {
						t.Fatalf("error = %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if got := c.Workflow.FinalSkepticFor(engine); got != value {
					t.Fatalf("got %q, want %q", got, value)
				}
			})
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
