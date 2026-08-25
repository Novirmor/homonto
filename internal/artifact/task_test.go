package artifact

import (
	"errors"
	"strings"
	"testing"
)

func TestParseChecklistFindsOnlyCheckboxLines(t *testing.T) {
	content := []byte(strings.Join([]string{
		"## Steps",
		"",
		"- [ ] reproduce the bug",
		"- [x] write the failing test",
		"- a plain list item, not a checkbox",
		"* [ ] a star bullet counts",
		"  - [ ] an indented item counts",
		"- [X] uppercase X is not the canonical spelling",
		"- [] no space is not a checkbox",
		"",
	}, "\n"))
	items := ParseChecklist(content)
	if len(items) != 4 {
		t.Fatalf("ParseChecklist found %d items, want 4: %+v", len(items), items)
	}
	want := []Item{
		{Index: 1, Line: 2, Done: false, Text: "reproduce the bug"},
		{Index: 2, Line: 3, Done: true, Text: "write the failing test"},
		{Index: 3, Line: 5, Done: false, Text: "a star bullet counts"},
		{Index: 4, Line: 6, Done: false, Text: "an indented item counts"},
	}
	for i, w := range want {
		if items[i] != w {
			t.Errorf("item %d = %+v, want %+v", i, items[i], w)
		}
	}
}

func TestCheckOffMarksOnlyTheNamedItems(t *testing.T) {
	content := []byte("## Steps\n\n- [ ] one\n- [ ] two\nprose in between\n- [ ] three\n")
	got, err := CheckOff(content, []int{1, 3})
	if err != nil {
		t.Fatalf("CheckOff: %v", err)
	}
	want := "## Steps\n\n- [x] one\n- [ ] two\nprose in between\n- [x] three\n"
	if string(got) != want {
		t.Fatalf("CheckOff =\n%q\nwant\n%q", got, want)
	}
}

func TestCheckOffIsIdempotentAndNeverUnchecks(t *testing.T) {
	content := []byte("- [x] done\n- [ ] open\n")
	once, err := CheckOff(content, []int{1, 2})
	if err != nil {
		t.Fatalf("CheckOff: %v", err)
	}
	twice, err := CheckOff(once, []int{1, 2})
	if err != nil {
		t.Fatalf("second CheckOff: %v", err)
	}
	if string(once) != string(twice) {
		t.Fatalf("CheckOff is not idempotent:\n%q\nvs\n%q", once, twice)
	}
	if strings.Contains(string(twice), "[ ]") {
		t.Fatalf("an item is still open after checking both: %q", twice)
	}
	// Checking nothing leaves the content untouched, byte for byte.
	same, err := CheckOff(content, nil)
	if err != nil {
		t.Fatalf("CheckOff(nil): %v", err)
	}
	if string(same) != string(content) {
		t.Fatalf("CheckOff(nil) changed the content: %q", same)
	}
}

func TestCheckOffRefusesUnknownItems(t *testing.T) {
	content := []byte("- [ ] one\n- [ ] two\n")
	for _, idx := range []int{0, 3, -1} {
		if _, err := CheckOff(content, []int{idx}); !errors.Is(err, ErrNoSuchItem) {
			t.Errorf("CheckOff(%d) error = %v, want ErrNoSuchItem", idx, err)
		}
	}
	// A refused checkoff changes nothing, even when some indexes are valid.
	got, err := CheckOff(content, []int{1, 9})
	if !errors.Is(err, ErrNoSuchItem) {
		t.Fatalf("CheckOff error = %v, want ErrNoSuchItem", err)
	}
	if got != nil {
		t.Fatalf("refused CheckOff returned content: %q", got)
	}
}

func TestServiceCheckOffIsBinaryOwned(t *testing.T) {
	svc, root := newService(t)
	ref := newTask(t, svc, "fix-login")
	editRegion(t, root, ref, RegionTaskChecklist, "- [ ] reproduce\n- [ ] fix\n")

	// Plan is the host's phase; the binary does not check items there.
	if _, err := svc.CheckOff(t.Context(), ref, PhasePlan, []int{1}); !errors.Is(err, ErrNotEditable) {
		t.Fatalf("CheckOff in Plan = %v, want ErrNotEditable", err)
	}
	if _, err := svc.CheckOff(t.Context(), ref, PhaseDo, []int{1}); err != nil {
		t.Fatalf("CheckOff in Do: %v", err)
	}
	items, err := svc.Checklist(t.Context(), ref)
	if err != nil {
		t.Fatalf("Checklist: %v", err)
	}
	if len(items) != 2 || !items[0].Done || items[1].Done {
		t.Fatalf("checklist = %+v, want only the first item done", items)
	}
}

func TestChecklistRefusesKindsWithoutOne(t *testing.T) {
	svc, _ := newService(t)
	ref := newProposal(t, svc, "rework-catalog")
	if _, err := svc.Checklist(t.Context(), ref); !errors.Is(err, ErrNoChecklist) {
		t.Fatalf("Checklist(proposal) error = %v, want ErrNoChecklist", err)
	}
	if _, err := svc.CheckOff(t.Context(), ref, PhaseBuild, []int{1}); !errors.Is(err, ErrNoChecklist) {
		t.Fatalf("CheckOff(proposal) error = %v, want ErrNoChecklist", err)
	}
}

// TestChangeTasksCheckOffPreservesProse proves a change's tasks.md keeps
// its headings and prose when the binary checks items off in Build.
func TestChangeTasksCheckOffPreservesProse(t *testing.T) {
	svc, root := newService(t)
	workID := mustWorkID(t)
	path, err := Path("rework-catalog", KindTasks)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if _, err := svc.Create(t.Context(), path, Metadata{
		Schema: MetadataSchema, WorkID: workID, Name: "rework-catalog", Kind: KindTasks,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	ref := Ref{WorkID: workID, Kind: KindTasks, Path: path}
	body := "## Tasks\n\n### Task 1\n\n- [ ] write the failing test\n- [ ] implement\n\n### Task 2\n\n- [ ] wire the CLI\n"
	editRegion(t, root, ref, RegionWholeDocument, body)

	if _, err := svc.CheckOff(t.Context(), ref, PhaseBuild, []int{2}); err != nil {
		t.Fatalf("CheckOff: %v", err)
	}
	doc, err := svc.Read(t.Context(), ref)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got := string(doc.Region(RegionWholeDocument))
	want := "## Tasks\n\n### Task 1\n\n- [ ] write the failing test\n- [x] implement\n\n### Task 2\n\n- [ ] wire the CLI\n"
	if got != want {
		t.Fatalf("tasks.md =\n%q\nwant\n%q", got, want)
	}
}

func TestAppendEvidenceAccumulates(t *testing.T) {
	svc, _ := newService(t)
	ref := newTask(t, svc, "fix-login")

	if _, err := svc.AppendEvidence(t.Context(), ref, PhaseDone, []byte("checks: go test ./... passed\n")); err != nil {
		t.Fatalf("first AppendEvidence: %v", err)
	}
	if _, err := svc.AppendEvidence(t.Context(), ref, PhaseDone, []byte("review: no blocking findings\n")); err != nil {
		t.Fatalf("second AppendEvidence: %v", err)
	}
	doc, err := svc.Read(t.Context(), ref)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got := string(doc.Region(RegionTaskEvidence))
	want := "checks: go test ./... passed\n\nreview: no blocking findings\n"
	if got != want {
		t.Fatalf("evidence =\n%q\nwant\n%q", got, want)
	}
}

func TestAppendEvidenceIsDoneOnlyAndTaskOnly(t *testing.T) {
	svc, _ := newService(t)
	task := newTask(t, svc, "fix-login")
	if _, err := svc.AppendEvidence(t.Context(), task, PhaseDo, []byte("early\n")); !errors.Is(err, ErrRegionNotGranted) {
		t.Fatalf("AppendEvidence in Do error = %v, want ErrRegionNotGranted", err)
	}
	proposal := newProposal(t, svc, "rework-catalog")
	if _, err := svc.AppendEvidence(t.Context(), proposal, PhaseDone, []byte("x\n")); !errors.Is(err, ErrRegionNotGranted) {
		t.Fatalf("AppendEvidence(proposal) error = %v, want ErrRegionNotGranted", err)
	}
}

func FuzzCheckOff(f *testing.F) {
	f.Add("- [ ] one\n- [x] two\n", 1)
	f.Add("## Heading\n\nprose\n", 1)
	f.Add("", 0)
	f.Fuzz(func(t *testing.T, content string, index int) {
		before := ParseChecklist([]byte(content))
		got, err := CheckOff([]byte(content), []int{index})
		if err != nil {
			if !errors.Is(err, ErrNoSuchItem) {
				t.Fatalf("untyped CheckOff error: %v", err)
			}
			return
		}
		after := ParseChecklist(got)
		if len(after) != len(before) {
			t.Fatalf("CheckOff changed the item count: %d -> %d", len(before), len(after))
		}
		for i := range before {
			// CheckOff never unchecks, never renumbers, and never edits text.
			if before[i].Done && !after[i].Done {
				t.Fatalf("item %d was unchecked", i+1)
			}
			if before[i].Text != after[i].Text || before[i].Line != after[i].Line {
				t.Fatalf("item %d moved or changed text: %+v -> %+v", i+1, before[i], after[i])
			}
		}
	})
}
