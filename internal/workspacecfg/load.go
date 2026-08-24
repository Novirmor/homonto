package workspacecfg

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/noviopenworks/homonto/internal/schema"
)

// Load reads the manifest at path, strictly decodes it, applies defaults, and
// validates it structurally. workspaceRoot-relative checks that need a root
// are the caller's business (see Validate).
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("workspacecfg: read config: %w", err)
	}
	cfg, err := Decode(bytes.NewReader(data))
	if err != nil {
		return Config{}, err
	}
	if err := Validate("", cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Decode strictly parses a manifest: unknown fields are rejected naming the
// key and its dotted path, schema_version must be present and exactly 1, and
// defaults are materialized. Decode does not run Validate; callers that want
// structural validation on top (every caller except round-trips) use Load or
// Validate directly.
func Decode(r io.Reader) (Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Config{}, fmt.Errorf("workspacecfg: read input: %w", err)
	}
	var cfg Config
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		// go-toml's strict-mode message does not name the offending key, so
		// find one ourselves and report key + dotted path.
		if key := findUnknownKey(data, reflect.TypeOf(cfg)); key != "" {
			return Config{}, fmt.Errorf("%w %q (at %s): %w", ErrUnknownField, lastKeySegment(key), key, ErrInvalidTOML)
		}
		return Config{}, fmt.Errorf("%w: %w", ErrInvalidTOML, err)
	}
	// Presence probe: an int field cannot distinguish absent from explicit 0.
	var probe struct {
		SchemaVersion *int `toml:"schema_version"`
	}
	if err := toml.Unmarshal(data, &probe); err != nil {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalidTOML, err)
	}
	if probe.SchemaVersion == nil {
		return Config{}, fmt.Errorf("%w: declare schema_version = %d", ErrMissingSchemaVersion, CurrentSchemaVersion)
	}
	if err := checkSchemaVersion(*probe.SchemaVersion); err != nil {
		return Config{}, err
	}
	return normalizedCopy(cfg), nil
}

// checkSchemaVersion enforces "present and exactly 1". Versions above the
// current one also wrap schema.ErrTooNew so callers can detect a too-new
// binary without matching message text.
func checkSchemaVersion(v int) error {
	if v == CurrentSchemaVersion {
		return nil
	}
	if v > CurrentSchemaVersion {
		return fmt.Errorf("%w: schema_version %d is newer than this binary supports (up to %d); upgrade homonto: %w",
			ErrUnsupportedSchema, v, CurrentSchemaVersion, schema.ErrTooNew)
	}
	return fmt.Errorf("%w: schema_version %d, want exactly %d", ErrUnsupportedSchema, v, CurrentSchemaVersion)
}

// findUnknownKey walks data (as a generic TOML document) against the struct
// type ty (following toml tags) and returns the dotted path of the first key
// the struct does not define, or "" if none is found or data does not parse.
// It only reports unknown keys; type mismatches are left to the decoder.
func findUnknownKey(data []byte, ty reflect.Type) string {
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return ""
	}
	return walkKeys(doc, ty, nil)
}

// walkKeys descends value (map[string]any / []any) alongside the struct
// type, accumulating the dotted path.
func walkKeys(value any, ty reflect.Type, path []string) string {
	for ty.Kind() == reflect.Pointer {
		ty = ty.Elem()
	}
	switch v := value.(type) {
	case map[string]any:
		if ty.Kind() != reflect.Struct {
			return ""
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic first-offender reporting
		for _, k := range keys {
			ft, ok := structFieldByTOMLName(ty, k)
			if !ok {
				return strings.Join(append(path, k), ".")
			}
			if sub := walkKeys(v[k], ft.Type, append(path, k)); sub != "" {
				return sub
			}
		}
	case []any:
		if ty.Kind() != reflect.Slice {
			return ""
		}
		elem := ty.Elem()
		for elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		if elem.Kind() != reflect.Struct {
			return ""
		}
		for i, item := range v {
			if sub := walkKeys(item, ty.Elem(), append(path, fmt.Sprintf("%s[%d]", path[len(path)-1], i))); sub != "" {
				return sub
			}
		}
	}
	return ""
}

// structFieldByTOMLName resolves a TOML key to the struct field that declares
// it (by toml tag, falling back to the case-insensitive field name, which is
// go-toml's behavior).
func structFieldByTOMLName(ty reflect.Type, key string) (reflect.StructField, bool) {
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		name := f.Name
		if tag := f.Tag.Get("toml"); tag != "" {
			parts := strings.Split(tag, ",")
			if parts[0] == "-" {
				continue
			}
			if parts[0] != "" {
				name = parts[0]
			}
		}
		if strings.EqualFold(name, key) {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// lastKeySegment returns the final dotted component (or the whole string when
// there are no dots), for the "unknown field %q" part of the message.
func lastKeySegment(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// normalizedCopy returns cfg with defaults materialized and canonical order
// applied (members by id, checks by name) without mutating the receiver's
// backing arrays.
func normalizedCopy(cfg Config) Config {
	out := cfg
	out.Members = append([]Member(nil), cfg.Members...)
	for i := range out.Members {
		m := &out.Members[i]
		m.Verification = append([]Check(nil), m.Verification...)
		for j := range m.Verification {
			ck := &m.Verification[j]
			if ck.Timeout == "" {
				ck.Timeout = DefaultCheckTimeout
			}
			if ck.WorkingDir == "" {
				ck.WorkingDir = "."
			}
		}
		sort.SliceStable(m.Verification, func(a, b int) bool {
			return m.Verification[a].Name < m.Verification[b].Name
		})
	}
	sort.SliceStable(out.Members, func(a, b int) bool {
		return out.Members[a].ID < out.Members[b].ID
	})
	return out
}

// Marshal writes the canonical form of cfg: defaults materialized, members
// sorted by id, checks sorted by name. Marshal(Decode(Marshal(cfg))) is
// byte-identical to Marshal(cfg). Marshal does not validate; serializing an
// invalid config is the caller's mistake, not a detectable one here.
func Marshal(cfg Config) ([]byte, error) {
	b, err := toml.Marshal(normalizedCopy(cfg))
	if err != nil {
		return nil, fmt.Errorf("workspacecfg: marshal: %w", err)
	}
	return b, nil
}
