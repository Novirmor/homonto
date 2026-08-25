package update

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/noviopenworks/homonto/internal/update/trust"
	"time"
)

// Typed fetch errors.
var (
	// ErrInsecureURL: the URL is not https.
	ErrInsecureURL = errors.New("update: refusing a non-HTTPS url")
	// ErrRedirect: the server tried to redirect the fetch.
	ErrRedirect = errors.New("update: refusing a redirect")
	// ErrTooLarge: the response exceeds the declared or allowed size.
	ErrTooLarge = errors.New("update: response is larger than allowed")
	// ErrFetchFailed: the server did not return the resource.
	ErrFetchFailed = errors.New("update: fetch failed")
)

// MaxManifestBytes bounds a manifest download. A manifest is a small JSON
// document; anything larger is not one.
const MaxManifestBytes = 1 << 20 // 1 MiB

// MaxArtifactBytes bounds a binary download.
const MaxArtifactBytes = 512 << 20 // 512 MiB

// DefaultTimeout bounds one fetch.
const DefaultTimeout = 2 * time.Minute

// Fetcher performs the only network access Homonto ever makes.
//
// Its policy is deliberately narrow: HTTPS only, TLS 1.2 or better, no
// redirects, a hard byte cap, and a timeout. Refusing redirects is the
// unusual one and it is the point — a redirect moves the fetch to a host
// nobody vetted, and "the signature still checks out" is not an argument
// for following one, because the whole reason to pin a URL is so that the
// set of machines involved is known in advance.
type Fetcher struct {
	client *http.Client
}

// NewFetcher returns a fetcher with the release policy applied.
func NewFetcher() *Fetcher {
	return &Fetcher{client: &http.Client{
		Timeout: DefaultTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("update: %s redirected to %s: %w",
				via[len(via)-1].URL.Host, req.URL.Host, ErrRedirect)
		},
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			DisableKeepAlives:   true,
			MaxIdleConnsPerHost: 1,
		},
	}}
}

// WithClient returns a fetcher over a caller-supplied client. Tests use it
// to point at a local server; nothing else should.
func WithClient(c *http.Client) *Fetcher { return &Fetcher{client: c} }

// Get downloads a resource, bounded by limit bytes.
func (f *Fetcher) Get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("update: parse %q: %w", rawURL, err)
	}
	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("update: %q uses %q: %w", rawURL, parsed.Scheme, ErrInsecureURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("update: build request for %q: %w", rawURL, err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrRedirect) {
			return nil, err
		}
		var urlErr *url.Error
		if errors.As(err, &urlErr) && errors.Is(urlErr.Err, ErrRedirect) {
			return nil, urlErr.Err
		}
		return nil, fmt.Errorf("update: fetch %q: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: %q returned %s: %w", rawURL, resp.Status, ErrFetchFailed)
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("update: %q declares %d bytes, limit is %d: %w",
			rawURL, resp.ContentLength, limit, ErrTooLarge)
	}
	// Read one byte past the limit so a response that lies about its
	// length is caught rather than silently truncated into something that
	// might still hash.
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("update: read %q: %w", rawURL, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("update: %q exceeded the %d byte limit: %w", rawURL, limit, ErrTooLarge)
	}
	return body, nil
}

// FetchManifest downloads and verifies a release manifest.
func (f *Fetcher) FetchManifest(ctx context.Context, url string, store trust.Store, channel Channel) (VerifiedRelease, error) {
	body, err := f.Get(ctx, url, MaxManifestBytes)
	if err != nil {
		return VerifiedRelease{}, err
	}
	return VerifyManifest(store, body, channel)
}

// FetchArtifact downloads a verified release's binary and checks it
// against the manifest's checksum before returning a single byte of it to
// the caller.
func (f *Fetcher) FetchArtifact(ctx context.Context, release VerifiedRelease) ([]byte, error) {
	body, err := f.Get(ctx, release.Artifact.URL, MaxArtifactBytes)
	if err != nil {
		return nil, err
	}
	if err := VerifyChecksum(release.Artifact, body); err != nil {
		return nil, err
	}
	return body, nil
}
