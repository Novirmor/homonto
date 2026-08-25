package adr

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/noviopenworks/homonto/internal/workname"
)

// Typed document errors.
var (
	// ErrMissingHeading: the ADR does not carry one of the fixed
	// headings.
	ErrMissingHeading = errors.New("adr: the document is missing a required heading")
	// ErrWrongCandidate: the ADR does not answer the candidate it was
	// allocated for.
	ErrWrongCandidate = errors.New("adr: the document does not answer that candidate")
	// ErrMissingDocument: no ADR exists at that path.
	ErrMissingDocument = errors.New("adr: no document at that path")
	// ErrExhausted: the four-digit numbering space is full.
	ErrExhausted = errors.New("adr: the four-digit numbering space is exhausted")
)

// requiredHeadings are the fixed sections every ADR carries. They are
// fixed so the directory stays skimmable, and checked so an ADR that omits
// Consequences — the part future readers actually need — is refused rather
// than accepted as a record.
var requiredHeadings = []string{"## Context", "## Decision", "## Consequences"}

// maxNumber is the largest four-digit ADR number.
const maxNumber = 9999

// numberPattern matches an allocated ADR file name.
var numberPattern = regexp.MustCompile(`^(\d{4})-[a-z0-9]+(?:-[a-z0-9]+)*\.md$`)

// reservationPattern matches the marker that holds a number while its
// document is being created.
var reservationPattern = regexp.MustCompile(`^\.(\d{4})\.reserved$`)

// reservationName is the marker file for one number.
func reservationName(n int) string { return fmt.Sprintf(".%04d.reserved", n) }

// Slug turns a title into the file-name slug: lowercase, hyphenated, with
// nothing that would need escaping in a path.
func Slug(title string) (string, error) {
	var b strings.Builder
	lastHyphen := true
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if err := workname.Validate(slug); err != nil {
		return "", fmt.Errorf("adr: title %q does not yield a usable slug: %w", title, err)
	}
	return slug, nil
}

// AllocatePath reserves the next ADR number under root and returns the
// file's path, relative to root.
//
// The reservation is on the NUMBER, not on the file name. Two allocations
// racing at the same moment produce different slugs, so creating the
// slugged file with O_EXCL would let both win the same number — and
// because numbers are never reused, that collision would silently merge
// two decisions into one record. So a number is claimed by exclusively
// creating a marker named after the number alone; the winner then creates
// its document and drops the marker.
//
// A crash mid-allocation leaves the marker behind, which keeps that number
// taken. That is the correct outcome: a number that may have been used is
// never handed out again, and a stray marker is visible and removable,
// while a duplicated decision record is neither.
//
// The document is created EMPTY. It is a reservation, not a record;
// writing it is the ADR assignment's job, and ValidateDocument refuses an
// empty one.
func AllocatePath(root, title string) (string, error) {
	slug, err := Slug(title)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, filepath.FromSlash(Dir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("adr: create %s: %w", Dir, err)
	}
	next, err := nextNumber(dir)
	if err != nil {
		return "", err
	}
	for n := next; n <= maxNumber; n++ {
		marker := filepath.Join(dir, reservationName(n))
		f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if errors.Is(err, os.ErrExist) {
			// Someone else holds this number. Take the next one.
			continue
		}
		if err != nil {
			return "", fmt.Errorf("adr: reserve %04d: %w", n, err)
		}
		if err := f.Close(); err != nil {
			return "", fmt.Errorf("adr: reserve %04d: %w", n, err)
		}
		// Winning the marker is what makes this check safe: the scan that
		// chose n may have run before another allocation finished taking
		// it, and only the marker's holder can look without racing.
		taken, err := numberTaken(dir, n)
		if err != nil {
			return "", err
		}
		if taken {
			if err := os.Remove(marker); err != nil {
				return "", fmt.Errorf("adr: release the reservation on %04d: %w", n, err)
			}
			continue
		}
		// Rename rather than create-then-delete: the marker becomes the
		// document in one step, so there is never an instant where neither
		// exists and a concurrent scan could hand the number out again.
		name := fmt.Sprintf("%04d-%s.md", n, slug)
		if err := os.Rename(marker, filepath.Join(dir, name)); err != nil {
			return "", fmt.Errorf("adr: allocate %s: %w", name, err)
		}
		return path.Join(Dir, name), nil
	}
	return "", fmt.Errorf("adr: %w", ErrExhausted)
}

// numberTaken reports whether a document already carries number n.
func numberTaken(dir string, n int) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("adr: list %s: %w", dir, err)
	}
	prefix := fmt.Sprintf("%04d-", n)
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			return true, nil
		}
	}
	return false, nil
}

// nextNumber returns one past the highest allocated number. Numbers are
// never reused, so a gap left by a deleted ADR stays a gap: the record of
// having had that many decisions is part of what the directory says.
func nextNumber(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("adr: list %s: %w", dir, err)
	}
	highest := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := numberPattern.FindStringSubmatch(e.Name())
		if m == nil {
			// A held reservation counts as taken: the number may already
			// belong to a document that is mid-creation, or to one whose
			// creation crashed.
			m = reservationPattern.FindStringSubmatch(e.Name())
		}
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if n > highest {
			highest = n
		}
	}
	return highest + 1, nil
}

// Number extracts the four-digit number from an ADR path.
func Number(p string) (int, bool) {
	m := numberPattern.FindStringSubmatch(path.Base(p))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// ValidateDocument checks that the ADR at path is a record of the
// candidate it was allocated for.
//
// It checks structure, never quality: the fixed headings must be present
// and the candidate's question must appear, so the document answers the
// question it was written for rather than some adjacent one. Whether the
// answer is any good is a reviewer's judgement, not a parser's.
func ValidateDocument(root, rel string, c Candidate) error {
	if err := c.Validate(); err != nil {
		return err
	}
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("adr: %s: %w", rel, ErrMissingDocument)
	}
	if err != nil {
		return fmt.Errorf("adr: read %s: %w", rel, err)
	}
	text := string(body)
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("adr: %s is still the empty reservation: %w", rel, ErrMissingDocument)
	}
	for _, heading := range requiredHeadings {
		if !containsHeading(text, heading) {
			return fmt.Errorf("adr: %s has no %q section: %w", rel, heading, ErrMissingHeading)
		}
	}
	if !hasTitle(text) {
		return fmt.Errorf("adr: %s has no title: %w", rel, ErrMissingHeading)
	}
	if !strings.Contains(text, c.Question) {
		return fmt.Errorf("adr: %s does not state the question %q it was allocated for: %w",
			rel, c.Question, ErrWrongCandidate)
	}
	return nil
}

// hasTitle reports whether text opens some line with a level-one heading.
// Checking for "# " anywhere would be satisfied by "## Context", which is
// how a document with no title at all passes a careless check.
func hasTitle(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") && strings.TrimSpace(trimmed[2:]) != "" {
			return true
		}
	}
	return false
}

// containsHeading reports whether text carries a heading at the start of
// some line. A heading inside a code fence or mid-sentence is not one.
func containsHeading(text, heading string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == heading {
			return true
		}
	}
	return false
}

// Template renders the skeleton an ADR implementer fills in. It carries
// the candidate's question verbatim so the finished document answers what
// it was allocated for.
func Template(c Candidate, r Record, date string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n- **Status:** Accepted\n- **Date:** %s\n\n", c.Title, date)
	b.WriteString("## Context\n\n")
	fmt.Fprintf(&b, "%s\n\n", c.Question)
	b.WriteString("## Decision\n\n")
	fmt.Fprintf(&b, "We will %s.\n\n", r.Choice)
	if strings.TrimSpace(r.Rationale) != "" {
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(r.Rationale))
	}
	b.WriteString("## Consequences\n\n")
	b.WriteString("What this costs and what it enables. Include the bad parts.\n")
	return b.String()
}
