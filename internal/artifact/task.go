package artifact

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNoChecklist reports a kind whose documents carry no checkbox region.
var ErrNoChecklist = errors.New("artifact: kind carries no checklist")

// ErrNoSuchItem reports a checkoff naming an item the checklist does not
// have.
var ErrNoSuchItem = errors.New("artifact: checklist has no such item")

// Item is one checkbox line of a checklist. Index is the 1-based position
// among checkbox lines — the stable handle an accepted assignment names —
// while Line is the 0-based position among all lines of the region, which
// is what CheckOff rewrites so surrounding prose is preserved byte for
// byte.
type Item struct {
	Index int
	Line  int
	Done  bool
	Text  string
}

// itemPrefixes are the accepted list bullets. Checkbox syntax is
// deliberately narrow: a bullet, one space, "[ ]" or "[x]", one space.
var itemPrefixes = []string{"- ", "* "}

// parseItem parses one line as a checkbox item, returning ok=false for
// every other line (prose, headings, plain list items).
func parseItem(line string) (done bool, text string, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
	for _, p := range itemPrefixes {
		rest, cut := strings.CutPrefix(trimmed, p)
		if !cut {
			continue
		}
		switch {
		case strings.HasPrefix(rest, "[ ] "):
			return false, rest[4:], true
		case strings.HasPrefix(rest, "[x] "):
			return true, rest[4:], true
		}
		return false, "", false
	}
	return false, "", false
}

// ParseChecklist returns the checkbox items of a region's content in
// order. Lines that are not checkbox items are not items and are never
// renumbered or rewritten.
func ParseChecklist(content []byte) []Item {
	var items []Item
	for i, line := range strings.Split(string(content), "\n") {
		done, text, ok := parseItem(line)
		if !ok {
			continue
		}
		items = append(items, Item{Index: len(items) + 1, Line: i, Done: done, Text: text})
	}
	return items
}

// CheckOff marks the named 1-based items done in content, rewriting only
// the "[ ]" of those lines. It is idempotent — an already-checked item is
// left alone rather than refused, so re-applying a journaled checkoff
// after a crash converges — and it never unchecks: unchecking is not an
// operation Homonto has.
func CheckOff(content []byte, indexes []int) ([]byte, error) {
	items := ParseChecklist(content)
	byIndex := make(map[int]Item, len(items))
	for _, it := range items {
		byIndex[it.Index] = it
	}
	todo := make(map[int]bool, len(indexes))
	for _, idx := range indexes {
		it, ok := byIndex[idx]
		if !ok {
			return nil, fmt.Errorf("artifact: item %d of %d: %w", idx, len(items), ErrNoSuchItem)
		}
		if it.Done {
			continue
		}
		todo[it.Line] = true
	}
	if len(todo) == 0 {
		return content, nil
	}
	lines := strings.Split(string(content), "\n")
	for line := range todo {
		lines[line] = strings.Replace(lines[line], "[ ] ", "[x] ", 1)
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// checklistRegion returns the region a kind keeps its checkboxes in. A
// task document has a dedicated checklist region; a change's tasks.md is
// a whole-document region whose checkboxes live among its prose.
func checklistRegion(k Kind) (Region, bool) {
	switch k {
	case KindTaskDocument:
		return RegionTaskChecklist, true
	case KindTasks:
		return RegionWholeDocument, true
	}
	return "", false
}

// Checklist returns the checkbox items of a document's checklist region.
func (s *Service) Checklist(ctx context.Context, ref Ref) ([]Item, error) {
	region, ok := checklistRegion(ref.Kind)
	if !ok {
		return nil, fmt.Errorf("artifact: %s: %w", ref.Kind, ErrNoChecklist)
	}
	doc, err := s.Read(ctx, ref)
	if err != nil {
		return nil, err
	}
	return ParseChecklist(doc.Region(region)), nil
}

// CheckOff checks the named items of a document's checklist. It is a
// binary-owned write: the ownership table must give the binary the
// checklist region in phase, which it does in Task's Do and a change's
// Build, and nowhere else.
func (s *Service) CheckOff(ctx context.Context, ref Ref, phase Phase, indexes []int) (Snapshot, error) {
	region, ok := checklistRegion(ref.Kind)
	if !ok {
		return Snapshot{}, fmt.Errorf("artifact: %s: %w", ref.Kind, ErrNoChecklist)
	}
	doc, err := s.Read(ctx, ref)
	if err != nil {
		return Snapshot{}, err
	}
	updated, err := CheckOff(doc.Region(region), indexes)
	if err != nil {
		return Snapshot{}, err
	}
	return s.WriteGenerated(ctx, ref, phase, []RegionContent{{Region: region, Content: updated}})
}

// AppendEvidence appends a block to a task document's evidence region. It
// is a binary-owned write available in Done only; evidence accumulates and
// is never rewritten, so each call adds to what previous calls recorded.
func (s *Service) AppendEvidence(ctx context.Context, ref Ref, phase Phase, block []byte) (Snapshot, error) {
	if ref.Kind != KindTaskDocument {
		return Snapshot{}, fmt.Errorf("artifact: %s has no evidence region: %w", ref.Kind, ErrRegionNotGranted)
	}
	doc, err := s.Read(ctx, ref)
	if err != nil {
		return Snapshot{}, err
	}
	existing := doc.Region(RegionTaskEvidence)
	var buf bytes.Buffer
	if len(existing) > 0 {
		buf.Write(existing)
		if existing[len(existing)-1] != '\n' {
			buf.WriteByte('\n')
		}
		buf.WriteByte('\n')
	}
	buf.Write(block)
	return s.WriteGenerated(ctx, ref, phase, []RegionContent{
		{Region: RegionTaskEvidence, Content: buf.Bytes()},
	})
}

// SemanticChecklist returns the checklist with every checkbox reset to
// unchecked. It is for FINGERPRINTING, never for writing: the semantics of
// a checklist are its items and their order, and whether Homonto has
// checked one off is progress rather than a change of plan. Digesting the
// raw region instead would make Homonto's own checkoffs invalidate the
// plan that produced them.
func SemanticChecklist(content []byte) []byte {
	if len(content) == 0 {
		return nil
	}
	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		if _, _, ok := parseItem(line); !ok {
			continue
		}
		lines[i] = strings.Replace(line, "[x] ", "[ ] ", 1)
	}
	return []byte(strings.Join(lines, "\n"))
}
