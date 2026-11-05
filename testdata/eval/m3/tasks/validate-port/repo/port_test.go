package netcfg

import "testing"

func TestParsePort(t *testing.T) {
	for _, ok := range []string{"1", "80", "65535"} {
		if _, err := ParsePort(ok); err != nil {
			t.Errorf("ParsePort(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"0", "-1", "65536", "70000", "http", ""} {
		if _, err := ParsePort(bad); err == nil {
			t.Errorf("ParsePort(%q) accepted an invalid port", bad)
		}
	}
}
