// Package archive owns the durable record of finished work: the
// collision-safe naming of archived entries and the move/lookup service
// over the control repository's archive directories.
//
// # Layout
//
// Active work lives under docs/homonto/ — a task as one file, a change as
// a directory — and archiving moves it into that tree's archive:
//
//	task:   docs/homonto/tasks/archive/<YYYY-MM-DD>-<name>[-N].md
//	change: docs/homonto/changes/archive/<YYYY-MM-DD>-<name>[-N]/
//
// The name suffix (-2, -3, ...) resolves same-day name collisions; it
// starts at -2 so an unsuffixed name is always the first archive of that
// name on that day. Work identity is NEVER derived from names or suffixes:
// every lookup reads the artifact metadata block inside the entry and
// matches the work id (see Service.LookupByID). A work archived twice on
// one day therefore never shadows another, and a renamed work still
// resolves.
//
// # I/O boundary
//
// Every read and every move goes through securefs: fd-anchored,
// symlink-refusing, fsynced. Directory listings and existence probes use
// os.ReadDir/os.Lstat on paths joined onto the control root, because
// securefs deliberately exposes no listing primitive. Those os-level
// probes are advisory only — they choose a free name and enumerate
// candidates; every access that follows goes back through securefs and
// fails closed if any component is a symlink.
package archive

import (
	"strconv"
	"time"

	"github.com/noviopenworks/homonto/internal/workname"
)

// dateFormat is the canonical YYYY-MM-DD archive-date spelling.
const dateFormat = "2006-01-02"

// Name returns the first-free archive base name for a work with name
// archived on date: "<YYYY-MM-DD>-<name>", or suffixed "-2", "-3", ...
// when the base (or an earlier suffix) is already taken. exists reports
// whether a candidate base name is occupied; Name calls it until one is
// free, so the caller decides what "occupied" means (a file, a directory,
// or either).
func Name(date time.Time, name string, exists func(string) bool) (string, error) {
	if err := workname.Validate(name); err != nil {
		return "", err
	}
	base := date.Format(dateFormat) + "-" + name
	if !exists(base) {
		return base, nil
	}
	for n := 2; ; n++ {
		cand := base + "-" + strconv.Itoa(n)
		if !exists(cand) {
			return cand, nil
		}
	}
}

// dateOf extracts the leading YYYY-MM-DD of an archive entry name; it is
// informational only — identity never relies on it.
func dateOf(name string) string {
	if len(name) < len(dateFormat) {
		return ""
	}
	return name[:len(dateFormat)]
}
