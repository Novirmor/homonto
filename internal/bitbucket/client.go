// Package bitbucket is homonto's Bitbucket Cloud REST transport, the
// counterpart of `gh api` and `glab api` for a host with no first-party CLI
// that covers the h processes (ADR 0091). `homonto host bitbucket api`
// exposes it; the hosting runtime and the coordinator call that command, so
// host access still goes through one host CLI (ADR 0047).
//
// The API token is read from the environment (optionally through a
// ${pass:...} or ${NAME} reference) and is never written to stdout,
// stderr, an error, or a log.
package bitbucket

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Novirmor/homonto/internal/secret"
)

// API is Bitbucket Cloud's REST 2.0 root. Only this origin ever receives the token.
const API = "https://api.bitbucket.org/2.0/"

const (
	// TokenEnv holds a scoped Bitbucket API token, or a ${pass:...} reference to one.
	TokenEnv = "HOMONTO_BITBUCKET_TOKEN"
	// EmailEnv, when set, switches to Basic auth (Atlassian email + token).
	EmailEnv = "HOMONTO_BITBUCKET_EMAIL"

	maxBody  = 8 * 1024 * 1024
	maxPages = 1000
	redacted = "REDACTED"
)

// Client calls one Bitbucket Cloud API root.
type Client struct {
	Base  string // API root, ending in "/"
	Token string
	Email string
	HTTP  *http.Client
}

// FromEnv builds a client for the production API from the environment.
func FromEnv(getenv func(string) string) (*Client, error) {
	raw := strings.TrimSpace(getenv(TokenEnv))
	if raw == "" {
		return nil, fmt.Errorf("bitbucket: %s is not set; create a scoped Bitbucket API token and export it (or a ${pass:...} reference to it)", TokenEnv)
	}
	token := raw
	if secret.ContainsRef(raw) {
		r := secret.NewResolver()
		r.Getenv = getenv
		resolved, err := r.Resolve(raw)
		if err != nil {
			return nil, fmt.Errorf("bitbucket: resolving %s failed", TokenEnv)
		}
		token = strings.TrimSpace(resolved)
	}
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, fmt.Errorf("bitbucket: %s resolved to an unusable token", TokenEnv)
	}
	return &Client{Base: API, Token: token, Email: strings.TrimSpace(getenv(EmailEnv)), HTTP: &http.Client{
		Timeout: 30 * time.Second,
		// Never follow a redirect: it could carry the Authorization header elsewhere.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// redact removes the token (and its Basic encoding) from any text.
func (c *Client) redact(s string) string {
	if c.Token != "" {
		s = strings.ReplaceAll(s, c.Token, redacted)
		if c.Email != "" {
			s = strings.ReplaceAll(s, base64.StdEncoding.EncodeToString([]byte(c.Email+":"+c.Token)), redacted)
		}
	}
	return s
}

// resolve joins a relative endpoint to the API root and refuses anything that
// would leave it.
func (c *Client) resolve(endpoint string) (*url.URL, error) {
	if endpoint == "" || strings.Contains(endpoint, "://") || strings.HasPrefix(endpoint, "/") || strings.HasPrefix(endpoint, "\\") ||
		strings.ContainsAny(endpoint, " \t\r\n#") || strings.Contains(endpoint, "..") {
		return nil, errors.New("bitbucket: endpoint must be a relative API path such as repositories/WORKSPACE/REPO")
	}
	base, err := url.Parse(c.Base)
	if err != nil {
		return nil, err
	}
	u, err := base.Parse(endpoint)
	if err != nil || !c.sameOrigin(u) {
		return nil, errors.New("bitbucket: endpoint must stay under the API root")
	}
	return u, nil
}

func (c *Client) sameOrigin(u *url.URL) bool {
	base, err := url.Parse(c.Base)
	return err == nil && u.Scheme == base.Scheme && u.Host == base.Host && u.User == nil && strings.HasPrefix(u.Path, base.Path)
}

func (c *Client) do(method string, u *url.URL, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, u.String(), reader)
	if err != nil {
		return nil, errors.New("bitbucket: invalid request")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Email != "" {
		req.SetBasicAuth(c.Email, c.Token)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bitbucket: %s %s: %s", method, u.Path, c.redact(err.Error()))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("bitbucket: %s %s: reading response failed", method, u.Path)
	}
	if len(data) > maxBody {
		return nil, fmt.Errorf("bitbucket: %s %s: response exceeds %d bytes", method, u.Path, maxBody)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("bitbucket: %s %s: HTTP %d%s", method, u.Path, resp.StatusCode, c.redact(apiMessage(data)))
	}
	return data, nil
}

// apiMessage extracts Bitbucket's error message, bounded.
func apiMessage(data []byte) string {
	var v struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &v) != nil || v.Error.Message == "" {
		return ""
	}
	msg := v.Error.Message
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return ": " + msg
}

// Call performs one request. With paginate, it follows same-origin `next`
// links and returns every page's `values` as one JSON array.
func (c *Client) Call(method, endpoint string, body []byte, paginate bool) ([]byte, error) {
	switch method {
	case "GET", "POST", "PUT", "DELETE":
	default:
		return nil, fmt.Errorf("bitbucket: unsupported method %q", method)
	}
	u, err := c.resolve(endpoint)
	if err != nil {
		return nil, err
	}
	if !paginate {
		return c.do(method, u, body)
	}
	if method != "GET" {
		return nil, errors.New("bitbucket: --paginate requires GET")
	}
	values := []json.RawMessage{}
	for page := 0; ; page++ {
		if page >= maxPages {
			return nil, errors.New("bitbucket: too many pages")
		}
		data, err := c.do("GET", u, nil)
		if err != nil {
			return nil, err
		}
		var p struct {
			Values []json.RawMessage `json:"values"`
			Next   string            `json:"next"`
		}
		if err := json.Unmarshal(data, &p); err != nil || p.Values == nil {
			return nil, errors.New("bitbucket: --paginate needs a paginated list response")
		}
		values = append(values, p.Values...)
		if p.Next == "" {
			break
		}
		next, err := url.Parse(p.Next)
		if err != nil || !c.sameOrigin(next) {
			return nil, errors.New("bitbucket: refusing a pagination link outside the API root")
		}
		u = next
	}
	return json.Marshal(values)
}
