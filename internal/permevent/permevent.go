// Package permevent pins the OpenCode 2 permission event contract that the
// bundled permission-observer plugin consumes. The OpenCode 1 event shapes and
// their Go correlator were removed with OpenCode 1 support (ADR 0081).
package permevent

// PinnedOpencodeRevision is the upstream OpenCode 2 commit the event contract
// follows (the runtime permission producer). A fixture mismatch against a
// newer revision must update this pin deliberately, never silently.
const PinnedOpencodeRevision = "3a103fe0aff726a4edc7492f03f7b88195d9e4c9"

// Event names as OpenCode spells them.
const (
	EventAsked   = "permission.asked"
	EventReplied = "permission.replied"
)

// Decision values carried by permission.replied.
const (
	ReplyOnce   = "once"
	ReplyAlways = "always"
	ReplyReject = "reject"
)
