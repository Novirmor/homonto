package ontocli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

func TestScaleSetRefusesTerminalAndMismatchedState(t *testing.T) {
	for _, kind := range []string{"abandoned", "archived", "identity-mismatch"} {
		t.Run(kind, func(t *testing.T) {
			root := prepWorkspace(t)
			base, err := resolveCommit(root, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			st := ontostate.State{Change: "c", Phase: "build", Workflow: "full", BaseRef: base}
			want := "terminal"
			switch kind {
			case "abandoned":
				st.Abandoned = true
			case "archived":
				st.Archived = true
			default:
				st.Change, want = "different", "does not match"
			}
			path := filepath.Join(root, "docs", "changes", "c", "onto-state.yaml")
			if err := ontostate.Save(path, st); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runOnto(t, "scale", "c", "--set", "--dir", root); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("scale --set accepted %s or gave wrong error: %v", kind, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatalf("refused scale changed state: %v", err)
			}
			// Measuring a terminal record is read-only and remains supported.
			_, err = runOnto(t, "scale", "c", "--dir", root)
			if kind == "identity-mismatch" {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("read accepted mismatched identity: %v", err)
				}
			} else if err != nil {
				t.Fatalf("read-only terminal measurement: %v", err)
			}
		})
	}
}

func TestScaleSetJSONRecordsMeasurement(t *testing.T) {
	root := prepWorkspace(t)
	base, err := resolveCommit(root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "docs", "changes", "c", "onto-state.yaml")
	if err := ontostate.Save(path, ontostate.State{Change: "c", Phase: "build", Workflow: "light", BaseRef: base}); err != nil {
		t.Fatal(err)
	}
	out, err := runOnto(t, "scale", "c", "--set", "--json", "--dir", root)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Level    string `json:"level"`
		Recorded bool   `json:"recorded"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil || result.Level != "light" || !result.Recorded {
		t.Fatalf("measurement JSON = %q: %v", out, err)
	}
	st, err := ontostate.Load(path)
	if err != nil || st.Verify.Scale != result.Level {
		t.Fatalf("recorded measurement = %+v: %v", st, err)
	}
}

func TestIsTestPath(t *testing.T) {
	tests := map[string]bool{
		"internal/x/foo.go":      false,
		"internal/x/foo_test.go": true,
		"src/app.spec.ts":        true,
		"src/app.test.js":        true,
		"test/docker/run.sh":     true,
		"tests/e2e/a.py":         true,
		"test_helpers.py":        true,
		"cmd/main.go":            false,
	}
	for path, want := range tests {
		if got := isTestPath(path); got != want {
			t.Errorf("isTestPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestDiffScale_SizeDerivesLevel(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init")
	gitRun(t, dir, "config", "user.email", "d@e.com")
	gitRun(t, dir, "config", "user.name", "d")
	os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "base")
	base := func() string {
		out, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		return string(out[:len(out)-1])
	}()

	// A small change → light.
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one line\n"), 0o644)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "small")
	if files, _, level, err := diffScale(dir, base); err != nil || level != "light" || files != 1 {
		t.Fatalf("small diff = (files=%d level=%s err=%v), want (1, light, nil)", files, level, err)
	}

	// Many files → full; a _test.go file does not count toward the file gate.
	for i := 0; i < 8; i++ {
		os.WriteFile(filepath.Join(dir, "f"+string(rune('a'+i))+".txt"), []byte("x\n"), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "z_test.go"), []byte("package z\n"), 0o644)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "big")
	files, _, level, err := diffScale(dir, base)
	if err != nil || level != "full" {
		t.Fatalf("big diff level = %s (err %v), want full", level, err)
	}
	if files != 9 { // a.txt + 8 f*.txt; z_test.go excluded
		t.Errorf("non-test file count = %d, want 9 (test file excluded)", files)
	}
}

func TestSetVerifyResultFail_IncrementsRounds(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "verify")
	for i := 0; i < 2; i++ {
		if _, err := runOnto(t, "set", "verify-result", "c", "fail", "--dir", root); err != nil {
			t.Fatalf("set verify-result fail: %v", err)
		}
	}
	// pass must NOT increment.
	if _, err := runOnto(t, "set", "verify-result", "c", "pass", "--dir", root); err != nil {
		t.Fatal(err)
	}
	st, err := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Observed.VerifyRounds != 2 {
		t.Errorf("verify_rounds = %d, want 2 (each fail increments, pass does not)", st.Observed.VerifyRounds)
	}
}

func TestSetBuildPause_SetAndClear(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "build")
	if _, err := runOnto(t, "set", "build-pause", "c", "plan-ready", "--dir", root); err != nil {
		t.Fatal(err)
	}
	load := func() ontostate.State {
		st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
		return st
	}
	if load().BuildPause != "plan-ready" {
		t.Fatal("build-pause not set")
	}
	if _, err := runOnto(t, "set", "build-pause", "c", "clear", "--dir", root); err != nil {
		t.Fatal(err)
	}
	if load().BuildPause != "" {
		t.Error("build-pause clear did not empty the field")
	}
}

func TestScaleSetWaitsForOntoWorkspaceLock(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "build")
	unlock, err := lockOnto(root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	_, err = runOnto(t, "scale", "c", "--set", "--dir", root)
	if err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("scale --set while locked = %v, want lock error", err)
	}
}
