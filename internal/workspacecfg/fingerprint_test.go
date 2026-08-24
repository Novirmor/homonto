package workspacecfg

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
)

func mustFingerprint(t *testing.T, cfg Config) fingerprint.Digest {
	t.Helper()
	d, err := Fingerprint(cfg)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	return d
}

func TestFingerprintDeterministic(t *testing.T) {
	a := mustLoad(t, filepath.Join("testdata", "valid-full.toml"))
	b := mustLoad(t, filepath.Join("testdata", "valid-full.toml"))
	if mustFingerprint(t, a) != mustFingerprint(t, b) {
		t.Error("Fingerprint of the same file twice differs")
	}
	if MembershipFingerprint(a) != MembershipFingerprint(b) {
		t.Error("MembershipFingerprint of the same file twice differs")
	}
	for _, d := range []fingerprint.Digest{mustFingerprint(t, a), MembershipFingerprint(a)} {
		if err := d.Validate(); err != nil {
			t.Errorf("digest %q invalid: %v", d, err)
		}
	}
}

func TestFingerprintFullConfigSensitive(t *testing.T) {
	a := validCfg()
	b := validCfg()
	b.Members[1].Verification[0].Command = []string{"make", "check"}
	if mustFingerprint(t, a) == mustFingerprint(t, b) {
		t.Error("Fingerprint ignored a changed check command")
	}
}

func TestMembershipIsolatedFromVerificationPathsRoutesUpdate(t *testing.T) {
	a := validCfg()
	b := validCfg()
	b.Members[0].Verification[0].Command = []string{"other"}
	b.Members[0].Paths.Vendored = []string{"third_party/**"}
	b.Routes.Claude.Explorer.Model = "other-model"
	b.Update.Channel = ChannelPrerelease
	b.Integrations.CommitGenerated = true
	if MembershipFingerprint(a) != MembershipFingerprint(b) {
		t.Error("MembershipFingerprint reacted to non-membership fields")
	}
	if mustFingerprint(t, a) == mustFingerprint(t, b) {
		t.Error("Fingerprint ignored real changes")
	}
}

func TestMembershipSensitiveToComposition(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"member kind", func(c *Config) { c.Members[2].Kind = KindGit }},
		{"member path", func(c *Config) { c.Members[2].Path = "docs/published" }},
		{"control path", func(c *Config) { c.Control.Path = "ctl" }},
		{"workflow", func(c *Config) { c.Workspace.Workflow = WorkflowChange }},
		{"dropped member", func(c *Config) { c.Members = c.Members[:2] }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := validCfg()
			b := validCfg()
			tt.mutate(&b)
			if MembershipFingerprint(a) == MembershipFingerprint(b) {
				t.Errorf("MembershipFingerprint ignored %s", tt.name)
			}
		})
	}
}

func TestVerificationIsolation(t *testing.T) {
	a := validCfg()
	b := validCfg()
	b.Members[1].Verification[0].Name = "integration"
	if MembershipFingerprint(a) != MembershipFingerprint(b) {
		t.Error("MembershipFingerprint reacted to a check change")
	}
	va, err := VerificationFingerprint(a, testMemberAPIID)
	if err != nil {
		t.Fatalf("VerificationFingerprint(a): %v", err)
	}
	vb, err := VerificationFingerprint(b, testMemberAPIID)
	if err != nil {
		t.Fatalf("VerificationFingerprint(b): %v", err)
	}
	if va == vb {
		t.Error("VerificationFingerprint ignored a changed check")
	}
	// Other members' checks are untouched, so their digests must not move.
	da, err := VerificationFingerprint(a, testMemberDocsID)
	if err != nil {
		t.Fatalf("VerificationFingerprint(docs): %v", err)
	}
	db, err := VerificationFingerprint(b, testMemberDocsID)
	if err != nil {
		t.Fatalf("VerificationFingerprint(docs b): %v", err)
	}
	if da != db {
		t.Error("VerificationFingerprint moved for an untouched member")
	}
}

func TestPathClassIsolation(t *testing.T) {
	a := validCfg()
	b := validCfg()
	b.Members[0].Paths.Generated = []string{"gen/**"}
	if MembershipFingerprint(a) != MembershipFingerprint(b) {
		t.Error("MembershipFingerprint reacted to a path-class change")
	}
	pa, err := PathClassFingerprint(a, testControlID)
	if err != nil {
		t.Fatalf("PathClassFingerprint(a): %v", err)
	}
	pb, err := PathClassFingerprint(b, testControlID)
	if err != nil {
		t.Fatalf("PathClassFingerprint(b): %v", err)
	}
	if pa == pb {
		t.Error("PathClassFingerprint ignored a changed class")
	}
	va, _ := VerificationFingerprint(a, testControlID)
	vb, _ := VerificationFingerprint(b, testControlID)
	if va != vb {
		t.Error("VerificationFingerprint moved for a path-class-only change")
	}
}

func TestFingerprintOrderIndependence(t *testing.T) {
	a := validCfg()
	b := validCfg()
	// Reverse both member order and check order: canonical form sorts.
	for i, j := 0, len(b.Members)-1; i < j; i, j = i+1, j-1 {
		b.Members[i], b.Members[j] = b.Members[j], b.Members[i]
	}
	for i := range b.Members {
		if b.Members[i].ID == testControlID {
			v := b.Members[i].Verification
			v[0], v[1] = v[1], v[0]
		}
	}
	if mustFingerprint(t, a) != mustFingerprint(t, b) {
		t.Error("Fingerprint depends on member/check declaration order")
	}
	if MembershipFingerprint(a) != MembershipFingerprint(b) {
		t.Error("MembershipFingerprint depends on member order")
	}
	va, _ := VerificationFingerprint(a, testControlID)
	vb, _ := VerificationFingerprint(b, testControlID)
	if va != vb {
		t.Error("VerificationFingerprint depends on check order")
	}
}

func TestPathClassNilEqualsEmpty(t *testing.T) {
	a := validCfg()
	b := validCfg()
	b.Members[2].Paths = &PathClasses{} // explicitly empty vs nil
	pa, err := PathClassFingerprint(a, testMemberDocsID)
	if err != nil {
		t.Fatalf("PathClassFingerprint(a): %v", err)
	}
	pb, err := PathClassFingerprint(b, testMemberDocsID)
	if err != nil {
		t.Fatalf("PathClassFingerprint(b): %v", err)
	}
	if pa != pb {
		t.Error("nil path classes fingerprint differently from empty ones")
	}
}

func TestPerMemberFingerprintUnknownMember(t *testing.T) {
	cfg := validCfg()
	bogus := identity.RepositoryID("ffffffff-1111-4222-8333-444455556666")
	if _, err := VerificationFingerprint(cfg, bogus); !errors.Is(err, ErrUnknownMember) {
		t.Errorf("VerificationFingerprint err = %v, want ErrUnknownMember", err)
	}
	if _, err := PathClassFingerprint(cfg, bogus); !errors.Is(err, ErrUnknownMember) {
		t.Errorf("PathClassFingerprint err = %v, want ErrUnknownMember", err)
	}
}
