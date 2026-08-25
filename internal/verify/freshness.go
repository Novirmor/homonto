package verify

import (
	"fmt"

	"github.com/noviopenworks/homonto/internal/fingerprint"
)

// StaleKind names which input moved under a recorded result set.
type StaleKind string

const (
	// StaleRepository: the set was taken against a different member.
	StaleRepository StaleKind = "repository"
	// StaleConfig: the member's verification configuration changed, so
	// the recorded commands are not the configured ones any more.
	StaleConfig StaleKind = "config"
	// StaleSource: an integrated source fingerprint changed, so the code
	// the checks ran against is not the current code.
	StaleSource StaleKind = "source"
	// StaleArtifact: a document the checks asserted about changed.
	StaleArtifact StaleKind = "artifact"
	// StaleEmpty: the set records no checks at all, so it proves nothing
	// and can never be fresh.
	StaleEmpty StaleKind = "empty"
	// StaleSpec: a recorded result names a check whose spec no longer
	// matches the configuration it claims to satisfy.
	StaleSpec StaleKind = "spec"
)

// StaleReason explains one way a recorded set no longer describes the
// world. Reasons are returned in a fixed order — repository, config,
// source, artifact, spec — so callers and tests see a stable list.
type StaleReason struct {
	Kind   StaleKind `json:"kind"`
	Detail string    `json:"detail"`
}

// Error renders the reason for messages.
func (r StaleReason) Error() string { return string(r.Kind) + ": " + r.Detail }

// Fresh reports whether a recorded result set still describes the current
// world, and if not, exactly which inputs moved.
//
// Freshness is never a matter of age: a set recorded a second ago against
// different sources is stale, and one recorded a week ago against
// unchanged inputs is fresh. An empty set is never fresh, because it
// asserts nothing.
func Fresh(set Set, current Inputs) (bool, []StaleReason) {
	var reasons []StaleReason
	was := set.Inputs.canonical()
	now := current.canonical()

	if was.Repository != now.Repository {
		reasons = append(reasons, StaleReason{
			Kind:   StaleRepository,
			Detail: fmt.Sprintf("recorded against member %s, current member is %s", was.Repository, now.Repository),
		})
	}
	if was.Config != now.Config {
		reasons = append(reasons, StaleReason{
			Kind:   StaleConfig,
			Detail: fmt.Sprintf("verification configuration moved from %s to %s", short(was.Config), short(now.Config)),
		})
	}
	if d, ok := firstDifference(was.Sources, now.Sources); ok {
		reasons = append(reasons, StaleReason{Kind: StaleSource, Detail: d})
	}
	if d, ok := firstDifference(was.Artifacts, now.Artifacts); ok {
		reasons = append(reasons, StaleReason{Kind: StaleArtifact, Detail: d})
	}
	if len(set.Results) == 0 {
		reasons = append(reasons, StaleReason{
			Kind:   StaleEmpty,
			Detail: "the set records no checks, so it proves nothing",
		})
	}
	for _, r := range set.Results {
		pin, err := r.Spec.Digest()
		if err != nil || pin != r.SpecPin {
			reasons = append(reasons, StaleReason{
				Kind:   StaleSpec,
				Detail: fmt.Sprintf("check %q does not match the spec it was pinned to", r.Spec.Name),
			})
			break
		}
	}
	return len(reasons) == 0, reasons
}

// FreshFor is Fresh plus the pass/fail verdict: evidence advances a
// workflow only when it is both fresh and green.
func FreshFor(set Set, current Inputs) (bool, []StaleReason) {
	fresh, reasons := Fresh(set, current)
	if !fresh {
		return false, reasons
	}
	if !set.Passed() {
		return false, nil
	}
	return true, nil
}

// firstDifference describes how two canonical digest lists differ.
func firstDifference(was, now []fingerprint.Digest) (string, bool) {
	if len(was) != len(now) {
		return fmt.Sprintf("recorded %d fingerprint(s), current world has %d", len(was), len(now)), true
	}
	for i := range was {
		if was[i] != now[i] {
			return fmt.Sprintf("fingerprint %s is now %s", short(was[i]), short(now[i])), true
		}
	}
	return "", false
}

// short abbreviates a digest for a message.
func short(d fingerprint.Digest) string {
	if len(d) <= 12 {
		return string(d)
	}
	return string(d[:12])
}
