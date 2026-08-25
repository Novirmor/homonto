package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ErrInspectFailed reports a candidate that could not be interrogated.
var ErrInspectFailed = errors.New("update: candidate could not be inspected")

// MetadataCommand is the hidden command a candidate answers with its own
// metadata. It is hidden because it exists for one machine to ask another
// what it is, not for a human to read.
var MetadataCommand = []string{"update", "candidate-metadata", "--json"}

// inspectTimeout bounds the interrogation. A candidate that will not
// answer promptly is a candidate that will not be installed.
const inspectTimeout = 30 * time.Second

// InspectCandidate asks a staged binary what it is.
//
// It RUNS the candidate, with an empty environment and in a temporary
// directory, and reads the metadata it prints. That is deliberate: the
// manifest describes what a release is supposed to be, and this is the
// only way to find out what it actually is. A candidate that will not
// answer, answers something unparsable, or answers a schema this binary
// does not understand is refused before anything is replaced.
//
// The empty environment is not a sandbox — the candidate is about to
// become the binary on this machine — but it does mean the answer cannot
// depend on the caller's configuration, so two inspections of the same
// file agree.
func InspectCandidate(ctx context.Context, binary string) (CandidateMetadata, error) {
	abs, err := filepath.Abs(binary)
	if err != nil {
		return CandidateMetadata{}, fmt.Errorf("update: resolve %s: %w", binary, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return CandidateMetadata{}, fmt.Errorf("update: stat %s: %w: %w", binary, ErrInspectFailed, err)
	}
	if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return CandidateMetadata{}, fmt.Errorf(
			"update: %s is not an executable file: %w", binary, ErrInspectFailed)
	}
	dir, err := os.MkdirTemp("", "homonto-inspect-*")
	if err != nil {
		return CandidateMetadata{}, fmt.Errorf("update: stage the inspection: %w", err)
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(ctx, inspectTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, abs, MetadataCommand...)
	cmd.Dir = dir
	// No inherited environment: the answer must describe the binary, not
	// the machine that asked.
	cmd.Env = []string{}
	cmd.Stdin = nil
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return CandidateMetadata{}, fmt.Errorf("update: %s did not answer %v (%v): %s: %w",
			binary, MetadataCommand, err, trimmed(stderr.String()), ErrInspectFailed)
	}

	var meta CandidateMetadata
	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&meta); err != nil {
		// A candidate whose metadata this binary cannot fully parse is a
		// candidate speaking a schema from the future. Refusing is the
		// right answer: the fields it added are exactly the ones that
		// would have told us why it is incompatible.
		return CandidateMetadata{}, fmt.Errorf("update: %s answered an unreadable metadata document: %w: %w",
			binary, ErrInspectFailed, err)
	}
	if meta.Version == "" || meta.ProtocolVersion < 1 || meta.StoreSchemaVersion < 1 {
		return CandidateMetadata{}, fmt.Errorf(
			"update: %s answered incomplete metadata (%+v): %w", binary, meta, ErrInspectFailed)
	}
	return meta, nil
}

// trimmed shortens a diagnostic for an error message.
func trimmed(s string) string {
	const limit = 200
	s = string(bytes.TrimSpace([]byte(s)))
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
