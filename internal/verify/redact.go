package verify

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"github.com/noviopenworks/homonto/internal/fingerprint"
)

// RedactedMarker replaces every occurrence of a forwarded secret value.
const RedactedMarker = "[redacted]"

// minSecretLength is the shortest value worth redacting. Very short
// values (a one-character HOME, an empty variable) match everywhere and
// would turn output into noise while protecting nothing.
const minSecretLength = 6

// Sanitize returns data as valid UTF-8, replacing every invalid byte
// sequence with the Unicode replacement character. Check output is
// arbitrary bytes — a compiler emitting latin-1, a test printing a binary
// blob — and everything downstream (the database, the digest, the record)
// must be able to hold it without corrupting.
func Sanitize(data []byte) []byte {
	if utf8.Valid(data) {
		return data
	}
	var buf bytes.Buffer
	buf.Grow(len(data))
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			buf.WriteRune(utf8.RuneError)
			i++
			continue
		}
		buf.Write(data[i : i+size])
		i += size
	}
	return buf.Bytes()
}

// Redact removes forwarded secret values from output. A check's
// environment allowlist names the variables whose values the command can
// see; those same values must never come back out in recorded evidence,
// so every occurrence is replaced. Longer secrets are replaced first, so a
// value that contains another is not left half-redacted.
func Redact(data []byte, secrets []string) []byte {
	ordered := redactable(secrets)
	if len(ordered) == 0 {
		return data
	}
	out := string(data)
	for _, s := range ordered {
		out = strings.ReplaceAll(out, s, RedactedMarker)
	}
	return []byte(out)
}

// redactable returns the secrets worth replacing, longest first and
// deduplicated.
func redactable(secrets []string) []string {
	seen := make(map[string]bool, len(secrets))
	out := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if len(s) < minSecretLength || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	// Longest first: a value that contains a shorter one must be replaced
	// whole rather than being chopped around the inner match.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && len(out[j]) > len(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// clean prepares one raw stream for storage: sanitize to valid UTF-8
// first, then redact. Secrets are environment values, which are text, so
// redacting the sanitized form is the order that actually matches them —
// a secret interrupted by an invalid byte was never a contiguous match in
// the raw bytes either.
func clean(data []byte, secrets []string) []byte {
	return Redact(Sanitize(data), secrets)
}

// summarize measures a redacted stream pair and digests it. The digest
// covers both streams under a domain, so it identifies the output without
// carrying it.
func summarize(stdout, stderr []byte, truncated bool) Summary {
	var buf bytes.Buffer
	buf.WriteString("stdout\n")
	buf.Write(stdout)
	buf.WriteString("\nstderr\n")
	buf.Write(stderr)
	return Summary{
		StdoutBytes: len(stdout),
		StdoutLines: countLines(stdout),
		StderrBytes: len(stderr),
		StderrLines: countLines(stderr),
		Truncated:   truncated,
		Output:      fingerprint.Bytes("verify-output", buf.Bytes()),
	}
}

// countLines counts newline-terminated lines plus a trailing partial one.
func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}
