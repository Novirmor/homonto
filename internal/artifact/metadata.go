package artifact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workname"
)

// MetadataSchema is the only schema version this binary reads and writes.
const MetadataSchema = 1

// Typed document errors. Callers branch with errors.Is; every malformed
// document failure wraps exactly one of these.
var (
	// ErrMissingMetadata: the document carries no metadata block at all.
	ErrMissingMetadata = errors.New("artifact: document has no homonto metadata block")
	// ErrTamperedMetadata: a metadata block is present but is not the
	// canonical single-line strict-JSON form with valid field values.
	ErrTamperedMetadata = errors.New("artifact: metadata block is malformed or carries invalid values")
	// ErrTamperedDocument: the region structure is broken — unknown or
	// mismatched markers, duplicate or missing regions, or stray content
	// outside every region.
	ErrTamperedDocument = errors.New("artifact: document region structure is malformed")
)

// Metadata is the identity block of a document: which work it belongs to,
// its normalized name, and its kind. It is immutable for the document's
// whole life; every identity decision (archive lookup included) reads it
// and never the file name.
type Metadata struct {
	Schema int             `json:"schema"`
	WorkID identity.WorkID `json:"work_id"`
	Name   string          `json:"name"`
	Kind   Kind            `json:"kind"`
}

// Validate checks the metadata values: schema currency, canonical work id,
// valid work name, and a known kind.
func (m Metadata) Validate() error {
	if m.Schema != MetadataSchema {
		return fmt.Errorf("artifact: metadata schema %d, want exactly %d", m.Schema, MetadataSchema)
	}
	if err := identity.ValidateUUID(string(m.WorkID)); err != nil {
		return fmt.Errorf("artifact: metadata work_id: %w", err)
	}
	if err := workname.Validate(m.Name); err != nil {
		return fmt.Errorf("artifact: metadata name: %w", err)
	}
	if !m.Kind.known() {
		return fmt.Errorf("artifact: metadata kind %q is not a known document kind", m.Kind)
	}
	return nil
}

// metaPrefix and metaSuffix delimit the metadata comment; the single space
// after the colon is canonical.
const (
	metaPrefix = "<!-- homonto: "
	metaSuffix = " -->"
)

// RenderMetadata returns the canonical single-line metadata comment.
func RenderMetadata(m Metadata) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("artifact: marshal metadata: %w", err)
	}
	if bytes.ContainsAny(b, "\n\r") {
		return nil, fmt.Errorf("artifact: metadata JSON is not single-line")
	}
	return []byte(metaPrefix + string(b) + metaSuffix), nil
}

// ParseMetadata extracts and validates the metadata block of a document.
// The block must be the first line, exactly in canonical form: any other
// metadata-looking line elsewhere is tampering, and a first line that is
// not the block is a missing block (the document is not an artifact).
func ParseMetadata(doc []byte) (Metadata, error) {
	lines := splitLines(doc)
	if len(lines) == 0 || !strings.HasPrefix(lines[0], metaPrefix) {
		return Metadata{}, fmt.Errorf("artifact: %w", ErrMissingMetadata)
	}
	if !strings.HasSuffix(lines[0], metaSuffix) {
		return Metadata{}, fmt.Errorf("artifact: metadata line is malformed: %w", ErrTamperedMetadata)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(lines[0], metaPrefix), metaSuffix)
	var m Metadata
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Metadata{}, fmt.Errorf("artifact: metadata JSON: %w: %w", ErrTamperedMetadata, err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return Metadata{}, fmt.Errorf("artifact: metadata JSON: %w: %w", ErrTamperedMetadata, err)
	}
	if err := m.Validate(); err != nil {
		return Metadata{}, fmt.Errorf("artifact: %w: %w", ErrTamperedMetadata, err)
	}
	return m, nil
}

// RegionContent is one region's content. Nil and empty content are the
// same thing: an empty region.
type RegionContent struct {
	Region  Region
	Content []byte
}

// equalContent reports whether two region contents are equal, treating nil
// and empty as identical.
func equalContent(a, b []byte) bool {
	var aa, bb []byte
	if len(a) > 0 {
		aa = a
	}
	if len(b) > 0 {
		bb = b
	}
	return bytes.Equal(aa, bb)
}

// Document is a parsed artifact document: its metadata and its regions in
// canonical order.
type Document struct {
	Metadata Metadata
	Regions  []RegionContent
}

// NewDocument returns a document with metadata and empty regions of the
// kind's canonical set.
func NewDocument(meta Metadata) Document {
	kinds := regionsOf(meta.Kind)
	regions := make([]RegionContent, len(kinds))
	for i, r := range kinds {
		regions[i] = RegionContent{Region: r}
	}
	return Document{Metadata: meta, Regions: regions}
}

// Region returns the content of region r, or nil when absent.
func (d Document) Region(r Region) []byte {
	for _, rc := range d.Regions {
		if rc.Region == r {
			return rc.Content
		}
	}
	return nil
}

// hasRegion reports whether the document carries region r.
func (d Document) hasRegion(r Region) bool {
	for _, rc := range d.Regions {
		if rc.Region == r {
			return true
		}
	}
	return false
}

// Render returns the canonical byte form of the document. Render(Parse(x))
// equals x for every canonical x, and Parse is its inverse.
func Render(d Document) ([]byte, error) {
	if err := d.Metadata.Validate(); err != nil {
		return nil, err
	}
	meta, err := RenderMetadata(d.Metadata)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(meta)
	buf.WriteByte('\n')
	seen := make(map[Region]bool, len(d.Regions))
	for _, rc := range d.Regions {
		if !regionKnown(rc.Region) || seen[rc.Region] {
			return nil, fmt.Errorf("artifact: render: region %q is unknown or duplicated", rc.Region)
		}
		seen[rc.Region] = true
	}
	for _, rc := range d.Regions {
		if rc.Region == RegionWholeDocument {
			if len(d.Regions) != 1 {
				return nil, fmt.Errorf("artifact: render: whole-document must be the only region")
			}
			buf.WriteByte('\n')
			buf.Write(rc.Content)
			if len(rc.Content) > 0 && rc.Content[len(rc.Content)-1] != '\n' {
				buf.WriteByte('\n')
			}
			continue
		}
		buf.WriteString(beginMarker(rc.Region))
		buf.WriteByte('\n')
		if len(rc.Content) > 0 {
			buf.Write(rc.Content)
			if rc.Content[len(rc.Content)-1] != '\n' {
				buf.WriteByte('\n')
			}
		}
		buf.WriteString(endMarker(rc.Region))
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// Parse decodes a whole document: metadata plus regions. Every structural
// violation — unknown or mismatched markers, duplicate regions, a missing
// canonical region, or content outside every region — is tampering.
func Parse(doc []byte) (Document, error) {
	meta, err := ParseMetadata(doc)
	if err != nil {
		return Document{}, err
	}
	lines := splitLines(doc)[1:] // everything after the metadata line

	// Whole-document kinds: markers are tampering; content is the rest.
	if meta.Kind != KindTaskDocument {
		var content []string
		for _, ln := range lines {
			if isMarkerLine(ln) {
				return Document{}, fmt.Errorf("artifact: kind %s carries region markers: %w", meta.Kind, ErrTamperedDocument)
			}
			content = append(content, ln)
		}
		whole := trimBlankLines(content)
		var body []byte
		if len(whole) > 0 {
			body = []byte(strings.Join(whole, "\n") + "\n")
		}
		return Document{Metadata: meta, Regions: []RegionContent{{Region: RegionWholeDocument, Content: body}}}, nil
	}

	// Task documents: three explicit regions in canonical order.
	var regions []RegionContent
	i := 0
	for i < len(lines) {
		ln := lines[i]
		if strings.TrimSpace(ln) == "" {
			i++
			continue
		}
		r, ok := parseBeginMarker(ln)
		if !ok {
			return Document{}, fmt.Errorf("artifact: content outside any region %q: %w", ln, ErrTamperedDocument)
		}
		if !taskRegion(r) {
			return Document{}, fmt.Errorf("artifact: task document carries region %q: %w", r, ErrTamperedDocument)
		}
		for _, rc := range regions {
			if rc.Region == r {
				return Document{}, fmt.Errorf("artifact: region %q appears twice: %w", r, ErrTamperedDocument)
			}
		}
		var content []string
		i++
		closed := false
		for ; i < len(lines); i++ {
			if er, ok := parseEndMarker(lines[i]); ok {
				if er != r {
					return Document{}, fmt.Errorf("artifact: region %q closed by %q: %w", r, er, ErrTamperedDocument)
				}
				closed = true
				i++
				break
			}
			if _, ok := parseBeginMarker(lines[i]); ok {
				return Document{}, fmt.Errorf("artifact: region %q not closed before a new region: %w", r, ErrTamperedDocument)
			}
			content = append(content, lines[i])
		}
		if !closed {
			return Document{}, fmt.Errorf("artifact: region %q has no end marker: %w", r, ErrTamperedDocument)
		}
		var body []byte
		trimmed := trimBlankLines(content)
		if len(trimmed) > 0 {
			body = []byte(strings.Join(trimmed, "\n") + "\n")
		}
		regions = append(regions, RegionContent{Region: r, Content: body})
	}
	want := regionsOf(KindTaskDocument)
	if len(regions) != len(want) {
		return Document{}, fmt.Errorf("artifact: task document carries %d regions, want %d: %w", len(regions), len(want), ErrTamperedDocument)
	}
	for i, r := range want {
		if regions[i].Region != r {
			return Document{}, fmt.Errorf("artifact: region order %q out of canonical order: %w", regions[i].Region, ErrTamperedDocument)
		}
	}
	return Document{Metadata: meta, Regions: regions}, nil
}

// beginMarker and endMarker render a region's marker line.
func beginMarker(r Region) string { return "<!-- homonto:begin " + string(r) + " -->" }
func endMarker(r Region) string   { return "<!-- homonto:end " + string(r) + " -->" }

// parseBeginMarker parses a begin marker line.
func parseBeginMarker(ln string) (Region, bool) {
	rest, ok := strings.CutPrefix(ln, "<!-- homonto:begin ")
	if !ok {
		return "", false
	}
	rest, ok = strings.CutSuffix(rest, " -->")
	if !ok {
		return "", false
	}
	return Region(rest), true
}

// parseEndMarker parses an end marker line.
func parseEndMarker(ln string) (Region, bool) {
	rest, ok := strings.CutPrefix(ln, "<!-- homonto:end ")
	if !ok {
		return "", false
	}
	rest, ok = strings.CutSuffix(rest, " -->")
	if !ok {
		return "", false
	}
	return Region(rest), true
}

// isMarkerLine reports whether ln is any begin/end marker line.
func isMarkerLine(ln string) bool {
	_, b := parseBeginMarker(ln)
	_, e := parseEndMarker(ln)
	return b || e
}

// splitLines splits doc into lines, tolerating a missing final newline.
// Line content never contains '\n'; '\r' is kept and is content.
func splitLines(doc []byte) []string {
	s := string(doc)
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// trimBlankLines drops leading and trailing blank lines.
func trimBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// ensureJSONEOF fails when the decoder holds anything after one complete
// JSON value.
func ensureJSONEOF(dec *json.Decoder) error {
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing data after the JSON object")
	}
	return nil
}
