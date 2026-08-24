package snapshot

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/fingerprint"
)

// manifestSeed builds a valid manifest document for the seed corpus.
func manifestSeed() string {
	m := Manifest{SchemaVersion: 1, Entries: []Entry{
		{Path: "d", Kind: KindDir, Mode: 0o755},
		{Path: "d/f.bin", Kind: KindFile, Mode: 0o600, Size: 4, Digest: strings.Repeat("a", 64)},
		{Path: "l", Kind: KindSymlink, Mode: 0o777, Size: 1, Digest: strings.Repeat("b", 64), LinkTarget: "d"},
	}}
	m.RootDigest = DigestManifest(m)
	b, err := EncodeManifest(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func patchSeed() string {
	p := PatchManifest{SchemaVersion: 1, BaseDigest: digestOfLength(1), ResultDigest: digestOfLength(2), Operations: []PatchOp{
		{Op: OpAdd, Path: "n", Kind: KindFile, Mode: 0o644, Size: 1, Digest: string(digestOfLength(3))},
		{Op: OpModify, Path: "o", Kind: KindFile, Mode: 0o644, Size: 2, Digest: string(digestOfLength(4)),
			BeforeKind: KindFile, BeforeMode: 0o644, BeforeDigest: string(digestOfLength(5))},
	}}
	b, err := EncodePatch(p)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func digestOfLength(n int) fingerprint.Digest {
	return fingerprint.Digest(strings.Repeat(string(rune('a'+n-1)), 64))
}

// FuzzDecodeManifest feeds arbitrary bytes to the strict manifest
// decoder. The contract: never panic, and every rejection is one of the
// package's typed errors — manifests are read back from disk during
// journal recovery, so a hostile or corrupt file must fail closed and
// legibly, never crash.
func FuzzDecodeManifest(f *testing.F) {
	f.Add(manifestSeed())
	f.Add(`{"schema_version":2}`)
	f.Add(`{"schema_version":1,"entries":[{"path":"../x","kind":"file","mode":420,"size":1,"digest":"` + strings.Repeat("0", 64) + `"}]}`)
	f.Add("not json \x00\xff")
	f.Add("")
	f.Add(`{"schema_version":1,"entries":[]}`)
	f.Fuzz(func(t *testing.T, doc string) {
		m, err := DecodeManifest([]byte(doc))
		if err != nil {
			typed := errors.Is(err, ErrInvalidManifest) ||
				errors.Is(err, ErrUnsupportedSchema) ||
				errors.Is(err, ErrInvalidPath) ||
				errors.Is(err, ErrDuplicatePath) ||
				errors.Is(err, ErrDigestMismatch)
			if !typed {
				t.Fatalf("DecodeManifest returned untyped error: %v", err)
			}
			return
		}
		// An accepted manifest must round-trip through its own encoding
		// and re-decode identically.
		b, err := EncodeManifest(m)
		if err != nil {
			t.Fatalf("EncodeManifest(decode(...)) failed on accepted input: %v", err)
		}
		again, err := DecodeManifest(b)
		if err != nil {
			t.Fatalf("re-decode of canonical encoding failed: %v", err)
		}
		if !reflect.DeepEqual(m, again) {
			t.Fatalf("round trip changed the manifest: %+v vs %+v", m, again)
		}
		if DigestManifest(m) != m.RootDigest {
			t.Fatalf("accepted manifest digest inconsistent")
		}
	})
}

// FuzzDecodePatch feeds arbitrary bytes to the strict patch decoder.
// Same contract: never panic, typed rejections only. Patches cross
// process boundaries (they are the durable handoff of a non-Git result),
// so a corrupt patch must be a legible error.
func FuzzDecodePatch(f *testing.F) {
	f.Add(patchSeed())
	f.Add(`{"schema_version":1,"operations":[{"op":"rename","path":"b","old_path":"a","kind":"file","mode":420,"size":1,"digest":"` + strings.Repeat("c", 64) + `","before_digest":"` + strings.Repeat("c", 64) + `"}]}`)
	f.Add(`{"schema_version":1,"operations":[{"op":"add","path":"A"},{"op":"delete","path":"a","kind":"file","mode":420,"before_kind":"file","before_mode":420,"before_digest":"` + strings.Repeat("d", 64) + `"}]}`)
	f.Add("garbage \x00\x01")
	f.Add("")
	f.Fuzz(func(t *testing.T, doc string) {
		p, err := DecodePatch([]byte(doc))
		if err != nil {
			typed := errors.Is(err, ErrInvalidPatch) ||
				errors.Is(err, ErrUnsupportedSchema) ||
				errors.Is(err, ErrInvalidPath) ||
				errors.Is(err, ErrCaseCollision)
			if !typed {
				t.Fatalf("DecodePatch returned untyped error: %v", err)
			}
			return
		}
		b, err := EncodePatch(p)
		if err != nil {
			t.Fatalf("EncodePatch(decode(...)) failed on accepted input: %v", err)
		}
		again, err := DecodePatch(b)
		if err != nil {
			t.Fatalf("re-decode of canonical encoding failed: %v", err)
		}
		if !reflect.DeepEqual(p, again) {
			t.Fatalf("round trip changed the patch: %+v vs %+v", p, again)
		}
		if _, err := InvertPatch(p); err != nil && !errors.Is(err, ErrInvalidPatch) && !errors.Is(err, ErrCaseCollision) && !errors.Is(err, ErrInvalidPath) && !errors.Is(err, ErrUnsupportedSchema) {
			t.Fatalf("InvertPatch returned untyped error: %v", err)
		}
	})
}
