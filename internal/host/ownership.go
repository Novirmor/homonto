package host

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// markerPrefix opens the ownership marker every generated file carries.
const markerPrefix = "homonto-managed: sha256="

// Comment styles for the file types Homonto generates. A marker has to be
// a comment in the file's own language, or the file it marks stops being
// valid.
type commentStyle struct {
	open  string
	close string
}

var (
	markdownComment = commentStyle{open: "<!-- ", close: " -->"}
	slashComment    = commentStyle{open: "// ", close: ""}
)

// styleFor picks the comment style for a path.
func styleFor(path string) commentStyle {
	if strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".mjs") {
		return slashComment
	}
	return markdownComment
}

// Mark prepends the ownership marker to generated content.
//
// The marker names the digest of everything after it, so ownership is a
// property of the file itself rather than of a state database somewhere
// else. A user who edits the file breaks the digest, and Homonto can tell —
// without needing to remember what it wrote, and without being wrong after
// a checkout, a merge, or a restore from backup.
func Mark(path string, body []byte) []byte {
	style := styleFor(path)
	digest := sha256.Sum256(body)
	marker := style.open + markerPrefix + hex.EncodeToString(digest[:]) + style.close + "\n"
	return append([]byte(marker), body...)
}

// Owned reports whether content carries a marker matching its own body.
func Owned(path string, content []byte) bool {
	body, digest, ok := splitMarker(content)
	if !ok {
		return false
	}
	actual := sha256.Sum256(body)
	return hex.EncodeToString(actual[:]) == digest
}

// splitMarker separates a marked file into its body and the digest its
// marker claims.
func splitMarker(content []byte) (body []byte, digest string, ok bool) {
	line, rest, found := bytes.Cut(content, []byte("\n"))
	if !found {
		return nil, "", false
	}
	text := string(line)
	start := strings.Index(text, markerPrefix)
	if start < 0 {
		return nil, "", false
	}
	digest = text[start+len(markerPrefix):]
	digest = strings.TrimSuffix(strings.TrimSpace(digest), "-->")
	digest = strings.TrimSpace(digest)
	if len(digest) != sha256.Size*2 {
		return nil, "", false
	}
	return rest, digest, true
}

// hookCommandPrefix is how a Claude hook entry declares itself Homonto's.
// Managed-key projection inside a shared document needs a way to recognize
// its own entries that survives the user reformatting the file, and the
// command string is the only part of a hook that is inherently ours.
func hookCommandPrefix(binary string) string { return binary + " host " }

// isHomontoHook reports whether a hook command belongs to Homonto.
func isHomontoHook(command, binary string) bool {
	return strings.HasPrefix(strings.TrimSpace(command), hookCommandPrefix(binary))
}

// describeConflict explains why a file was not written.
func describeConflict(path string) string {
	return fmt.Sprintf(
		"%s exists and does not carry a matching homonto-managed marker, so it was edited "+
			"by hand or written by something else; install --adopt replaces it", path)
}
