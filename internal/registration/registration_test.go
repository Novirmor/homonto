package registration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

const (
	testWorkspaceID = identity.WorkspaceID("0f0f5e1a-2b3c-4d5e-8f9a-bcdef0123456")
	testRepoID      = identity.RepositoryID("1a1a6b2c-3d4e-4f60-9abc-def012345678")
)

func validReg() Registration {
	return Registration{
		SchemaVersion: 1,
		WorkspaceID:   testWorkspaceID,
		RepositoryID:  testRepoID,
		ControlRoot:   "/home/u/ws",
		MemberRoot:    "/home/u/ws/services/api",
		Kind:          workspacecfg.KindGit,
	}
}

func TestRegistrationRoundTrip(t *testing.T) {
	reg := validReg()
	data, err := reg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	back, err := ReadBytes(data)
	if err != nil {
		t.Fatalf("ReadBytes: %v", err)
	}
	if back != reg {
		t.Errorf("round trip = %+v, want %+v", back, reg)
	}
	again, err := back.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(again) != string(data) {
		t.Errorf("marshal not stable: %q vs %q", again, data)
	}
}

func TestRegistrationJSONShape(t *testing.T) {
	data, err := validReg().Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := map[string]any{
		"schema_version": float64(1),
		"workspace_id":   string(testWorkspaceID),
		"repository_id":  string(testRepoID),
		"control_root":   "/home/u/ws",
		"member_root":    "/home/u/ws/services/api",
		"kind":           "git",
	}
	if len(raw) != len(want) {
		t.Errorf("fields = %v, want exactly %v", raw, want)
	}
	for k, v := range want {
		if raw[k] != v {
			t.Errorf("field %q = %v, want %v", k, raw[k], v)
		}
	}
}

func TestReadBytesStrict(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		errSub  string
		wantErr bool
	}{
		{"valid", `{"schema_version":1,"workspace_id":"` + string(testWorkspaceID) + `","repository_id":"` + string(testRepoID) + `","control_root":"/a","member_root":"/a/b","kind":"non_git"}`, "", false},
		{"unknown field", `{"schema_version":1,"workspace_id":"` + string(testWorkspaceID) + `","repository_id":"` + string(testRepoID) + `","control_root":"/a","member_root":"/a/b","kind":"git","extra":1}`, "extra", true},
		{"missing workspace_id", `{"schema_version":1,"repository_id":"` + string(testRepoID) + `","control_root":"/a","member_root":"/a/b","kind":"git"}`, "workspace_id", true},
		{"missing control_root", `{"schema_version":1,"workspace_id":"` + string(testWorkspaceID) + `","repository_id":"` + string(testRepoID) + `","member_root":"/a/b","kind":"git"}`, "control_root", true},
		{"missing member_root", `{"schema_version":1,"workspace_id":"` + string(testWorkspaceID) + `","repository_id":"` + string(testRepoID) + `","control_root":"/a","kind":"git"}`, "member_root", true},
		{"missing kind", `{"schema_version":1,"workspace_id":"` + string(testWorkspaceID) + `","repository_id":"` + string(testRepoID) + `","control_root":"/a","member_root":"/a/b"}`, "kind", true},
		{"schema version 2", `{"schema_version":2,"workspace_id":"` + string(testWorkspaceID) + `","repository_id":"` + string(testRepoID) + `","control_root":"/a","member_root":"/a/b","kind":"git"}`, "schema_version", true},
		{"bad workspace uuid", `{"schema_version":1,"workspace_id":"not-a-uuid","repository_id":"` + string(testRepoID) + `","control_root":"/a","member_root":"/a/b","kind":"git"}`, "workspace_id", true},
		{"uppercase uuid", `{"schema_version":1,"workspace_id":"` + strings.ToUpper(string(testWorkspaceID)) + `","repository_id":"` + string(testRepoID) + `","control_root":"/a","member_root":"/a/b","kind":"git"}`, "workspace_id", true},
		{"bad kind", `{"schema_version":1,"workspace_id":"` + string(testWorkspaceID) + `","repository_id":"` + string(testRepoID) + `","control_root":"/a","member_root":"/a/b","kind":"svn"}`, "kind", true},
		{"relative control root", `{"schema_version":1,"workspace_id":"` + string(testWorkspaceID) + `","repository_id":"` + string(testRepoID) + `","control_root":"ws","member_root":"/a/b","kind":"git"}`, "control_root", true},
		{"dirty member root", `{"schema_version":1,"workspace_id":"` + string(testWorkspaceID) + `","repository_id":"` + string(testRepoID) + `","control_root":"/a","member_root":"/a/b/../c","kind":"git"}`, "member_root", true},
		{"trailing data", `{"schema_version":1,"workspace_id":"` + string(testWorkspaceID) + `","repository_id":"` + string(testRepoID) + `","control_root":"/a","member_root":"/a/b","kind":"git"}{}`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ReadBytes([]byte(tt.json))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReadBytes error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
				t.Errorf("error %q does not name %q", err, tt.errSub)
			}
		})
	}
}

func TestValidateRejectsBadUUID(t *testing.T) {
	reg := validReg()
	reg.RepositoryID = "nope"
	if err := reg.Validate(); err == nil {
		t.Fatal("Validate: expected error for bad repository id")
	}
}
