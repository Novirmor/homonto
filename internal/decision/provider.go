package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Failure classes (ADR 0102). Every failed decision call carries exactly one.
const (
	FailNotConfigured = "not-configured" // no provider for the point, or no credential
	FailUnsupported   = "unsupported"    // the protocol cannot express the request
	FailUnavailable   = "unavailable"    // unreachable, unknown model, 5xx, payment required
	FailTimeout       = "timeout"        // deadline exceeded or gateway timeout
	FailRateLimited   = "rate-limited"   // 429
	FailAuth          = "auth"           // 401, 403
	FailRejected      = "rejected"       // 400, 413, 422: the provider refused the request shape
	FailInvalid       = "invalid"        // the response did not decode or did not answer the question
	FailRefused       = "refused"        // the model declined to answer
)

// Failure is a classified decision failure. Message never contains the
// provider credential.
type Failure struct {
	Class    string
	Provider string
	Status   int // HTTP status, when there was one
	Message  string
}

func (f *Failure) Error() string {
	if f.Status != 0 {
		return fmt.Sprintf("decision provider %s: %s (HTTP %d): %s", f.Provider, f.Class, f.Status, f.Message)
	}
	return fmt.Sprintf("decision provider %s: %s: %s", f.Provider, f.Class, f.Message)
}

// ClassOf returns the failure class of err, or "" when err is not a Failure.
func ClassOf(err error) string {
	var f *Failure
	if errors.As(err, &f) {
		return f.Class
	}
	return ""
}

// HTTPProvider is a Decider that speaks one Protocol to one endpoint.
type HTTPProvider struct {
	Name     string
	Protocol Protocol
	Endpoint string
	Model    string
	// APIKey is sent as a bearer token. It is never logged, recorded or
	// included in an error.
	APIKey  string
	Timeout time.Duration
	Client  *http.Client

	// lastAnswer keeps the served model and usage of the last call for the
	// record; Decide is called once per CLI invocation.
	last Answer
}

// LastAnswer returns the served model and usage of the last successful call.
func (p *HTTPProvider) LastAnswer() Answer { return p.last }

// Decide implements Decider.
func (p *HTTPProvider) Decide(ctx context.Context, req Request) (Result, error) {
	if err := req.Validate(); err != nil {
		return Result{}, err
	}
	if err := p.Protocol.Supports(req); err != nil {
		return Result{}, &Failure{Class: FailUnsupported, Provider: p.Name, Message: err.Error()}
	}
	body, err := p.Protocol.Encode(p.Model, req)
	if err != nil {
		return Result{}, &Failure{Class: FailUnsupported, Provider: p.Name, Message: err.Error()}
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, &Failure{Class: FailNotConfigured, Provider: p.Name, Message: p.redact(err.Error())}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			return Result{}, &Failure{Class: FailTimeout, Provider: p.Name, Message: fmt.Sprintf("no answer within %s", timeout)}
		}
		return Result{}, &Failure{Class: FailUnavailable, Provider: p.Name, Message: p.redact(err.Error())}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, &Failure{Class: FailTimeout, Provider: p.Name, Message: fmt.Sprintf("no answer within %s", timeout)}
		}
		return Result{}, &Failure{Class: FailUnavailable, Provider: p.Name, Message: p.redact(err.Error())}
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, &Failure{Class: classifyStatus(resp.StatusCode), Provider: p.Name, Status: resp.StatusCode, Message: p.redact(errorMessage(data))}
	}
	ans, err := p.Protocol.Decode(data, req)
	if err != nil {
		return Result{}, &Failure{Class: FailInvalid, Provider: p.Name, Message: p.redact(err.Error())}
	}
	if ans.Refused {
		return Result{}, &Failure{Class: FailRefused, Provider: p.Name, Message: "the model declined to answer"}
	}
	res := ans.Result
	res.Provider = p.Name
	if err := res.Validate(req); err != nil {
		return Result{}, &Failure{Class: FailInvalid, Provider: p.Name, Message: err.Error()}
	}
	p.last = ans
	return res, nil
}

func classifyStatus(status int) string {
	switch {
	case status == 401 || status == 403:
		return FailAuth
	case status == 429:
		return FailRateLimited
	case status == 408 || status == 504 || status == 524:
		return FailTimeout
	case status == 400 || status == 413 || status == 422:
		return FailRejected
	default:
		return FailUnavailable
	}
}

// errorMessage extracts {"error": {"message": ...}} (both protocols' error
// shape) or a truncated body.
func errorMessage(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return truncate(e.Error.Message)
	}
	return truncate(strings.TrimSpace(string(body)))
}

func truncate(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

func (p *HTTPProvider) redact(s string) string {
	if p.APIKey == "" {
		return s
	}
	return strings.ReplaceAll(s, p.APIKey, "[redacted]")
}
