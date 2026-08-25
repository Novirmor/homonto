package verify

import (
	"sort"
	"time"

	"github.com/noviopenworks/homonto/internal/fingerprint"
)

// Outcome grades one check run.
type Outcome string

const (
	// OutcomePassed: the command exited zero.
	OutcomePassed Outcome = "passed"
	// OutcomeFailed: the command exited non-zero.
	OutcomeFailed Outcome = "failed"
	// OutcomeTimeout: the command outlived its timeout and its process
	// group was killed.
	OutcomeTimeout Outcome = "timeout"
	// OutcomeError: the command could not be started or was cut short by
	// the caller's context — no verdict was reached, which is never a
	// pass.
	OutcomeError Outcome = "error"
)

// Blocking reports whether an outcome stops the workflow. Only a pass
// does not: a failure, a timeout, and a check that never ran all block,
// because none of them is evidence that anything works.
func (o Outcome) Blocking() bool { return o != OutcomePassed }

// Summary is the PORTABLE half of a result: how much output there was and
// what it hashed to, never a byte of what it said. It is what a checkpoint
// may carry across machines.
type Summary struct {
	StdoutBytes int                `json:"stdout_bytes"`
	StdoutLines int                `json:"stdout_lines"`
	StderrBytes int                `json:"stderr_bytes"`
	StderrLines int                `json:"stderr_lines"`
	Truncated   bool               `json:"truncated"`
	Output      fingerprint.Digest `json:"output"`
}

// Result is one check run. Stdout and Stderr are the redacted raw streams
// and are LOCAL-ONLY: they belong in the runtime database and must never
// be copied into a checkpoint, a record, or a protocol payload. Everything
// portable about the run is in Summary.
type Result struct {
	Spec      Spec               `json:"spec"`
	SpecPin   fingerprint.Digest `json:"spec_pin"`
	Outcome   Outcome            `json:"outcome"`
	ExitCode  int                `json:"exit_code"`
	StartedAt time.Time          `json:"started_at"`
	Duration  time.Duration      `json:"duration"`
	Summary   Summary            `json:"summary"`
	// Error explains a non-verdict outcome (start failure, timeout).
	Error string `json:"error,omitempty"`

	Stdout []byte `json:"-"`
	Stderr []byte `json:"-"`
}

// Set is one verification pass: the inputs it was taken against and the
// result of every configured check, in configured order.
type Set struct {
	Inputs  Inputs    `json:"inputs"`
	Results []Result  `json:"results"`
	At      time.Time `json:"at"`
}

// Passed reports whether every check in the set passed. An empty set has
// proved nothing, so it does not pass.
func (s Set) Passed() bool {
	if len(s.Results) == 0 {
		return false
	}
	for _, r := range s.Results {
		if r.Outcome.Blocking() {
			return false
		}
	}
	return true
}

// Failures returns the results that block.
func (s Set) Failures() []Result {
	var out []Result
	for _, r := range s.Results {
		if r.Outcome.Blocking() {
			out = append(out, r)
		}
	}
	return out
}

// Portable returns the set with every raw stream dropped — the form that
// may leave this machine.
func (s Set) Portable() Set {
	out := Set{Inputs: s.Inputs.canonical(), At: s.At}
	for _, r := range s.Results {
		r.Stdout = nil
		r.Stderr = nil
		out.Results = append(out.Results, r)
	}
	return out
}

// Digest fingerprints the portable form of the set, so two machines that
// saw the same evidence agree on its identity.
func (s Set) Digest() (fingerprint.Digest, error) {
	return fingerprint.CanonicalJSON("verify-set", s.Portable())
}

// sortedUnique returns digests sorted and deduplicated.
func sortedUnique(in []fingerprint.Digest) []fingerprint.Digest {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[fingerprint.Digest]bool, len(in))
	out := make([]fingerprint.Digest, 0, len(in))
	for _, d := range in {
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
