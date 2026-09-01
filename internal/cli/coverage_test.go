package cli

import (
	"strings"
	"testing"
)

func TestCoverageComplete(t *testing.T) {
	if err := coverageComplete(nil); err != nil {
		t.Errorf("coverageComplete(nil) = %v, want nil", err)
	}
	if err := coverageComplete([]string{}); err != nil {
		t.Errorf("coverageComplete(empty) = %v, want nil", err)
	}
	err := coverageComplete([]string{"skipped opencode: unwritable"})
	if err == nil {
		t.Fatal("coverageComplete(warnings) = nil, want error")
	}
	if !strings.Contains(err.Error(), "incomplete") || !strings.Contains(err.Error(), "unwritable") {
		t.Errorf("error %q should mention incomplete coverage and the warning", err.Error())
	}
}
