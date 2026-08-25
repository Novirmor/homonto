package pathclass

import (
	"errors"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"src/login.go":    "src/login.go",
		"./src/login.go":  "src/login.go",
		"src//login.go":   "src/login.go",
		"src/./login.go":  "src/login.go",
		"src/a/../b.go":   "src/b.go",
		"a.go":            "a.go",
		"docs/homonto/x/": "docs/homonto/x",
	} {
		got, err := Normalize(in)
		if err != nil {
			t.Errorf("Normalize(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", ".", "/abs/path", `src\win.go`, "../escape.go", "src/../../escape.go", "a\x00b"} {
		if _, err := Normalize(bad); err == nil {
			t.Errorf("Normalize(%q) = nil error, want rejection", bad)
		}
	}
}

func TestMatchHandlesDoublestar(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"vendor/**", "vendor/x/y.go", true},
		{"vendor/**", "vendor", false},
		{"vendor/**", "vendors/x.go", false},
		{"**", "anything/at/all.go", true},
		{"**/*_test.go", "internal/x/y_test.go", true},
		{"**/*_test.go", "y_test.go", true},
		{"**/*_test.go", "internal/x/y.go", false},
		{"*_test.go", "y_test.go", true},
		{"*_test.go", "internal/y_test.go", false},
		{"gen/**/*.pb.go", "gen/a/b/c.pb.go", true},
		{"gen/**/*.pb.go", "gen/c.pb.go", true},
		{"gen/**/*.pb.go", "gen/c.go", false},
		{"src/*.go", "src/a.go", true},
		{"src/*.go", "src/a/b.go", false},
	}
	for _, tt := range tests {
		if got := Match(tt.pattern, tt.path); got != tt.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func matcher(t *testing.T, pc *workspacecfg.PathClasses) *Matcher {
	t.Helper()
	m, err := NewMatcher(pc)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}
	return m
}

func TestClassifyAppliesPrecedence(t *testing.T) {
	m := matcher(t, &workspacecfg.PathClasses{
		Tests:     []string{"**/*_test.go"},
		Generated: []string{"gen/**"},
		Vendored:  []string{"vendor/**"},
	})
	for path, want := range map[string]Class{
		"src/login.go":       ClassSource,
		"src/login_test.go":  ClassTest,
		"gen/api.pb.go":      ClassGenerated,
		"vendor/x/y.go":      ClassVendored,
		"README.md":          ClassSource,
		"config/app.toml":    ClassSource,
		"vendor/x/y_test.go": ClassVendored,  // vendored wins over test
		"gen/api_test.pb.go": ClassGenerated, // generated wins over test
	} {
		if got := m.Classify(path); got != want {
			t.Errorf("Classify(%q) = %s, want %s", path, got, want)
		}
	}
	// The overlap is visible rather than silently resolved.
	matches := m.Matches("vendor/x/y_test.go")
	if len(matches) != 2 || matches[0] != ClassVendored || matches[1] != ClassTest {
		t.Fatalf("Matches = %v, want [vendored test]", matches)
	}
}

// TestOverlappingPatternIsRefused proves an ambiguous configuration is
// rejected rather than resolved by evaluation order.
func TestOverlappingPatternIsRefused(t *testing.T) {
	_, err := NewMatcher(&workspacecfg.PathClasses{
		Tests:     []string{"gen/**"},
		Generated: []string{"gen/**"},
	})
	if !errors.Is(err, ErrOverlappingPattern) {
		t.Fatalf("NewMatcher error = %v, want ErrOverlappingPattern", err)
	}
}

func TestInvalidPatternsAreRefused(t *testing.T) {
	for _, bad := range []string{"", "/abs/**", `win\**`, "../escape/**", "a**b/x"} {
		_, err := NewMatcher(&workspacecfg.PathClasses{Tests: []string{bad}})
		if !errors.Is(err, ErrInvalidPattern) {
			t.Errorf("NewMatcher(%q) error = %v, want ErrInvalidPattern", bad, err)
		}
	}
}

func TestNilClassesClassifyEverythingAsSource(t *testing.T) {
	m := matcher(t, nil)
	for _, p := range []string{"src/a.go", "src/a_test.go", "vendor/x.go"} {
		if got := m.Classify(p); got != ClassSource {
			t.Errorf("Classify(%q) = %s, want source with no configured classes", p, got)
		}
	}
}

// fixedMatchers resolves one matcher for every member.
func fixedMatchers(m *Matcher) Matchers {
	return func(string) (*Matcher, error) { return m, nil }
}

func TestCountPresetChangesCountsUniqueSourcePaths(t *testing.T) {
	m := matcher(t, &workspacecfg.PathClasses{
		Tests:     []string{"**/*_test.go"},
		Generated: []string{"gen/**"},
		Vendored:  []string{"vendor/**"},
	})
	count, err := CountPresetChanges([]DiffEntry{
		{Member: "api", Path: "src/login.go", Op: OpModified},
		{Member: "api", Path: "src/login_test.go", Op: OpAdded},
		{Member: "api", Path: "gen/api.pb.go", Op: OpModified},
		{Member: "api", Path: "vendor/x/y.go", Op: OpAdded},
		{Member: "api", Path: "README.md", Op: OpModified},
		// The same file touched twice counts once.
		{Member: "api", Path: "./src/login.go", Op: OpModified},
	}, fixedMatchers(m))
	if err != nil {
		t.Fatalf("CountPresetChanges: %v", err)
	}
	if count.Total != 2 {
		t.Fatalf("Total = %d, want 2 (the source file and the README): %+v", count.Total, count)
	}
	want := []string{"api:README.md", "api:src/login.go"}
	if strings.Join(count.Counted, ",") != strings.Join(want, ",") {
		t.Fatalf("Counted = %v, want %v", count.Counted, want)
	}
	if len(count.Excluded) != 3 {
		t.Fatalf("Excluded = %v, want the test, generated, and vendored paths", count.Excluded)
	}
}

// TestRenameCountsOnce is the spec's rule, and the reason the count is
// keyed on the entry rather than on its endpoints.
func TestRenameCountsOnce(t *testing.T) {
	m := matcher(t, &workspacecfg.PathClasses{Vendored: []string{"vendor/**"}})
	count, err := CountPresetChanges([]DiffEntry{
		{Member: "api", Path: "src/new.go", OldPath: "src/old.go", Op: OpRenamed},
	}, fixedMatchers(m))
	if err != nil {
		t.Fatalf("CountPresetChanges: %v", err)
	}
	if count.Total != 1 {
		t.Fatalf("Total = %d, want 1: a rename is one change: %+v", count.Total, count)
	}
	if count.Counted[0] != "api:src/new.go" {
		t.Fatalf("Counted = %v, want the new path", count.Counted)
	}
}

// TestRenameAcrossAnExcludedBoundaryCounts proves the conservative
// reading: moving a source file into vendor/ is a real change to the
// source tree even though where it landed does not count.
func TestRenameAcrossAnExcludedBoundaryCounts(t *testing.T) {
	m := matcher(t, &workspacecfg.PathClasses{Vendored: []string{"vendor/**"}})
	count, err := CountPresetChanges([]DiffEntry{
		{Member: "api", Path: "vendor/x/moved.go", OldPath: "src/moved.go", Op: OpRenamed},
	}, fixedMatchers(m))
	if err != nil {
		t.Fatalf("CountPresetChanges: %v", err)
	}
	if count.Total != 1 {
		t.Fatalf("Total = %d, want 1: the source endpoint counts: %+v", count.Total, count)
	}
	// And a rename entirely inside an excluded class counts for nothing.
	count, err = CountPresetChanges([]DiffEntry{
		{Member: "api", Path: "vendor/b.go", OldPath: "vendor/a.go", Op: OpRenamed},
	}, fixedMatchers(m))
	if err != nil {
		t.Fatalf("CountPresetChanges: %v", err)
	}
	if count.Total != 0 {
		t.Fatalf("Total = %d, want 0: both endpoints are vendored: %+v", count.Total, count)
	}
}

// TestSamePathInDifferentMembersCountsTwice proves members are distinct:
// two repositories may hold src/login.go and they are different files.
func TestSamePathInDifferentMembersCountsTwice(t *testing.T) {
	m := matcher(t, nil)
	count, err := CountPresetChanges([]DiffEntry{
		{Member: "api", Path: "src/login.go", Op: OpModified},
		{Member: "web", Path: "src/login.go", Op: OpModified},
	}, fixedMatchers(m))
	if err != nil {
		t.Fatalf("CountPresetChanges: %v", err)
	}
	if count.Total != 2 {
		t.Fatalf("Total = %d, want 2: %+v", count.Total, count)
	}
}

func TestCountPresetChangesRejectsMalformedEntries(t *testing.T) {
	m := matcher(t, nil)
	tests := []struct {
		name  string
		entry DiffEntry
	}{
		{"unknown operation", DiffEntry{Member: "api", Path: "a.go", Op: "copied"}},
		{"rename with no source", DiffEntry{Member: "api", Path: "a.go", Op: OpRenamed}},
		{"rename to itself", DiffEntry{Member: "api", Path: "a.go", OldPath: "a.go", Op: OpRenamed}},
		{"old path on a modification", DiffEntry{Member: "api", Path: "a.go", OldPath: "b.go", Op: OpModified}},
		{"escaping path", DiffEntry{Member: "api", Path: "../a.go", Op: OpModified}},
		{"absolute path", DiffEntry{Member: "api", Path: "/a.go", Op: OpModified}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := CountPresetChanges([]DiffEntry{tt.entry}, fixedMatchers(m)); err == nil {
				t.Fatal("CountPresetChanges = nil error, want rejection")
			}
		})
	}
	if _, err := CountPresetChanges(nil, nil); err == nil {
		t.Fatal("CountPresetChanges with no resolver = nil error, want rejection")
	}
}

func TestAssessPresetFiresEverySemanticSignal(t *testing.T) {
	for _, signal := range SemanticSignals() {
		got, err := AssessPreset(AssessmentInput{Observed: []Signal{signal}})
		if err != nil {
			t.Errorf("AssessPreset(%s): %v", signal, err)
			continue
		}
		if !got.Pause {
			t.Errorf("%s did not pause the preset", signal)
		}
		if len(got.Signals) != 1 || got.Signals[0] != signal {
			t.Errorf("Signals = %v, want [%s]", got.Signals, signal)
		}
		if len(got.Evidence) != 1 || got.Evidence[0] == "" {
			t.Errorf("%s fired without evidence: %v", signal, got.Evidence)
		}
	}
}

// TestFileCountIsAWarningNotAVerdict pins the spec's most misread rule.
func TestFileCountIsAWarningNotAVerdict(t *testing.T) {
	under, err := AssessPreset(AssessmentInput{Count: Count{Total: FileWarningThreshold}})
	if err != nil {
		t.Fatalf("AssessPreset: %v", err)
	}
	if under.Pause {
		t.Fatalf("exactly %d files paused the preset; the rule is MORE than that", FileWarningThreshold)
	}
	over, err := AssessPreset(AssessmentInput{Count: Count{
		Total:   FileWarningThreshold + 1,
		Counted: []string{"api:a.go"},
	}})
	if err != nil {
		t.Fatalf("AssessPreset: %v", err)
	}
	if !over.Pause {
		t.Fatal("crossing the threshold did not pause the preset")
	}
	if len(over.Signals) != 1 || over.Signals[0] != SignalFileCount {
		t.Fatalf("Signals = %v, want [file_count]", over.Signals)
	}
	// The evidence must say it is a warning, because the next thing that
	// happens is a human reading it.
	if !strings.Contains(over.Evidence[0], "not an automatic upgrade") {
		t.Fatalf("evidence does not say the count is a warning: %q", over.Evidence[0])
	}
}

func TestAssessPresetHonoursAnExplicitThreshold(t *testing.T) {
	got, err := AssessPreset(AssessmentInput{Count: Count{Total: 3}, Threshold: 2})
	if err != nil {
		t.Fatalf("AssessPreset: %v", err)
	}
	if !got.Pause {
		t.Fatal("the explicit threshold was not applied")
	}
	if _, err := AssessPreset(AssessmentInput{Threshold: -1}); err == nil {
		t.Fatal("a negative threshold was accepted")
	}
}

func TestAssessPresetOrdersAndRefusesBadSignals(t *testing.T) {
	got, err := AssessPreset(AssessmentInput{
		Observed: []Signal{SignalShouldSplit, SignalPublicAPI, SignalNewCapability},
		Count:    Count{Total: 99, Counted: []string{"api:a.go"}},
	})
	if err != nil {
		t.Fatalf("AssessPreset: %v", err)
	}
	want := []Signal{SignalNewCapability, SignalPublicAPI, SignalShouldSplit, SignalFileCount}
	if len(got.Signals) != len(want) {
		t.Fatalf("Signals = %v, want %v", got.Signals, want)
	}
	for i := range want {
		if got.Signals[i] != want[i] {
			t.Fatalf("Signals = %v, want %v", got.Signals, want)
		}
	}
	if _, err := AssessPreset(AssessmentInput{Observed: []Signal{"vibes"}}); err == nil {
		t.Error("an unknown signal was accepted")
	}
	if _, err := AssessPreset(AssessmentInput{
		Observed: []Signal{SignalPublicAPI, SignalPublicAPI},
	}); err == nil {
		t.Error("a duplicate signal was accepted")
	}
	// The file count is measured, never observed: accepting it as an
	// observation would let a caller fake the measurement.
	if _, err := AssessPreset(AssessmentInput{Observed: []Signal{SignalFileCount}}); err == nil {
		t.Error("the file-count signal was accepted as an observation")
	}
}

func TestAssessPresetIsQuietWhenNothingFires(t *testing.T) {
	got, err := AssessPreset(AssessmentInput{Count: Count{Total: 1}})
	if err != nil {
		t.Fatalf("AssessPreset: %v", err)
	}
	if got.Pause || len(got.Signals) != 0 || len(got.Evidence) != 0 {
		t.Fatalf("a small, unremarkable change paused the preset: %+v", got)
	}
}

func FuzzMatch(f *testing.F) {
	f.Add("vendor/**", "vendor/x.go")
	f.Add("**/*_test.go", "a/b_test.go")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, pattern, name string) {
		// Match must never panic and must be deterministic.
		first := Match(pattern, name)
		if second := Match(pattern, name); first != second {
			t.Fatalf("Match(%q, %q) is not deterministic", pattern, name)
		}
		// "**" alone matches every non-empty path.
		if pattern == "**" && name != "" && !first {
			t.Fatalf(`Match("**", %q) = false`, name)
		}
	})
}

func FuzzCountPresetChanges(f *testing.F) {
	f.Add("src/a.go", "", "modified")
	f.Add("b.go", "a.go", "renamed")
	f.Fuzz(func(t *testing.T, path, oldPath, op string) {
		m, err := NewMatcher(&workspacecfg.PathClasses{Vendored: []string{"vendor/**"}})
		if err != nil {
			t.Fatalf("NewMatcher: %v", err)
		}
		count, err := CountPresetChanges([]DiffEntry{
			{Member: "api", Path: path, OldPath: oldPath, Op: Op(op)},
		}, fixedMatchers(m))
		if err != nil {
			return
		}
		// A single entry can never count more than once.
		if count.Total > 1 {
			t.Fatalf("one entry counted %d times: %+v", count.Total, count)
		}
		if count.Total != len(count.Counted) {
			t.Fatalf("Total %d does not match Counted %v", count.Total, count.Counted)
		}
	})
}
