package secret

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveJSONHandlesSpecialCharsSafely(t *testing.T) {
	r := &Resolver{
		Getenv: os_Getenv_none,
		Pass: func(p string) (string, error) {
			// a secret containing a quote, backslash, newline, and a JSON-ish payload
			return "a\"b\\c\nd\",\"injected\":\"x", nil
		},
	}
	val, err := r.ResolveJSON(`{"command":["npx"],"env":{"K":"${pass:ai/brave}"}}`)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	m, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T", val)
	}
	env := m["env"].(map[string]any)
	if env["K"] != "a\"b\\c\nd\",\"injected\":\"x" {
		t.Fatalf("secret not placed verbatim: %q", env["K"])
	}
	// no key injection: env has exactly one key, top-level has exactly command+env
	if len(env) != 1 {
		t.Fatalf("key injection into env: %v", env)
	}
	if _, bad := m["injected"]; bad {
		t.Fatal("key injection into top-level object")
	}
	// re-marshals to valid JSON
	if _, err := json.Marshal(val); err != nil {
		t.Fatalf("resolved value not marshalable: %v", err)
	}
}

func TestResolveJSONMissingRefErrors(t *testing.T) {
	r := &Resolver{Getenv: os_Getenv_none, Pass: func(string) (string, error) { return "", nil }}
	if _, err := r.ResolveJSON(`{"env":{"K":"${MISSING}"}}`); err == nil {
		t.Fatal("expected error for missing ref")
	}
}

// Several missing refs always report the one under the lowest key.
func TestResolveJSONMissingRefIsDeterministic(t *testing.T) {
	r := &Resolver{Getenv: os_Getenv_none, Pass: func(string) (string, error) { return "", nil }}
	for range 40 {
		_, err := r.ResolveJSON(`{"zz":"${ZZ}","aa":"${AA}","mm":"${MM}"}`)
		if err == nil || !strings.Contains(err.Error(), "AA") {
			t.Fatalf("want error naming AA, got: %v", err)
		}
	}
}

func TestResolveJSONPlainValue(t *testing.T) {
	r := &Resolver{Getenv: os_Getenv_none, Pass: func(string) (string, error) { return "", nil }}
	v, err := r.ResolveJSON(`"opus"`)
	if err != nil {
		t.Fatal(err)
	}
	if v != "opus" {
		t.Fatalf("plain value = %v", v)
	}
}

func os_Getenv_none(string) string { return "" }
