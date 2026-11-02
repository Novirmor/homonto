package bitbucket

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const token = "ATATT3xFfGF0-secret-token-value"

func testClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{Base: srv.URL + "/2.0/", Token: token, HTTP: &http.Client{Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, srv
}

func noSecret(t *testing.T, err error, c *Client) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, s := range []string{token, base64.StdEncoding.EncodeToString([]byte(c.Email + ":" + token))} {
		if strings.Contains(err.Error(), s) {
			t.Fatalf("error leaks the credential: %v", err)
		}
	}
}

func TestBearerAuthAndBody(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token || r.URL.Path != "/2.0/repositories/ws/app/pullrequests/1/comments" || r.Method != "POST" {
			t.Errorf("request %s %s auth %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		fmt.Fprint(w, `{"id":5}`)
	})
	out, err := c.Call("POST", "repositories/ws/app/pullrequests/1/comments", []byte(`{"content":{"raw":"x"}}`), false)
	if err != nil || string(out) != `{"id":5}` {
		t.Fatalf("%s %v", out, err)
	}
}

func TestBasicAuthWhenEmailIsSet(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "me@example.com" || p != token {
			t.Error("missing Basic credentials")
		}
		fmt.Fprint(w, `{}`)
	})
	c.Email = "me@example.com"
	if _, err := c.Call("GET", "user", nil, false); err != nil {
		t.Fatal(err)
	}
}

func TestFailuresNeverLeakTheToken(t *testing.T) {
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/2.0/echo":
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"error":{"message":"bad token %s / %s"}}`, token, r.Header.Get("Authorization"))
		case "/2.0/redirect":
			http.Redirect(w, r, "https://evil.example/steal", http.StatusFound)
		case "/2.0/page":
			fmt.Fprint(w, `{"values":[1],"next":"https://evil.example/2.0/page?page=2"}`)
		}
	})
	_, err := c.Call("GET", "echo", nil, false)
	noSecret(t, err, c)
	if !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), redacted) {
		t.Fatalf("error = %v", err)
	}
	c.Email = "me@example.com"
	_, err = c.Call("GET", "echo", nil, false)
	noSecret(t, err, c)

	if _, err := c.Call("GET", "redirect", nil, false); err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("redirect must not be followed: %v", err)
	}
	if _, err := c.Call("GET", "page", nil, true); err == nil || !strings.Contains(err.Error(), "outside the API root") {
		t.Fatalf("foreign next link followed: %v", err)
	}
	srv.Close()
	_, err = c.Call("GET", "user", nil, false)
	noSecret(t, err, c)
}

func TestEndpointMustStayUnderTheAPIRoot(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Errorf("request sent: %s", r.URL) })
	for _, bad := range []string{"https://evil.example/x", "//evil.example/x", "/2.0/user", "../user", "repositories/../../x", "user#x", "a b", ""} {
		if _, err := c.Call("GET", bad, nil, false); err == nil {
			t.Errorf("accepted endpoint %q", bad)
		}
	}
	if _, err := c.Call("PATCH", "user", nil, false); err == nil {
		t.Error("accepted an unsupported method")
	}
}

func TestPaginateMergesValues(t *testing.T) {
	var base string
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"values":[{"id":3}]}`)
			return
		}
		fmt.Fprintf(w, `{"values":[{"id":1},{"id":2}],"next":"%s/2.0/list?page=2"}`, base)
	})
	base = srv.URL
	out, err := c.Call("GET", "list", nil, true)
	if err != nil || string(out) != `[{"id":1},{"id":2},{"id":3}]` {
		t.Fatalf("%s %v", out, err)
	}
}

func TestFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if _, err := FromEnv(env(nil)); err == nil || !strings.Contains(err.Error(), TokenEnv) {
		t.Fatalf("missing token = %v", err)
	}
	c, err := FromEnv(env(map[string]string{TokenEnv: "${BB_SECRET}", "BB_SECRET": token}))
	if err != nil || c.Token != token || c.Base != API {
		t.Fatalf("reference resolution = %+v, %v", c, err)
	}
	_, err = FromEnv(env(map[string]string{TokenEnv: "${MISSING_VAR}"}))
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("unresolved reference error = %v", err)
	}
}
