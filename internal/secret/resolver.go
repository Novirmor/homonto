package secret

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

var refRe = regexp.MustCompile(`\$\{([^}]+)\}`)

// passTimeout bounds a single `pass show` invocation. `pass` may shell out to
// gpg-agent, which can block indefinitely waiting for a passphrase prompt;
// without a deadline a hung pass hangs the whole apply with no way for the
// caller's context to interrupt it. The CLI is single-threaded, so this leaf
// timeout is sufficient — we do not need to thread context through the entire
// resolver/adapter contract.
const passTimeout = 30 * time.Second

// Resolver replaces ${...} references with values from pass or the environment.
// Resolved tokens are memoized for the Resolver's lifetime (one `pass`
// invocation per distinct token per run; the CLI is single-threaded, so a
// plain map suffices).
type Resolver struct {
	Getenv func(string) string
	Pass   func(path string) (string, error)

	cache map[string]string // token body (e.g. "pass:ai/brave") -> value
}

// NewResolver returns a Resolver backed by os.Getenv and `pass show`.
func NewResolver() *Resolver {
	return &Resolver{
		Getenv: os.Getenv,
		Pass: func(path string) (string, error) {
			// gpg-agent may block on a passphrase prompt; bound it so apply
			// cannot hang forever waiting on interactive input that will never
			// arrive in a non-interactive CLI run.
			ctx, cancel := context.WithTimeout(context.Background(), passTimeout)
			defer cancel()
			out, err := exec.CommandContext(ctx, "pass", "show", path).Output()
			if err != nil {
				return "", fmt.Errorf("pass show %s: %w", path, err)
			}
			return strings.SplitN(strings.TrimRight(string(out), "\n"), "\n", 2)[0], nil
		},
	}
}

// ContainsRef reports whether s contains a ${...} reference.
func ContainsRef(s string) bool { return refRe.MatchString(s) }

// ResolveJSON parses a JSON-encoded value and resolves ${...} tokens in every
// string leaf, returning the resolved Go value. Resolving on parsed leaves (not
// on the serialized text) means a secret containing quotes, backslashes, or
// newlines can never corrupt the document or inject sibling keys.
func (r *Resolver) ResolveJSON(raw string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, err
	}
	return r.resolveValue(v)
}

func (r *Resolver) resolveValue(v any) (any, error) {
	switch t := v.(type) {
	case string:
		return r.Resolve(t)
	case map[string]any:
		// Key order, so the same document always reports the same failing ref.
		for _, k := range slices.Sorted(maps.Keys(t)) {
			rv, err := r.resolveValue(t[k])
			if err != nil {
				return nil, err
			}
			t[k] = rv
		}
		return t, nil
	case []any:
		for i, e := range t {
			rv, err := r.resolveValue(e)
			if err != nil {
				return nil, err
			}
			t[i] = rv
		}
		return t, nil
	default:
		return v, nil
	}
}

// Resolve replaces every ${...} token in s with its resolved value.
func (r *Resolver) Resolve(s string) (string, error) {
	var firstErr error
	out := refRe.ReplaceAllStringFunc(s, func(tok string) string {
		inner := tok[2 : len(tok)-1] // strip ${ }
		if val, ok := r.cache[inner]; ok {
			return val
		}
		var val string
		var err error
		if strings.HasPrefix(inner, "pass:") {
			val, err = r.Pass(strings.TrimPrefix(inner, "pass:"))
		} else {
			val = r.Getenv(inner)
			if val == "" {
				err = fmt.Errorf("env var %s is not set", inner)
			}
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return val // failures are not cached: a retry re-resolves
		}
		if r.cache == nil {
			r.cache = map[string]string{}
		}
		r.cache[inner] = val
		return val
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}
