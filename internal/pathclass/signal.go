package pathclass

import (
	"fmt"
	"sort"
	"strings"
)

// FileWarningThreshold is the spec's "more than five changed non-test
// files". Crossing it is a warning that pauses the preset for a human, not
// a verdict: no count upgrades anything by itself.
const FileWarningThreshold = 5

// Signal is one reason a preset should stop and ask a human.
type Signal string

const (
	// SignalNewCapability: the change adds a capability rather than
	// adjusting one.
	SignalNewCapability Signal = "new_capability"
	// SignalPublicAPI: a public API changes.
	SignalPublicAPI Signal = "public_api"
	// SignalStorageSchema: a storage schema changes.
	SignalStorageSchema Signal = "storage_schema"
	// SignalCrossModule: the work needs coordination across modules.
	SignalCrossModule Signal = "cross_module"
	// SignalArchitecture: a deep architectural change.
	SignalArchitecture Signal = "architecture"
	// SignalShouldSplit: the scope should be several formal changes.
	SignalShouldSplit Signal = "should_split"
	// SignalIntentExpansion: the work has materially outgrown the intent
	// the human confirmed, even when no other category matches. It exists
	// precisely so that "none of the boxes tick" is not a way to smuggle a
	// large change through a preset.
	SignalIntentExpansion Signal = "intent_expansion"
	// SignalFileCount: more than FileWarningThreshold counted files
	// changed. A WARNING: it pauses, it never upgrades.
	SignalFileCount Signal = "file_count"
)

// semanticSignals are the ones a human or an agent observes. The file
// count is not among them: it is measured, not judged.
var semanticSignals = []Signal{
	SignalNewCapability, SignalPublicAPI, SignalStorageSchema, SignalCrossModule,
	SignalArchitecture, SignalShouldSplit, SignalIntentExpansion,
}

// Known reports whether s is a recognized signal.
func (s Signal) Known() bool {
	if s == SignalFileCount {
		return true
	}
	for _, k := range semanticSignals {
		if k == s {
			return true
		}
	}
	return false
}

// Semantic reports whether s is a judged signal rather than the measured
// file count.
func (s Signal) Semantic() bool { return s != SignalFileCount && s.Known() }

// SemanticSignals returns every judged signal, in canonical order.
func SemanticSignals() []Signal { return append([]Signal(nil), semanticSignals...) }

// AssessmentInput is everything the preset scope assessment reads: what
// was measured, and what was observed.
type AssessmentInput struct {
	// Count is the measured diff against the immutable work baseline.
	Count Count
	// Observed are the semantic signals an explorer, a skeptic, or a human
	// reported. Duplicates and unknown values are refused rather than
	// ignored: a signal Homonto does not understand is one it cannot
	// weigh, and dropping it silently would under-report the scope.
	Observed []Signal
	// Threshold overrides FileWarningThreshold; zero means the default.
	Threshold int
}

// Assessment is what the preset scope assessment concluded.
type Assessment struct {
	// Signals are every signal that fired, in canonical order.
	Signals []Signal
	// Pause reports whether the preset must stop and ask a human. It is
	// true when ANY signal fired.
	Pause bool
	// Evidence explains each signal in a sentence, for the decision
	// prompt. A pause a human cannot evaluate is a pause they will learn
	// to dismiss.
	Evidence []string
}

// AssessPreset weighs a preset's scope.
//
// It decides one thing: whether to stop and ask. It never picks a path,
// never upgrades, and never continues on the human's behalf — "the human
// may continue the preset with the broader scope recorded or upgrade to
// Full", and both of those are answers to a question this function only
// poses.
func AssessPreset(in AssessmentInput) (Assessment, error) {
	threshold := in.Threshold
	if threshold == 0 {
		threshold = FileWarningThreshold
	}
	if threshold < 0 {
		return Assessment{}, fmt.Errorf("pathclass: threshold %d must not be negative", threshold)
	}

	fired := map[Signal]bool{}
	for i, s := range in.Observed {
		if !s.Known() {
			return Assessment{}, fmt.Errorf("pathclass: observed[%d] %q is not a known signal", i, s)
		}
		if !s.Semantic() {
			return Assessment{}, fmt.Errorf(
				"pathclass: observed[%d] %q is measured, not observed; it is derived from the count", i, s)
		}
		if fired[s] {
			return Assessment{}, fmt.Errorf("pathclass: observed[%d] %q is a duplicate", i, s)
		}
		fired[s] = true
	}

	var out Assessment
	for _, s := range semanticSignals {
		if !fired[s] {
			continue
		}
		out.Signals = append(out.Signals, s)
		out.Evidence = append(out.Evidence, evidenceFor(s))
	}
	if in.Count.Total > threshold {
		out.Signals = append(out.Signals, SignalFileCount)
		out.Evidence = append(out.Evidence, fmt.Sprintf(
			"%d counted files changed, more than the %d a preset expects: %s"+
				" (the count is a warning, not an automatic upgrade)",
			in.Count.Total, threshold, strings.Join(in.Count.Counted, ", ")))
	}
	out.Pause = len(out.Signals) > 0
	return out, nil
}

// evidenceFor renders one semantic signal for the decision prompt.
func evidenceFor(s Signal) string {
	switch s {
	case SignalNewCapability:
		return "the change adds a new capability rather than adjusting an existing one"
	case SignalPublicAPI:
		return "a public API changes"
	case SignalStorageSchema:
		return "a storage schema changes"
	case SignalCrossModule:
		return "the work needs coordination across modules"
	case SignalArchitecture:
		return "the change is architectural"
	case SignalShouldSplit:
		return "the scope should be split into several formal changes"
	case SignalIntentExpansion:
		return "the work has materially outgrown the confirmed intent"
	}
	return string(s)
}

// SortSignals returns signals in canonical order, deduplicated.
func SortSignals(in []Signal) []Signal {
	rank := map[Signal]int{}
	for i, s := range append(SemanticSignals(), SignalFileCount) {
		rank[s] = i
	}
	seen := map[Signal]bool{}
	var out []Signal
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return rank[out[i]] < rank[out[j]] })
	return out
}
