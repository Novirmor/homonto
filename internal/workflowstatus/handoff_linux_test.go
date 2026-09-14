package workflowstatus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReadHandoffRejectsFIFOWithoutOpening(t *testing.T) {
	for _, name := range []string{"plan.md", "to-state.yaml"} {
		t.Run(name, func(t *testing.T) {
			root, dir := handoffFixture(t, "to")
			path := filepath.Join(root, dir, name)
			if name == "to-state.yaml" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := ReadHandoff(filepath.Join(root, "selected.toml"), "to", "demo", "generation")
			if name == "to-state.yaml" {
				if err == nil {
					t.Fatal("selected FIFO state")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(got)
			if !strings.Contains(string(b), "nonregular") || got.Artifacts[0].Text != "" {
				t.Fatalf("FIFO artifact = %s", b)
			}
		})
	}
}
