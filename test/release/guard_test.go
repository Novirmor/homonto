package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The guard exists because of a specific failure: a release that publishes
// while one of the things it claims was proven was never run. Every case
// here is that failure, one required piece at a time.
//
// It matters most for the cells nobody can run on the machine doing the
// release — a macOS host run, a live OpenCode session. Those are exactly
// the ones a hurried person marks as "fine" and moves on. The guard makes
// absent evidence stop the release rather than pass quietly.

// evidence writes a complete evidence directory and returns its path. Each
// test then removes precisely one thing.
func evidence(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "host-cells.tsv"), strings.Join([]string{
		"claude\tlinux\tpass",
		"claude\tdarwin\tpass",
		"opencode\tlinux\tpass",
		"opencode\tdarwin\tpass",
	}, "\n")+"\n")
	write(t, filepath.Join(dir, "migration-rehearsal.txt"), "migrated v7 -> v8, verified\n")
	write(t, filepath.Join(dir, "rollback-rehearsal.txt"), "activation failed, rolled back to v0.9.0\n")
	return dir
}

// write creates a file with content.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// guard runs the release guard and returns its output and whether it
// allowed the release.
func guard(t *testing.T, evidenceDir string, env map[string]string) (string, bool) {
	t.Helper()
	cmd := exec.Command(script(t, "release-guard.sh"), "--evidence", evidenceDir)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"HOMONTO_RELEASE_SIGNING_KEY=", "HOMONTO_RELEASE_ROOT_ID=", "GH_TOKEN=")
	for name, value := range env {
		cmd.Env = append(cmd.Env, name+"="+value)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// complete is the environment of a release that is genuinely ready.
func complete(t *testing.T) map[string]string {
	t.Helper()
	key := filepath.Join(t.TempDir(), "root.key")
	write(t, key, "not-a-real-key\n")
	return map[string]string{
		"HOMONTO_RELEASE_SIGNING_KEY": key,
		"HOMONTO_RELEASE_ROOT_ID":     "homonto-release-1",
		"GH_TOKEN":                    "token",
	}
}

// TestGuardAllowsACompleteRelease establishes that the guard is passable —
// otherwise every refusal below proves nothing.
func TestGuardAllowsACompleteRelease(t *testing.T) {
	out, allowed := guard(t, evidence(t), complete(t))
	if !allowed {
		t.Fatalf("the guard refused a complete release:\n%s", out)
	}
}

// TestGuardRefusesWithoutSigningKey: an unsigned release is one no binary
// carrying a root will install.
func TestGuardRefusesWithoutSigningKey(t *testing.T) {
	env := complete(t)
	env["HOMONTO_RELEASE_SIGNING_KEY"] = ""
	out, allowed := guard(t, evidence(t), env)
	assertRefused(t, out, allowed)
}

// TestGuardRefusesWhenTheSigningKeyIsAbsent: a path that names nothing is
// the same as no key, and fails later and less clearly.
func TestGuardRefusesWhenTheSigningKeyIsAbsent(t *testing.T) {
	env := complete(t)
	env["HOMONTO_RELEASE_SIGNING_KEY"] = filepath.Join(t.TempDir(), "missing.key")
	out, allowed := guard(t, evidence(t), env)
	assertRefused(t, out, allowed)
}

// TestGuardRefusesWithoutRootID: signing as an unnamed root produces a
// signature no store maps to a key.
func TestGuardRefusesWithoutRootID(t *testing.T) {
	env := complete(t)
	env["HOMONTO_RELEASE_ROOT_ID"] = ""
	out, allowed := guard(t, evidence(t), env)
	assertRefused(t, out, allowed)
}

// TestGuardRefusesWithoutPublishCredentials.
func TestGuardRefusesWithoutCredentials(t *testing.T) {
	env := complete(t)
	env["GH_TOKEN"] = ""
	out, allowed := guard(t, evidence(t), env)
	assertRefused(t, out, allowed)
}

// TestGuardRefusesWithoutHostEvidence: the acceptance matrix is the whole
// argument that the workflows work against real agents.
func TestGuardRefusesWithoutHostEvidence(t *testing.T) {
	dir := evidence(t)
	os.Remove(filepath.Join(dir, "host-cells.tsv"))
	out, allowed := guard(t, dir, complete(t))
	assertRefused(t, out, allowed)
}

// TestGuardRefusesAMissingHostCell is the case that motivated the guard: a
// matrix with a hole in it looks like a matrix.
func TestGuardRefusesAMissingHostCell(t *testing.T) {
	for _, missing := range []string{
		"claude\tdarwin\tpass",
		"opencode\tdarwin\tpass",
		"opencode\tlinux\tpass",
		"claude\tlinux\tpass",
	} {
		dir := evidence(t)
		path := filepath.Join(dir, "host-cells.tsv")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		write(t, path, strings.ReplaceAll(string(body), missing+"\n", ""))
		out, allowed := guard(t, dir, complete(t))
		if allowed {
			t.Errorf("the guard published with %q missing:\n%s", missing, out)
		}
	}
}

// TestGuardRefusesASkippedHostCell: "skip" is how a cell that was never
// run gets recorded, and it must not read as a pass.
func TestGuardRefusesASkippedHostCell(t *testing.T) {
	for _, result := range []string{"skip", "fail", "not run", ""} {
		dir := evidence(t)
		path := filepath.Join(dir, "host-cells.tsv")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		write(t, path, strings.Replace(string(body),
			"opencode\tdarwin\tpass", "opencode\tdarwin\t"+result, 1))
		out, allowed := guard(t, dir, complete(t))
		if allowed {
			t.Errorf("the guard published with a %q cell:\n%s", result, out)
		}
	}
}

// TestGuardRefusesWithoutMigrationRehearsal: a schema step nobody
// rehearsed is discovered by the first user who updates.
func TestGuardRefusesWithoutMigrationRehearsal(t *testing.T) {
	dir := evidence(t)
	os.Remove(filepath.Join(dir, "migration-rehearsal.txt"))
	out, allowed := guard(t, dir, complete(t))
	assertRefused(t, out, allowed)
}

// TestGuardRefusesWithoutRollbackRehearsal: the rollback path only ever
// runs when something has already gone wrong, so it is the one that must
// be exercised deliberately.
func TestGuardRefusesWithoutRollbackRehearsal(t *testing.T) {
	dir := evidence(t)
	os.Remove(filepath.Join(dir, "rollback-rehearsal.txt"))
	out, allowed := guard(t, dir, complete(t))
	assertRefused(t, out, allowed)
}

// TestGuardRefusesEmptyRehearsalEvidence: an empty file is a touched file.
func TestGuardRefusesEmptyRehearsalEvidence(t *testing.T) {
	for _, name := range []string{"migration-rehearsal.txt", "rollback-rehearsal.txt"} {
		dir := evidence(t)
		write(t, filepath.Join(dir, name), "")
		out, allowed := guard(t, dir, complete(t))
		if allowed {
			t.Errorf("the guard published with empty %s:\n%s", name, out)
		}
	}
}

// TestGuardNamesWhatIsMissing. A refusal that does not say what to do
// gets worked around rather than satisfied.
func TestGuardNamesWhatIsMissing(t *testing.T) {
	dir := evidence(t)
	os.Remove(filepath.Join(dir, "rollback-rehearsal.txt"))
	env := complete(t)
	env["GH_TOKEN"] = ""
	out, allowed := guard(t, dir, env)
	if allowed {
		t.Fatal("the guard allowed a release missing two things")
	}
	for _, want := range []string{"rollback", "GH_TOKEN"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, out)
		}
	}
}

// assertRefused fails when the guard allowed a release it should not have.
func assertRefused(t *testing.T, out string, allowed bool) {
	t.Helper()
	if allowed {
		t.Fatalf("the guard allowed an incomplete release:\n%s", out)
	}
}
