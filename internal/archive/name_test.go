package archive

import (
	"testing"
	"time"
)

func TestNameFirstFreeSuffix(t *testing.T) {
	date := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		exists func(string) bool
		want   string
	}{
		{"base free", func(string) bool { return false }, "2026-08-25-bugfix"},
		{"base taken first free -2", func(c string) bool { return c == "2026-08-25-bugfix" }, "2026-08-25-bugfix-2"},
		{"suffix -2 taken", func(c string) bool {
			return c == "2026-08-25-bugfix" || c == "2026-08-25-bugfix-2"
		}, "2026-08-25-bugfix-3"},
		{"skip taken -3", func(c string) bool {
			return c == "2026-08-25-bugfix" || c == "2026-08-25-bugfix-3"
		}, "2026-08-25-bugfix-2"},
		{"many taken", func(c string) bool {
			for _, n := range []string{"2026-08-25-bugfix", "2026-08-25-bugfix-2", "2026-08-25-bugfix-3", "2026-08-25-bugfix-4"} {
				if c == n {
					return true
				}
			}
			return false
		}, "2026-08-25-bugfix-5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Name(date, "bugfix", tt.exists)
			if err != nil {
				t.Fatalf("Name: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Name = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNameValidatesWorkName(t *testing.T) {
	date := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{"", "Uppercase", "with space", "archive", "trailing-", "-leading"} {
		if _, err := Name(date, name, func(string) bool { return false }); err == nil {
			t.Errorf("Name(date, %q) = nil error, want workname validation failure", name)
		}
	}
}

func TestNameFormatsDateCanonically(t *testing.T) {
	for _, d := range []time.Time{
		time.Date(2026, 1, 2, 23, 59, 59, 0, time.UTC),
		time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC),
	} {
		got, err := Name(d, "foo", func(string) bool { return false })
		if err != nil {
			t.Fatalf("Name: %v", err)
		}
		want := d.Format("2006-01-02") + "-foo"
		if got != want {
			t.Fatalf("Name = %q, want %q", got, want)
		}
	}
}
