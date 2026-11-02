package cli

import (
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/bitbucket"
)

func TestBitbucketAPIRequiresATokenAndJSONInput(t *testing.T) {
	t.Setenv(bitbucket.TokenEnv, "")
	if _, err := runCmd(t, t.TempDir(), "", "host", "bitbucket", "api", "user"); err == nil || !strings.Contains(err.Error(), bitbucket.TokenEnv) {
		t.Fatalf("missing token = %v", err)
	}
	t.Setenv(bitbucket.TokenEnv, "secret-token-value")
	for _, args := range [][]string{
		{"host", "bitbucket", "api", "https://evil.example/x"},
		{"host", "bitbucket", "api", "--input", "body.json", "user"},
		{"host", "bitbucket", "api", "--method", "PATCH", "user"},
	} {
		out, err := runCmd(t, t.TempDir(), "", args...)
		if err == nil || strings.Contains(out+err.Error(), "secret-token-value") {
			t.Fatalf("%v: err=%v out=%q", args, err, out)
		}
	}
	if _, err := runCmd(t, t.TempDir(), "not json", "host", "bitbucket", "api", "--method", "POST", "--input", "-", "user"); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("non-JSON input = %v", err)
	}
}
