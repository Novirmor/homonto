package workname

import (
	"strings"
	"testing"
)

func TestValidateAcceptsKebabCaseNames(t *testing.T) {
	valid := map[string]string{
		"single":          "fix-login",
		"one-char":        "a",
		"leading-digits":  "2fa-rollout",
		"numeric-only":    "123",
		"mixed-alnum":     "sso-v2-2026",
		"max-length-64":   strings.Repeat("a", 64),
		"boundary-64-alt": "a" + strings.Repeat("-b", 31) + "c", // 64 chars
	}
	for name, candidate := range valid {
		t.Run(name, func(t *testing.T) {
			if err := Validate(candidate); err != nil {
				t.Fatalf("Validate(%q) = %v, want nil", candidate, err)
			}
		})
	}
}

func TestValidateRejectsInvalidKebabCase(t *testing.T) {
	invalid := map[string]string{
		"empty":           "",
		"max-plus-one":    strings.Repeat("a", 65),
		"leading-hyphen":  "-fix-login",
		"trailing-hyphen": "fix-login-",
		"double-hyphen":   "fix--login",
		"hyphen-only":     "-",
		"uppercase":       "Fix-Login",
		"all-uppercase":   "FIXLOGIN",
		"underscore":      "fix_login",
		"space":           "fix login",
		"dot":             "fix.login",
		"slash":           "fix/login",
		"plus":            "fix+login",
		"unicode":         "fix-lögin",
		"emoji":           "fix-🚀",
		"tab":             "fix\tlogin",
		"newline":         "fix\nlogin",
		"null-byte":       "fix\x00login",
		"tilde":           "~fix",
		"ampersand":       "a&b",
	}
	for name, candidate := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := Validate(candidate); err == nil {
				t.Fatalf("Validate(%q) = nil, want error", candidate)
			}
		})
	}
}

// TestValidateRejectsReservedNames: these collide with workflow directories
// and CLI command names, so they can never be taken by a work.
func TestValidateRejectsReservedNames(t *testing.T) {
	reserved := []string{
		"archive", "active", "task", "change", "init",
		"attach", "status", "doctor", "update", "version",
	}
	for _, name := range reserved {
		t.Run(name, func(t *testing.T) {
			if err := Validate(name); err == nil {
				t.Fatalf("Validate(%q) = nil, want reserved-name error", name)
			}
		})
	}
}

// TestValidateAllowsReservedWordsAsSegments: only exact matches are reserved;
// a legitimate name that merely contains a reserved word stays usable.
func TestValidateAllowsReservedWordsAsSegments(t *testing.T) {
	for _, candidate := range []string{"archive-cleanup", "task-runner", "version-bump", "init-parser"} {
		if err := Validate(candidate); err != nil {
			t.Fatalf("Validate(%q) = %v, want nil; only exact reserved matches are rejected", candidate, err)
		}
	}
}
