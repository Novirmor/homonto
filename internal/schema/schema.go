// Package schema carries cross-package sentinels for persisted-document
// schema checks. The state database and the workspace config manifest
// each enforce a schema version cap; converging on a shared sentinel
// lets callers branch (e.g. detecting "binary too old" without
// substring-matching an error message).
package schema

import "errors"

// ErrTooNew is returned when a persisted document's schema version is higher
// than this binary supports. Wrapping rules: wrap this sentinel as the outer
// cause with the package/file-specific message ("state", "parse config",
// etc.), so errors.Is(err, schema.ErrTooNew) succeeds at any caller.
//
// Mental model: this binary is the old one and the on-disk document was
// written by a newer one — surface the cap and instruct the user to upgrade.
var ErrTooNew = errors.New("schema version newer than this binary supports")
