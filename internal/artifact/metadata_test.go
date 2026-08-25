package artifact

import (
	"errors"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
)

// testWorkID is a fixed canonical work id; tests that only need "a valid
// id" use it so failures read as content differences, not random noise.
const testWorkID = identity.WorkID("6ba7b810-9dad-41d4-80b4-00c04fd430c8")

func taskMeta() Metadata {
	return Metadata{Schema: MetadataSchema, WorkID: testWorkID, Name: "fix-login", Kind: KindTaskDocument}
}

func TestRenderMetadataIsCanonicalSingleLine(t *testing.T) {
	b, err := RenderMetadata(taskMeta())
	if err != nil {
		t.Fatalf("RenderMetadata: %v", err)
	}
	got := string(b)
	want := `<!-- homonto: {"schema":1,"work_id":"6ba7b810-9dad-41d4-80b4-00c04fd430c8","name":"fix-login","kind":"task"} -->`
	if got != want {
		t.Fatalf("RenderMetadata =\n%s\nwant\n%s", got, want)
	}
}

func TestParseMetadataRoundTrips(t *testing.T) {
	b, err := RenderMetadata(taskMeta())
	if err != nil {
		t.Fatalf("RenderMetadata: %v", err)
	}
	got, err := ParseMetadata(append(b, '\n'))
	if err != nil {
		t.Fatalf("ParseMetadata: %v", err)
	}
	if got != taskMeta() {
		t.Fatalf("ParseMetadata = %+v, want %+v", got, taskMeta())
	}
}

func TestParseMetadataRejectsTampering(t *testing.T) {
	line := `<!-- homonto: {"schema":1,"work_id":"6ba7b810-9dad-41d4-80b4-00c04fd430c8","name":"fix-login","kind":"task"} -->`
	tests := []struct {
		name string
		doc  string
		want error
	}{
		{"no metadata at all", "# just a heading\n", ErrMissingMetadata},
		{"empty document", "", ErrMissingMetadata},
		{"metadata not on the first line", "# heading\n" + line + "\n", ErrMissingMetadata},
		{"unterminated comment", `<!-- homonto: {"schema":1}` + "\n", ErrTamperedMetadata},
		{"unknown field", `<!-- homonto: {"schema":1,"work_id":"6ba7b810-9dad-41d4-80b4-00c04fd430c8","name":"fix-login","kind":"task","extra":1} -->` + "\n", ErrTamperedMetadata},
		{"trailing JSON", `<!-- homonto: {"schema":1,"work_id":"6ba7b810-9dad-41d4-80b4-00c04fd430c8","name":"fix-login","kind":"task"}{} -->` + "\n", ErrTamperedMetadata},
		{"wrong schema", `<!-- homonto: {"schema":2,"work_id":"6ba7b810-9dad-41d4-80b4-00c04fd430c8","name":"fix-login","kind":"task"} -->` + "\n", ErrTamperedMetadata},
		{"uppercase work id", `<!-- homonto: {"schema":1,"work_id":"6BA7B810-9DAD-41D4-80B4-00C04FD430C8","name":"fix-login","kind":"task"} -->` + "\n", ErrTamperedMetadata},
		{"invalid work name", `<!-- homonto: {"schema":1,"work_id":"6ba7b810-9dad-41d4-80b4-00c04fd430c8","name":"Fix Login","kind":"task"} -->` + "\n", ErrTamperedMetadata},
		{"unknown kind", `<!-- homonto: {"schema":1,"work_id":"6ba7b810-9dad-41d4-80b4-00c04fd430c8","name":"fix-login","kind":"invoice"} -->` + "\n", ErrTamperedMetadata},
		{"reserved work name", `<!-- homonto: {"schema":1,"work_id":"6ba7b810-9dad-41d4-80b4-00c04fd430c8","name":"archive","kind":"task"} -->` + "\n", ErrTamperedMetadata},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseMetadata([]byte(tt.doc))
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParseMetadata error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestRenderParseRoundTripsTaskDocument proves Render and Parse are
// inverses on the canonical form — the property the grant digests rest on.
func TestRenderParseRoundTripsTaskDocument(t *testing.T) {
	doc := NewDocument(taskMeta())
	doc.Regions = []RegionContent{
		{Region: RegionTaskGoal, Content: []byte("Make login work.\n")},
		{Region: RegionTaskChecklist, Content: []byte("- [ ] reproduce\n- [x] fix\n")},
		{Region: RegionTaskEvidence, Content: nil},
	}
	rendered, err := Render(doc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	back, err := Parse(rendered)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	again, err := Render(back)
	if err != nil {
		t.Fatalf("Render(Parse(x)): %v", err)
	}
	if string(again) != string(rendered) {
		t.Fatalf("Render(Parse(x)) =\n%s\nwant\n%s", again, rendered)
	}
	for _, r := range []Region{RegionTaskGoal, RegionTaskChecklist, RegionTaskEvidence} {
		if !equalContent(back.Region(r), doc.Region(r)) {
			t.Fatalf("region %q = %q, want %q", r, back.Region(r), doc.Region(r))
		}
	}
}

func TestParseRejectsBrokenRegionStructure(t *testing.T) {
	meta, err := RenderMetadata(taskMeta())
	if err != nil {
		t.Fatalf("RenderMetadata: %v", err)
	}
	head := string(meta) + "\n"
	goal := "<!-- homonto:begin task-goal -->\ngoal\n<!-- homonto:end task-goal -->\n"
	list := "<!-- homonto:begin task-checklist -->\n- [ ] a\n<!-- homonto:end task-checklist -->\n"
	ev := "<!-- homonto:begin task-evidence -->\n<!-- homonto:end task-evidence -->\n"
	tests := []struct {
		name string
		body string
	}{
		{"missing a region", goal + list},
		{"duplicate region", goal + goal + list + ev},
		{"regions out of canonical order", list + goal + ev},
		{"content outside every region", "stray line\n" + goal + list + ev},
		{"unclosed region", "<!-- homonto:begin task-goal -->\ngoal\n" + list + ev},
		{"mismatched end marker", "<!-- homonto:begin task-goal -->\ngoal\n<!-- homonto:end task-checklist -->\n" + list + ev},
		{"unknown region name", "<!-- homonto:begin task-notes -->\nx\n<!-- homonto:end task-notes -->\n" + goal + list + ev},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(head + tt.body))
			if !errors.Is(err, ErrTamperedDocument) {
				t.Fatalf("Parse error = %v, want ErrTamperedDocument", err)
			}
		})
	}
}

func TestParseRejectsMarkersInWholeDocumentKinds(t *testing.T) {
	meta := taskMeta()
	meta.Kind = KindProposal
	b, err := RenderMetadata(meta)
	if err != nil {
		t.Fatalf("RenderMetadata: %v", err)
	}
	doc := string(b) + "\n\n<!-- homonto:begin task-goal -->\nx\n<!-- homonto:end task-goal -->\n"
	if _, err := Parse([]byte(doc)); !errors.Is(err, ErrTamperedDocument) {
		t.Fatalf("Parse error = %v, want ErrTamperedDocument", err)
	}
}

func TestRenderRejectsDuplicateOrUnknownRegions(t *testing.T) {
	doc := NewDocument(taskMeta())
	doc.Regions = append(doc.Regions, RegionContent{Region: RegionTaskGoal})
	if _, err := Render(doc); err == nil {
		t.Fatal("Render with a duplicate region = nil error, want refusal")
	}
	doc = NewDocument(taskMeta())
	doc.Regions[0].Region = Region("task-notes")
	if _, err := Render(doc); err == nil {
		t.Fatal("Render with an unknown region = nil error, want refusal")
	}
}

// TestWholeDocumentRoundTrip proves a non-task document keeps its body and
// carries exactly one implicit region.
func TestWholeDocumentRoundTrip(t *testing.T) {
	meta := taskMeta()
	meta.Kind = KindProposal
	doc := NewDocument(meta)
	body := "## Why\n\nBecause.\n\n## What\n\n- one\n- two\n"
	doc.Regions = []RegionContent{{Region: RegionWholeDocument, Content: []byte(body)}}
	rendered, err := Render(doc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	back, err := Parse(rendered)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(back.Regions) != 1 || back.Regions[0].Region != RegionWholeDocument {
		t.Fatalf("regions = %+v, want a single whole-document region", back.Regions)
	}
	if string(back.Region(RegionWholeDocument)) != body {
		t.Fatalf("body = %q, want %q", back.Region(RegionWholeDocument), body)
	}
	if !strings.Contains(string(rendered), "## Why") {
		t.Fatal("rendered document lost its body")
	}
}

func FuzzParse(f *testing.F) {
	doc := NewDocument(taskMeta())
	doc.Regions = []RegionContent{
		{Region: RegionTaskGoal, Content: []byte("goal\n")},
		{Region: RegionTaskChecklist, Content: []byte("- [ ] a\n")},
		{Region: RegionTaskEvidence, Content: nil},
	}
	if b, err := Render(doc); err == nil {
		f.Add(b)
	}
	f.Add([]byte(""))
	f.Add([]byte("<!-- homonto: {} -->\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		parsed, err := Parse(in)
		if err != nil {
			// Every rejection must be one of the typed document errors,
			// never a bare or panicking failure.
			if !errors.Is(err, ErrMissingMetadata) &&
				!errors.Is(err, ErrTamperedMetadata) &&
				!errors.Is(err, ErrTamperedDocument) {
				t.Fatalf("untyped parse error: %v", err)
			}
			return
		}
		// Anything that parses must render back and re-parse identically.
		rendered, err := Render(parsed)
		if err != nil {
			t.Fatalf("Render of a parsed document failed: %v", err)
		}
		again, err := Parse(rendered)
		if err != nil {
			t.Fatalf("re-parse of a rendered document failed: %v", err)
		}
		second, err := Render(again)
		if err != nil {
			t.Fatalf("second render failed: %v", err)
		}
		if string(second) != string(rendered) {
			t.Fatalf("render is not idempotent:\n%q\nvs\n%q", second, rendered)
		}
	})
}
