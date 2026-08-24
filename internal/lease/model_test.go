package lease

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
)

func validContent(t *testing.T) LeaseContent {
	t.Helper()
	wsID, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("lease: workspace id: %v", err)
	}
	repoID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("lease: repository id: %v", err)
	}
	workID, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("lease: work id: %v", err)
	}
	token, err := identity.NewToken()
	if err != nil {
		t.Fatalf("lease: token: %v", err)
	}
	prov, err := CurrentProcess()
	if err != nil {
		t.Fatalf("lease: process: %v", err)
	}
	return LeaseContent{
		SchemaVersion: 1,
		WorkspaceID:   wsID,
		RepositoryID:  repoID,
		WorkID:        workID,
		Generation:    1,
		Process:       prov,
		RecoveryToken: token,
	}
}

func TestLeaseContentRoundTrip(t *testing.T) {
	want := validContent(t)
	data, err := want.Marshal()
	if err != nil {
		t.Fatalf("lease: marshal: %v", err)
	}
	got, err := ReadBytes(data)
	if err != nil {
		t.Fatalf("lease: read: %v", err)
	}
	if got != want {
		t.Errorf("lease: round trip = %+v, want %+v", got, want)
	}
}

func TestLeaseContentRejectsNonCanonicalForms(t *testing.T) {
	valid := validContent(t)
	cases := []struct {
		name   string
		mutate func(*LeaseContent)
	}{
		{"wrong schema version", func(c *LeaseContent) { c.SchemaVersion = 2 }},
		{"missing schema version", func(c *LeaseContent) { c.SchemaVersion = 0 }},
		{"bad workspace id", func(c *LeaseContent) { c.WorkspaceID = "nope" }},
		{"bad repository id", func(c *LeaseContent) { c.RepositoryID = "nope" }},
		{"bad work id", func(c *LeaseContent) { c.WorkID = "nope" }},
		{"zero generation", func(c *LeaseContent) { c.Generation = 0 }},
		{"bad token", func(c *LeaseContent) { c.RecoveryToken = "short" }},
		{"empty host id", func(c *LeaseContent) { c.Process.HostID = "" }},
		{"empty hostname", func(c *LeaseContent) { c.Process.Hostname = "" }},
		{"zero pid", func(c *LeaseContent) { c.Process.PID = 0 }},
		{"empty executable", func(c *LeaseContent) { c.Process.Executable = "" }},
		{"zero started at", func(c *LeaseContent) { c.Process.StartedAt = time.Time{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.mutate(&c)
			if err := c.Validate(); !errors.Is(err, ErrInvalidLease) {
				t.Errorf("lease: Validate() error = %v, want ErrInvalidLease", err)
			}
			if _, err := c.Marshal(); !errors.Is(err, ErrInvalidLease) {
				t.Errorf("lease: Marshal() error = %v, want ErrInvalidLease", err)
			}
		})
	}
}

func TestLeaseContentStrictDecode(t *testing.T) {
	valid := validContent(t)
	data, err := valid.Marshal()
	if err != nil {
		t.Fatalf("lease: marshal: %v", err)
	}
	if _, err := ReadBytes(append(data, []byte("{}\n")...)); !errors.Is(err, ErrInvalidLease) {
		t.Errorf("lease: trailing data accepted, err = %v", err)
	}
	unknown := strings.Replace(string(data), `"schema_version":1`, `"schema_version":1,"bogus":1`, 1)
	if _, err := ReadBytes([]byte(unknown)); !errors.Is(err, ErrInvalidLease) {
		t.Errorf("lease: unknown field accepted, err = %v", err)
	}
}

func TestAcquireRequestValidation(t *testing.T) {
	wsID, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("lease: workspace id: %v", err)
	}
	workID, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("lease: work id: %v", err)
	}
	repoID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("lease: repository id: %v", err)
	}
	prov, err := CurrentProcess()
	if err != nil {
		t.Fatalf("lease: process: %v", err)
	}
	valid := AcquireRequest{
		WorkspaceID: wsID,
		WorkID:      workID,
		Generation:  1,
		Provenance:  prov,
		ControlRoot: "/tmp/ws",
		Targets:     []Target{{RepositoryID: repoID, Path: "/tmp/ws/member/lease.json"}},
	}
	cases := []struct {
		name   string
		mutate func(*AcquireRequest)
	}{
		{"bad workspace id", func(r *AcquireRequest) { r.WorkspaceID = "x" }},
		{"bad work id", func(r *AcquireRequest) { r.WorkID = "x" }},
		{"zero generation", func(r *AcquireRequest) { r.Generation = 0 }},
		{"relative control root", func(r *AcquireRequest) { r.ControlRoot = "ws" }},
		{"unclean control root", func(r *AcquireRequest) { r.ControlRoot = "/tmp/ws/" }},
		{"zero pid", func(r *AcquireRequest) { r.Provenance.PID = 0 }},
		{"no targets", func(r *AcquireRequest) { r.Targets = nil }},
		{"duplicate repository id", func(r *AcquireRequest) {
			r.Targets = []Target{
				{RepositoryID: repoID, Path: "/tmp/ws/a/lease.json"},
				{RepositoryID: repoID, Path: "/tmp/ws/b/lease.json"},
			}
		}},
		{"duplicate path", func(r *AcquireRequest) {
			other, err := identity.NewRepositoryID()
			if err != nil {
				t.Fatalf("lease: repository id: %v", err)
			}
			r.Targets = []Target{
				{RepositoryID: repoID, Path: "/tmp/ws/a/lease.json"},
				{RepositoryID: other, Path: "/tmp/ws/a/lease.json"},
			}
		}},
		{"bad repository id", func(r *AcquireRequest) {
			r.Targets = []Target{{RepositoryID: "x", Path: "/tmp/ws/a/lease.json"}}
		}},
		{"relative target path", func(r *AcquireRequest) {
			r.Targets = []Target{{RepositoryID: repoID, Path: "a/lease.json"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := valid
			tc.mutate(&r)
			if err := r.Validate(); !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("lease: Validate() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
	if err := valid.Validate(); err != nil {
		t.Errorf("lease: valid request rejected: %v", err)
	}
}

func TestSentinelPathAndContent(t *testing.T) {
	workID, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("lease: work id: %v", err)
	}
	got := SentinelPath("/tmp/ws", workID)
	want := filepath.Join("/tmp/ws", ".homonto", "leases", string(workID)+".active")
	if got != want {
		t.Errorf("lease: SentinelPath = %q, want %q", got, want)
	}

	wsID, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("lease: workspace id: %v", err)
	}
	opID, err := identity.NewOperationID()
	if err != nil {
		t.Fatalf("lease: operation id: %v", err)
	}
	repoID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("lease: repository id: %v", err)
	}
	c := SentinelContent{
		SchemaVersion: 1,
		WorkspaceID:   wsID,
		WorkID:        workID,
		Generation:    1,
		Version:       1,
		OperationID:   opID,
		Leases:        []SentinelLease{{RepositoryID: repoID, Path: "/tmp/ws/member/lease.json"}},
	}
	data, err := c.Marshal()
	if err != nil {
		t.Fatalf("lease: sentinel marshal: %v", err)
	}
	back, err := ReadSentinelBytes(data)
	if err != nil {
		t.Fatalf("lease: sentinel read: %v", err)
	}
	if !reflect.DeepEqual(back, c) {
		t.Errorf("lease: sentinel round trip = %+v, want %+v", back, c)
	}

	bad := c
	bad.SchemaVersion = 0
	if _, err := bad.Marshal(); !errors.Is(err, ErrInvalidLease) {
		t.Errorf("lease: sentinel schema version accepted, err = %v", err)
	}
	bad = c
	bad.Version = 0
	if _, err := bad.Marshal(); !errors.Is(err, ErrInvalidLease) {
		t.Errorf("lease: sentinel version zero accepted, err = %v", err)
	}
}

func TestProcessAliveDiagnostic(t *testing.T) {
	cur, err := CurrentProcess()
	if err != nil {
		t.Fatalf("lease: current process: %v", err)
	}
	if cur.PID <= 0 || cur.Hostname == "" || cur.Executable == "" || cur.StartedAt.IsZero() {
		t.Errorf("lease: current process = %+v, want populated provenance", cur)
	}
	if !cur.Alive() {
		t.Error("lease: current process reported dead")
	}

	cmd := startExitChild(t)
	if cmd.Alive() {
		t.Error("lease: exited child process reported alive (PID reuse caveat: a recycled pid would report alive)")
	}
}

// startExitChild starts a short-lived child and returns its Process after
// the child has exited, so its pid no longer maps to a running process.
func startExitChild(t *testing.T) Process {
	t.Helper()
	exe := "/bin/sh"
	if _, err := os.Stat(exe); err != nil {
		exe = "/usr/bin/true"
	}
	cmd := startCommand(t, exe)
	pid := cmd.Process.Pid
	waitForExit(t, cmd)
	return Process{HostID: "test", Hostname: "test", PID: pid, Executable: exe, StartedAt: time.Now()}
}

// startCommand starts a command that exits immediately.
func startCommand(t *testing.T, exe string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(exe)
	if err := cmd.Start(); err != nil {
		t.Fatalf("lease: start %s: %v", exe, err)
	}
	return cmd
}

// waitForExit waits for cmd to finish.
func waitForExit(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("lease: wait %s: %v", cmd.Path, err)
	}
}
