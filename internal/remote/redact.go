package remote

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// secretQueryKeys are query parameter names commonly used to carry credentials.
// Their values are redacted before a locator is persisted or reported.
var secretQueryKeys = map[string]bool{
	"token":         true,
	"access_token":  true,
	"api_key":       true,
	"apikey":        true,
	"key":           true,
	"secret":        true,
	"password":      true,
	"passwd":        true,
	"pat":           true,
	"private_token": true,
	"auth":          true,
}

// redactedMarker replaces a redacted secret query value.
const redactedMarker = "REDACTED"

// RedactLocator returns a canonical form of a remote locator with any embedded
// credentials removed, so it is safe to persist in remote.lock.json or surface
// in an error or log line. URL userinfo (user:pass@, including a bare token as
// the username) is dropped entirely, and the values of known secret query
// parameters are replaced with a marker. Malformed queries are redacted in full
// rather than trusting a partial parse that may silently drop secret parameters.
func RedactLocator(raw string) string {
	prefix := ""
	if rest, ok := strings.CutPrefix(raw, RemoteSourcePrefix); ok {
		prefix, raw = RemoteSourcePrefix, rest
	}
	u, err := url.Parse(raw)
	if err != nil {
		safe := stripUserinfoFallback(raw)
		// A malformed path/authority can prevent URL parsing entirely. Strip
		// its query without parsing; it may still contain valid credentials.
		head, fragment, hasFragment := strings.Cut(safe, "#")
		if before, _, hasQuery := strings.Cut(head, "?"); hasQuery {
			head = before + "?" + redactedMarker
		}
		if hasFragment {
			head += "#" + fragment
		}
		return prefix + head
	}
	changed := false
	if u.User != nil {
		// Drop userinfo wholesale: a bare username can itself be a token
		// (e.g. a PAT used as https://<token>@host/...), so keeping the
		// username is not safe.
		u.User = nil
		changed = true
	}
	if u.RawQuery != "" {
		vals, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			u.RawQuery = redactedMarker
			changed = true
		} else {
			for k := range vals {
				if secretQueryKeys[strings.ToLower(k)] {
					vals.Set(k, redactedMarker)
					changed = true
				}
			}
			if changed {
				u.RawQuery = vals.Encode()
			}
		}
	}
	if !changed {
		return prefix + raw
	}
	return prefix + u.String()
}

// Diagnostics can repeat URLs outside structured url.Error fields: net/http
// embeds malformed redirect locations in plain text, and Git quotes its URLs
// in stderr. Keep quoted strings whole so even invalid URLs containing spaces
// are redacted. Unquoted tokens cover diagnostics that print a URL directly.
var diagnosticToken = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'[^']*'|[^\s"'<>]+`)

func redactDiagnostic(text string) string {
	return diagnosticToken.ReplaceAllStringFunc(text, func(token string) string {
		raw := token
		quote := byte(0)
		if token[0] == '"' {
			if decoded, err := strconv.Unquote(token); err == nil {
				raw, quote = decoded, '"'
			}
		} else if token[0] == '\'' {
			raw, quote = token[1:len(token)-1], '\''
		}
		if !strings.Contains(raw, "://") && !strings.Contains(raw, "?") {
			return token
		}
		safe := RedactLocator(raw)
		if safe == raw {
			return token
		}
		switch quote {
		case '"':
			return strconv.Quote(safe)
		case '\'':
			return "'" + safe + "'"
		default:
			return safe
		}
	})
}

// Keep the original cause available to errors.Is/As without rendering its
// credential-bearing diagnostic in the user-facing error message.
type redactedTransportError struct {
	cause   error
	message string
}

func (e *redactedTransportError) Error() string { return e.message }
func (e *redactedTransportError) Unwrap() error { return e.cause }

func redactTransportError(err error) error {
	message := err.Error()
	safe := redactDiagnostic(message)
	if safe == message {
		return err
	}
	return &redactedTransportError{cause: err, message: safe}
}

// stripUserinfoFallback removes a "user:pass@" (or "token@") userinfo segment
// from the authority of a "scheme://…" string without a full URL parse. It only
// touches the segment before the first '/', '?' or '#' after the authority so it
// cannot corrupt a path or query that happens to contain '@'.
func stripUserinfoFallback(raw string) string {
	i := strings.Index(raw, "://")
	if i < 0 {
		return raw
	}
	rest := raw[i+3:]
	// Authority ends at the first '/', '?' or '#'.
	end := len(rest)
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		end = j
	}
	authority := rest[:end]
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return raw
	}
	return raw[:i+3] + authority[at+1:] + rest[end:]
}
