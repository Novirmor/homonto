package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinalSkepticCommand(t *testing.T) {
	for _, tc := range []struct {
		value, want string
		invalid     bool
	}{
		{"", "one\n", false}, {"off", "off\n", false}, {"one", "one\n", false},
		{"two", "two\n", false}, {"auto", "auto\n", false}, {"invalid", "", true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "homonto.toml")
			body := "[workflow]\n"
			if tc.value != "" {
				body += "final_skeptic='" + tc.value + "'\n"
			}
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := NewRootCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"final-skeptic", "--config", path})
			err := cmd.Execute()
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "workflow.final_skeptic") {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || out.String() != tc.want {
				t.Fatalf("output %q error %v", out.String(), err)
			}
		})
	}
}

func TestFinalSkepticCommandEngineDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(path, []byte("[workflow]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for engine, want := range map[string]string{"onto": "two\n", "no": "two\n", "to": "one\n"} {
		cmd := NewRootCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"final-skeptic", engine, "--config", path})
		if err := cmd.Execute(); err != nil || out.String() != want {
			t.Fatalf("%s: got %q, err %v", engine, out.String(), err)
		}
	}
}
