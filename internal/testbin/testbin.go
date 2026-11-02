// Package testbin builds the homonto binary for tests that drive the workflow
// plugin, which starts `homonto runtime serve` from PATH (ADR 0090).
package testbin

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	once sync.Once
	dir  string
	err  error
	out  []byte
)

// HomontoDir returns a directory holding a freshly built homonto binary. It
// builds once per test process.
func HomontoDir(t testing.TB) string {
	t.Helper()
	once.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		root := filepath.Join(filepath.Dir(file), "..", "..")
		dir, err = os.MkdirTemp("", "homonto-testbin-")
		if err != nil {
			return
		}
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "homonto"), ".")
		cmd.Dir = root
		out, err = cmd.CombinedOutput()
	})
	if err != nil {
		t.Fatalf("build homonto for runtime tests: %v\n%s", err, out)
	}
	return dir
}

// Env returns the process environment with the built homonto first on PATH.
func Env(t testing.TB) []string {
	return append(os.Environ(), "PATH="+HomontoDir(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
}
