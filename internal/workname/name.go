// Package workname validates the user-facing kebab-case names given to
// tasks and changes. Names become directories and CLI targets, so the
// grammar is deliberately narrow ASCII and collisions with Homonto's own
// command and directory names are refused.
package workname

import "fmt"

// MaxLength is the longest accepted work name.
const MaxLength = 64

// reserved names collide with workflow directories (archive, active) and
// top-level CLI commands (the rest); a work must never be able to shadow
// them.
var reserved = map[string]bool{
	"archive": true,
	"active":  true,
	"task":    true,
	"change":  true,
	"init":    true,
	"attach":  true,
	"status":  true,
	"doctor":  true,
	"update":  true,
	"version": true,
}

// Validate reports whether name is an acceptable work name: 1..64 ASCII
// lowercase kebab-case ([a-z0-9] segments joined by single hyphens), starting
// and ending with an alphanumeric, and not one of the reserved names.
func Validate(name string) error {
	if name == "" {
		return fmt.Errorf("workname: name must not be empty")
	}
	if len(name) > MaxLength {
		return fmt.Errorf("workname: name is %d characters, want at most %d", len(name), MaxLength)
	}
	if reserved[name] {
		return fmt.Errorf("workname: %q is reserved", name)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-':
			if i == 0 || i == len(name)-1 {
				return fmt.Errorf("workname: name must start and end with a letter or digit")
			}
			if name[i-1] == '-' {
				return fmt.Errorf("workname: name contains a double hyphen")
			}
		default:
			return fmt.Errorf("workname: character %q is not allowed; use lowercase ASCII letters, digits, and single hyphens", string(rune(c)))
		}
	}
	return nil
}
