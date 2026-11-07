// Package deltamerge applies an onto delta spec onto a living capability spec,
// deterministically — the RENAMED → MODIFIED → REMOVED → ADDED merge that
// onto-close otherwise performs by hand (the workflow's most destructive step).
//
// A living spec is `## Requirements` followed by `### Requirement: <name>`
// blocks. A delta groups those blocks under `## ADDED|MODIFIED|REMOVED|RENAMED
// Requirements` sections. Merge applies the four in the fixed order so a
// MODIFIED targeting a just-RENAMED name resolves, then returns the merged
// living spec — carrying no delta-only section headings and no duplicate
// requirement name (Lint verifies both).
package deltamerge

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrAlreadyExists marks an ADDED name that the living spec already carries.
// Callers pair it with Applied to recognize a prior round's completed work
// without weakening the loud failure for genuinely conflicting content.
var ErrAlreadyExists = errors.New("requirement already exists")

var (
	reqHeading     = regexp.MustCompile(`^### Requirement:\s*(.+?)\s*$`)
	sectionHeading = regexp.MustCompile(`^##\s+(ADDED|MODIFIED|REMOVED|RENAMED)\s+Requirements\s*$`)
	topHeading     = regexp.MustCompile(`^##\s+`)
	fromLine       = regexp.MustCompile(`^\s*-?\s*FROM:\s*(.+?)\s*$`)
	toLine         = regexp.MustCompile(`^\s*TO:\s*(.+?)\s*$`)
)

type requirement struct {
	name  string
	block []string // lines, starting with the "### Requirement: <name>" heading
}

// Merge applies delta onto living (the current living spec for capability, or ""
// when none exists) and returns the merged living spec. It errors when a
// MODIFIED/REMOVED name or a RENAMED FROM is absent, or an ADDED name already
// exists — the delta references reality that isn't there. Strictness is the
// point: a typo'd name fails loudly instead of silently merging around it. For
// crash recovery, callers pair Merge with Applied — a delta that no longer
// merges may already be fully applied.
func Merge(capability, living, delta string) (string, error) {
	renamed, modified, removed, added, err := parseDelta(delta)
	if err != nil {
		return "", err
	}
	preamble, reqs, trailer := splitLiving(capability, living)

	find := func(name string) int {
		for i, r := range reqs {
			if r.name == name {
				return i
			}
		}
		return -1
	}

	// 1. RENAMED — rename the heading, keep the body.
	for _, ft := range renamed {
		i := find(ft[0])
		if i < 0 {
			return "", fmt.Errorf("deltamerge: RENAMED FROM %q not found in living spec %q", ft[0], capability)
		}
		reqs[i].name = ft[1]
		reqs[i].block[0] = "### Requirement: " + ft[1]
	}
	// 2. MODIFIED — replace the whole requirement.
	for _, m := range modified {
		i := find(m.name)
		if i < 0 {
			return "", fmt.Errorf("deltamerge: MODIFIED %q not found in living spec %q", m.name, capability)
		}
		reqs[i] = m
	}
	// 3. REMOVED — delete the named requirement.
	for _, rm := range removed {
		i := find(rm.name)
		if i < 0 {
			return "", fmt.Errorf("deltamerge: REMOVED %q not found in living spec %q", rm.name, capability)
		}
		reqs = append(reqs[:i], reqs[i+1:]...)
	}
	// 4. ADDED — append; a name that already exists is a conflict.
	for _, a := range added {
		if find(a.name) >= 0 {
			return "", fmt.Errorf("deltamerge: ADDED %q already exists in living spec %q: %w", a.name, capability, ErrAlreadyExists)
		}
		reqs = append(reqs, a)
	}

	return assemble(preamble, reqs, trailer), nil
}

// Applied reports whether living is already in delta's POST-state: every
// RENAMED's FROM is gone and its TO present, every MODIFIED and ADDED name is
// present with byte-identical content, and every REMOVED name is absent.
//
// This is the crash-recovery half of Merge's strictness. The caller (onto
// merge-deltas) writes one living spec at a time, each write atomic — so a
// crash between writes leaves each spec either untouched (Merge still applies)
// or fully merged (Applied is true). Without this predicate a partial run
// poisoned the merged specs against every re-run ("ADDED %q already exists",
// forever), and the only escape was hand-asserting close.merged — the exact
// flag the command exists to make trustworthy. A delta that neither merges nor
// is applied (a typo'd name, a hand-edited spec) still fails loudly.
func Applied(capability, living, delta string) bool {
	renamed, modified, removed, added, err := parseDelta(delta)
	if err != nil {
		return false
	}
	_, reqs, _ := splitLiving(capability, living)
	byName := make(map[string]requirement, len(reqs))
	for _, r := range reqs {
		byName[r.name] = r
	}
	same := func(want requirement) bool {
		got, ok := byName[want.name]
		return ok && strings.Join(got.block, "\n") == strings.Join(want.block, "\n")
	}
	for _, ft := range renamed {
		if _, fromPresent := byName[ft[0]]; fromPresent {
			return false
		}
		if _, toPresent := byName[ft[1]]; !toPresent {
			return false
		}
	}
	for _, m := range modified {
		if !same(m) {
			return false
		}
	}
	for _, rm := range removed {
		if _, present := byName[rm.name]; present {
			return false
		}
	}
	for _, a := range added {
		if !same(a) {
			return false
		}
	}
	return true
}

// Lint checks a merged living spec for the two failure modes the merge must
// never produce: a leaked delta-only section heading, and a duplicated
// requirement name.
func Lint(merged string) []string {
	var findings []string
	seen := map[string]bool{}
	lines := strings.Split(merged, "\n")
	fence := fencedLines(lines)
	for i, ln := range lines {
		if fence[i] {
			continue
		}
		if sectionHeading.MatchString(ln) {
			findings = append(findings, "leaked delta section heading: "+strings.TrimSpace(ln))
		}
		if m := reqHeading.FindStringSubmatch(ln); m != nil {
			if seen[m[1]] {
				findings = append(findings, "duplicated requirement: "+m[1])
			}
			seen[m[1]] = true
		}
	}
	return findings
}

// splitLiving separates the living spec into its preamble (title/prose through
// the `## Requirements` heading), its requirement blocks, and any trailing
// level-2+ sections after the requirements (Rationale, Notes, References, …).
// Trailing sections are preserved verbatim through a merge so prose that lives
// after the requirements is never silently dropped (F5). For an empty living
// spec it synthesizes a minimal preamble titled from the capability.
func splitLiving(capability, living string) (preamble []string, reqs []requirement, trailer []string) {
	if strings.TrimSpace(living) == "" {
		return []string{"# " + capability, "", "## Requirements"}, nil, nil
	}
	lines := strings.Split(strings.ReplaceAll(living, "\r\n", "\n"), "\n")
	fence := fencedLines(lines)
	isReq := func(i int) bool { return !fence[i] && reqHeading.MatchString(lines[i]) }
	// The preamble ends at the first requirement, or at the first section after
	// `## Requirements` when there is none yet, so ADDED never lands after it.
	requirementsHeading := false
	i := 0
	for i < len(lines) && !isReq(i) {
		if !fence[i] && topHeading.MatchString(lines[i]) {
			if requirementsHeading {
				break
			}
			requirementsHeading = strings.TrimSpace(lines[i]) == "## Requirements"
		}
		preamble = append(preamble, lines[i])
		i++
	}
	for i < len(lines) {
		if isReq(i) {
			m := reqHeading.FindStringSubmatch(lines[i])
			block := []string{lines[i]}
			i++
			for i < len(lines) && !isReq(i) && (fence[i] || !topHeading.MatchString(lines[i])) {
				block = append(block, lines[i])
				i++
			}
			reqs = append(reqs, requirement{name: m[1], block: trimBlock(block)})
		} else {
			// A level-2+ section after the requirements: capture the rest of the
			// document verbatim as the trailer and stop scanning for requirements.
			trailer = append(trailer, lines[i:]...)
			break
		}
	}
	return preamble, reqs, trailer
}

// parseDelta reads the four delta sections. ADDED/MODIFIED yield requirement
// blocks; REMOVED yields names; RENAMED yields FROM/TO pairs.
func parseDelta(delta string) (renamed [][2]string, modified, removed []requirement, added []requirement, err error) {
	// note: removed carries names in requirement.name (block unused).
	lines := strings.Split(strings.ReplaceAll(delta, "\r\n", "\n"), "\n")
	p := deltaParser{lines: lines, fence: fencedLines(lines), seen: map[string]bool{}, ops: map[string]int{}}
	for p.i < len(p.lines) {
		if err := p.step(); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if p.pendingFrom != "" {
		return nil, nil, nil, nil, fmt.Errorf("deltamerge: RENAMED FROM %q has no TO", p.pendingFrom)
	}
	if p.section != "" && p.ops[p.section] == 0 {
		return nil, nil, nil, nil, fmt.Errorf("deltamerge: %s section has no operations", p.section)
	}
	if len(p.renamed)+len(p.modified)+len(p.removed)+len(p.added) == 0 {
		return nil, nil, nil, nil, fmt.Errorf("deltamerge: delta has no operations")
	}
	return p.renamed, p.modified, p.removed, p.added, nil
}

// deltaParser walks a delta line by line, tracking the open section and a
// RENAMED FROM awaiting its TO.
type deltaParser struct {
	lines       []string
	fence       []bool
	i           int
	section     string
	pendingFrom string
	seen        map[string]bool
	ops         map[string]int

	renamed                  [][2]string
	modified, removed, added []requirement
}

// step consumes the line at p.i, and for a requirement block every line in it.
func (p *deltaParser) step() error {
	ln := p.lines[p.i]
	if p.fence[p.i] {
		if p.section == "RENAMED" {
			return fmt.Errorf("deltamerge: invalid RENAMED line %q", strings.TrimSpace(ln))
		}
		p.i++
		return nil
	}
	if m := sectionHeading.FindStringSubmatch(ln); m != nil {
		p.i++
		return p.openSection(m[1])
	}
	if topHeading.MatchString(ln) {
		return fmt.Errorf("deltamerge: unsupported section %q", strings.TrimSpace(ln))
	}
	if reqHeading.MatchString(ln) && p.section == "" {
		return fmt.Errorf("deltamerge: requirement appears outside a delta section")
	}
	switch p.section {
	case "ADDED", "MODIFIED":
		if m := reqHeading.FindStringSubmatch(ln); m != nil {
			p.requirementBlock(m[1])
			return nil
		}
	case "REMOVED":
		if m := reqHeading.FindStringSubmatch(ln); m != nil {
			p.removed = append(p.removed, requirement{name: m[1]})
			p.ops[p.section]++
		}
	case "RENAMED":
		if err := p.renameLine(ln); err != nil {
			return err
		}
	}
	p.i++
	return nil
}

// openSection closes the current section, which must hold an operation and no
// dangling FROM, and opens name, which must be new.
func (p *deltaParser) openSection(name string) error {
	if p.section != "" && p.ops[p.section] == 0 {
		return fmt.Errorf("deltamerge: %s section has no operations", p.section)
	}
	if p.pendingFrom != "" {
		return fmt.Errorf("deltamerge: RENAMED FROM %q has no TO", p.pendingFrom)
	}
	if p.seen[name] {
		return fmt.Errorf("deltamerge: duplicate %s section", name)
	}
	p.section = name
	p.seen[name] = true
	p.pendingFrom = ""
	return nil
}

// requirementBlock consumes an ADDED or MODIFIED requirement: its heading and
// every line up to the next requirement or section heading outside a fence.
func (p *deltaParser) requirementBlock(name string) {
	block := []string{p.lines[p.i]}
	p.i++
	for p.i < len(p.lines) && (p.fence[p.i] || (!reqHeading.MatchString(p.lines[p.i]) && !topHeading.MatchString(p.lines[p.i]))) {
		block = append(block, p.lines[p.i])
		p.i++
	}
	r := requirement{name: name, block: trimBlock(block)}
	if p.section == "ADDED" {
		p.added = append(p.added, r)
	} else {
		p.modified = append(p.modified, r)
	}
	p.ops[p.section]++
}

// renameLine reads one RENAMED line: a FROM, the TO completing it, or blank.
func (p *deltaParser) renameLine(ln string) error {
	if m := fromLine.FindStringSubmatch(ln); m != nil {
		if p.pendingFrom != "" {
			return fmt.Errorf("deltamerge: RENAMED FROM %q has no TO", p.pendingFrom)
		}
		p.pendingFrom = m[1]
		return nil
	}
	if m := toLine.FindStringSubmatch(ln); m != nil {
		if p.pendingFrom == "" {
			return fmt.Errorf("deltamerge: RENAMED TO %q has no preceding FROM", m[1])
		}
		p.renamed = append(p.renamed, [2]string{p.pendingFrom, m[1]})
		p.ops[p.section]++
		p.pendingFrom = ""
		return nil
	}
	if strings.TrimSpace(ln) != "" {
		return fmt.Errorf("deltamerge: invalid RENAMED line %q", strings.TrimSpace(ln))
	}
	return nil
}

// fencedLines marks each line that opens, sits inside, or closes a fenced code
// block, so example Markdown in a requirement body is never read as structure.
// A fence closes on a bare run of its own character at least as long.
func fencedLines(lines []string) []bool {
	out := make([]bool, len(lines))
	open := ""
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if open != "" {
			out[i] = true
			if strings.HasPrefix(line, open) && strings.Trim(line, open[:1]) == "" {
				open = ""
			}
			continue
		}
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			out[i] = true
			open = line[:len(line)-len(strings.TrimLeft(line, line[:1]))]
		}
	}
	return out
}

// trimBlock removes trailing blank lines from a requirement block.
func trimBlock(block []string) []string {
	for len(block) > 0 && strings.TrimSpace(block[len(block)-1]) == "" {
		block = block[:len(block)-1]
	}
	return block
}

// assemble reconstitutes the living spec: the preamble, then each requirement
// block, then any trailing sections, separated by a single blank line, ending
// with one newline. The trailer is emitted verbatim after the requirements so
// Rationale/Notes/References prose survives the merge (F5).
func assemble(preamble []string, reqs []requirement, trailer []string) string {
	var b strings.Builder
	pre := strings.TrimRight(strings.Join(preamble, "\n"), "\n")
	b.WriteString(pre)
	b.WriteString("\n")
	for _, r := range reqs {
		b.WriteString("\n")
		b.WriteString(strings.Join(r.block, "\n"))
		b.WriteString("\n")
	}
	if len(trailer) > 0 {
		// Trim a leading blank line so the trailer's own H2 heading gets the same
		// single-blank-line separator the requirements use; preserve its body and
		// final newline as written.
		t := strings.TrimPrefix(strings.Join(trailer, "\n"), "\n")
		t = strings.TrimPrefix(t, "\n")
		b.WriteString("\n")
		b.WriteString(t)
		if !strings.HasSuffix(t, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}
