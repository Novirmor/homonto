package remote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchHTTPSCanceledErrorRedactsCredentials(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := fetchHTTPS(ctx, "https://user:s3cret@host/archive?token=s3cret", DefaultLimits, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancellation cause preserved, got %v", err)
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("fetch error leaked source credentials: %v", err)
	}
}

func TestFetchHTTPSRedirectErrorRedactsCredentials(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/again?token=s3cret", http.StatusFound)
	}))
	defer srv.Close()
	_, _, err := fetchHTTPS(context.Background(), srv.URL+"/archive", DefaultLimits, srv.Client())
	if err == nil {
		t.Fatal("want redirect limit error")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("fetch error leaked redirected credentials: %v", err)
	}
}

func TestFetchHTTPSMalformedURLErrorRedactsCredentials(t *testing.T) {
	_, _, err := fetchHTTPS(context.Background(), "https://user:s3cret@host/%zz?token=s3cret", DefaultLimits, nil)
	if err == nil {
		t.Fatal("want URL parse error")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("parse error leaked source credentials: %v", err)
	}
}

func TestFetchHTTPSMalformedRedirectRedactsCredentials(t *testing.T) {
	for _, location := range []string{
		"/%zz?token=s3cret",
		"https://user:s3cret@host/%zz?token=s3cret",
		"https://user:s3cret with space@host/%zz?token=s3cret",
	} {
		t.Run(location, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", location)
				w.WriteHeader(http.StatusFound)
			}))
			defer srv.Close()
			_, _, err := fetchHTTPS(context.Background(), srv.URL+"/archive", DefaultLimits, srv.Client())
			if err == nil {
				t.Fatal("want malformed redirect error")
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Fatalf("malformed redirect error leaked credentials: %v", err)
			}
		})
	}
}

func writeFixtureTarGz(t *testing.T, dir, name string) string {
	t.Helper()
	raw := gz(t, buildTar(t, []tarEntry{
		{name: "agent.md", data: "# agent"},
		{name: "ref/notes.md", data: "notes"},
	}))
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFetchFileTarGz(t *testing.T) {
	dir := t.TempDir()
	p := writeFixtureTarGz(t, dir, "pkg.tar.gz")
	src, err := ParseRemoteSource("remote:file://" + p)
	if err != nil {
		t.Fatal(err)
	}
	tree, size, err := Fetch(context.Background(), src, DefaultLimits)
	if err != nil {
		t.Fatalf("fetch file tar.gz: %v", err)
	}
	if len(tree.Files) != 2 {
		t.Fatalf("want 2 files, got %d", len(tree.Files))
	}
	if size <= 0 {
		t.Errorf("size should be positive, got %d", size)
	}
}

func TestFetchFileDirectory(t *testing.T) {
	dir := t.TempDir()
	content := filepath.Join(dir, "content")
	if err := os.MkdirAll(filepath.Join(content, "ref"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "agent.md"), []byte("# agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "ref", "n.md"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := ParseRemoteSource("remote:file://" + content)
	if err != nil {
		t.Fatal(err)
	}
	tree, _, err := Fetch(context.Background(), src, DefaultLimits)
	if err != nil {
		t.Fatalf("fetch directory: %v", err)
	}
	if len(tree.Files) != 2 {
		t.Fatalf("want 2 files from dir, got %d", len(tree.Files))
	}
}

func TestFetchFileRejectsSymlinkInDir(t *testing.T) {
	dir := t.TempDir()
	content := filepath.Join(dir, "content")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "ok.md"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(content, "evil")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	src, _ := ParseRemoteSource("remote:file://" + content)
	if _, _, err := Fetch(context.Background(), src, DefaultLimits); err == nil {
		t.Fatal("a symlink in a source directory must fail closed")
	}
}

func TestFetchHTTPS(t *testing.T) {
	raw := gz(t, buildTar(t, []tarEntry{{name: "a.md", data: "hi"}}))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(raw)
	}))
	defer srv.Close()

	src := RemoteSource{URL: srv.URL + "/pkg.tar.gz", Transport: TransportHTTPS}
	tree, _, err := fetchHTTPS(context.Background(), src.URL, DefaultLimits, srv.Client())
	if err != nil {
		t.Fatalf("https fetch: %v", err)
	}
	if len(tree.Files) != 1 {
		t.Fatalf("want 1 file, got %d", len(tree.Files))
	}
}

func TestFetchHTTPSRedirectCapped(t *testing.T) {
	// A server that always redirects must trip the redirect cap, not loop.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	defer srv.Close()
	if _, _, err := fetchHTTPS(context.Background(), srv.URL+"/x.tar.gz", DefaultLimits, srv.Client()); err == nil {
		t.Fatal("excessive redirects must fail")
	}
}

func TestFetchHTTPSSizeCapped(t *testing.T) {
	big := make([]byte, 4096)
	raw := gz(t, buildTar(t, []tarEntry{{name: "big", data: string(big)}}))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(raw)
	}))
	defer srv.Close()
	small := Limits{MaxEntries: 5, MaxEntryBytes: 1024, MaxTotalBytes: 64}
	if _, _, err := fetchHTTPS(context.Background(), srv.URL+"/x.tar.gz", small, srv.Client()); err == nil {
		t.Fatal("oversized content must be rejected")
	}
}

func TestFetchUnknownSchemeErrors(t *testing.T) {
	if _, _, err := Fetch(context.Background(), RemoteSource{URL: "ftp://h/x", Transport: "ftp"}, DefaultLimits); err == nil {
		t.Fatal("unknown transport must error")
	}
}

func TestFetchHTTPSRejectsHTTPRedirect(t *testing.T) {
	// A server that redirects https → http must be rejected (no downgrade/SSRF),
	// honoring the https-only guarantee even across redirects.
	// Serve VALID content over plaintext, so the only thing that can reject the
	// fetch is the scheme-downgrade check (not a later gzip/validation error).
	valid := gz(t, buildTar(t, []tarEntry{{name: "a.md", data: "x"}}))
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(valid)
	}))
	defer httpSrv.Close()
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, httpSrv.URL+"/x.tar.gz", http.StatusFound)
	}))
	defer tlsSrv.Close()
	// Use the TLS client but it will follow to the plain-http server; our
	// CheckRedirect must refuse the scheme downgrade.
	if _, _, err := fetchHTTPS(context.Background(), tlsSrv.URL+"/x.tar.gz", DefaultLimits, tlsSrv.Client()); err == nil {
		t.Fatal("an https→http redirect must be rejected")
	}
}
