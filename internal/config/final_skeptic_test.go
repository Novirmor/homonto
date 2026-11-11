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
