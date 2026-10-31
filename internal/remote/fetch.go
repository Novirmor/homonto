package remote

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxRedirects = 5
	httpTimeout  = 60 * time.Second
	// gitFetchTimeout bounds a git init/fetch/checkout so a malicious pinned
	// repository cannot hang the process indefinitely (F27).
	gitFetchTimeout = 120 * time.Second
)

// Fetch retrieves a remote source into a validated Tree, selecting a transport
// by scheme. It never writes to the cache or any target; verification (pin match,
// revocation) is the caller's responsibility. The returned size is the fetched
// byte count (compressed download or on-disk archive size).
func Fetch(ctx context.Context, src RemoteSource, lim Limits) (Tree, int64, error) {
	switch src.Transport {
	case TransportHTTPS:
		return fetchHTTPS(ctx, src.URL, lim, nil)
	case TransportFile:
		return fetchFile(ctx, src.URL, lim)
	case TransportGit:
		return fetchGit(ctx, src.URL, lim)
	default:
		return Tree{}, 0, fmt.Errorf("remote: unsupported transport %q", src.Transport)
	}
}

// fetchHTTPS downloads an https tar.gz with a redirect cap, timeout, and a size
// ceiling, then validates it. The client is injectable for tests; a nil client
// uses a default. Only https is reachable here (the locator rejects plain http).
func fetchHTTPS(ctx context.Context, url string, lim Limits, client *http.Client) (Tree, int64, error) {
	if !strings.HasPrefix(url, "https://") {
		return Tree{}, 0, fmt.Errorf("remote: https transport requires an https:// URL, got %q", RedactLocator(url))
	}
	base := http.DefaultClient
	if client != nil {
		base = client
	}
	c := *base // shallow copy so we can set redirect/timeout without mutating the caller's client
	c.Timeout = httpTimeout
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("remote: stopped after %d redirects", maxRedirects)
		}
		// Never follow a redirect that downgrades away from https: an https source
		// must not be silently fetched over plaintext or a non-https scheme (SSRF /
		// downgrade defense).
		if req.URL.Scheme != "https" {
			return fmt.Errorf("remote: refusing redirect to non-https scheme %q", req.URL.Scheme)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Tree{}, 0, fmt.Errorf("remote: %w", redactTransportError(err))
	}
	resp, err := c.Do(req)
	if err != nil {
		return Tree{}, 0, fmt.Errorf("remote: fetch %q: %w", RedactLocator(url), redactTransportError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Tree{}, 0, fmt.Errorf("remote: fetch %q: unexpected status %s", RedactLocator(url), resp.Status)
	}
	// Bound the compressed download; the decompressed stream is bounded again by
	// ValidateTarGz. One extra byte detects an overflow.
	compressed, err := io.ReadAll(io.LimitReader(resp.Body, lim.MaxTotalBytes+1))
	if err != nil {
		return Tree{}, 0, fmt.Errorf("remote: reading body: %w", err)
	}
	if int64(len(compressed)) > lim.MaxTotalBytes {
		return Tree{}, 0, fmt.Errorf("remote: download exceeds the %d-byte cap", lim.MaxTotalBytes)
	}
	tree, err := ValidateTarGz(bytes.NewReader(compressed), lim)
	if err != nil {
		return Tree{}, 0, err
	}
	return tree, int64(len(compressed)), nil
}

// fetchFile reads a local file:// source: either a .tar.gz archive or a
// directory, both run through the same validation.
func fetchFile(_ context.Context, url string, lim Limits) (Tree, int64, error) {
	p := strings.TrimPrefix(url, "file://")
	if p == "" {
		return Tree{}, 0, fmt.Errorf("remote: file source has an empty path")
	}
	info, err := os.Stat(p)
	if err != nil {
		return Tree{}, 0, fmt.Errorf("remote: file source: %w", err)
	}
	if info.IsDir() {
		tree, size, err := treeFromDir(p, lim)
		if err != nil {
			return Tree{}, 0, err
		}
		return tree, size, nil
	}
	f, err := os.Open(p)
	if err != nil {
		return Tree{}, 0, fmt.Errorf("remote: file source: %w", err)
	}
	defer f.Close()
	tree, err := ValidateTarGz(f, lim)
	if err != nil {
		return Tree{}, 0, err
	}
	return tree, info.Size(), nil
}

// fetchGit shallow-clones a pinned ref into a temp worktree and validates its
// tree (excluding .git). Trust is governed by the content pin, so a moved tag or
// branch is caught by the digest at verify time.
func fetchGit(ctx context.Context, url string, lim Limits) (Tree, int64, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return Tree{}, 0, fmt.Errorf("remote: git transport requires git on PATH: %w", err)
	}
	cloneURL := strings.TrimPrefix(url, "git+")
	ref := ""
	if i := strings.LastIndex(cloneURL, "#"); i >= 0 {
		ref = cloneURL[i+1:]
		cloneURL = cloneURL[:i]
	}
	if ref == "" {
		return Tree{}, 0, fmt.Errorf("remote: git source %q must pin a ref with #<commit-or-tag>", RedactLocator(url))
	}
	tmp, err := os.MkdirTemp("", "homonto-git-*")
	if err != nil {
		return Tree{}, 0, fmt.Errorf("remote: %w", err)
	}
	defer os.RemoveAll(tmp)

	// Bound every git invocation under a deadline so a malicious pin cannot stall
	// the process; the caller's context still cancels earlier if it is shorter.
	ctx, cancel := context.WithTimeout(ctx, gitFetchTimeout)
	defer cancel()

	// Shallow-fetch only the pinned ref so the download is bounded to a single
	// commit's objects, not the repository's whole history (bomb/DoS defense).
	// git init + fetch --depth 1 <ref> works for a commit sha or a tag.
	gitcOut := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", tmp, "-c", "protocol.file.allow=always", "-c", "advice.detachedHead=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			// Git can repeat the clone URL in stderr, sometimes with quotes in
			// the credentials themselves. Replace that known URL intact before
			// redacting any other locators in its diagnostic.
			safe := make([]string, len(args))
			for i, a := range args {
				safe[i] = RedactLocator(a)
			}
			diagnostic := strings.ReplaceAll(string(out), cloneURL, RedactLocator(cloneURL))
			return out, fmt.Errorf("remote: git %v failed: %w: %s", safe, err, redactDiagnostic(diagnostic))
		}
		return out, nil
	}
	gitc := func(args ...string) error {
		_, err := gitcOut(args...)
		return err
	}
	if err := gitc("init", "--quiet"); err != nil {
		return Tree{}, 0, err
	}
	if err := gitc("remote", "add", "origin", cloneURL); err != nil {
		return Tree{}, 0, err
	}
	// Pass the ref after "--" is not applicable to fetch; validate it does not
	// start with a dash so it is never parsed as an option.
	if strings.HasPrefix(ref, "-") {
		return Tree{}, 0, fmt.Errorf("remote: git ref %q must not start with '-'", ref)
	}
	if err := gitc("fetch", "--quiet", "--depth", "1", "origin", ref); err != nil {
		return Tree{}, 0, err
	}
	// Enforce the size and file-count caps BEFORE checkout: ls-tree reads the
	// fetched objects without writing the working tree, so an oversized pin is
	// rejected before it can exhaust disk (F27).
	if err := guardGitTreeSize(gitcOut, lim); err != nil {
		return Tree{}, 0, err
	}
	if err := gitc("checkout", "--quiet", "--detach", "FETCH_HEAD"); err != nil {
		return Tree{}, 0, err
	}
	if err := os.RemoveAll(filepath.Join(tmp, ".git")); err != nil {
		return Tree{}, 0, fmt.Errorf("remote: %w", err)
	}
	return treeFromDir(tmp, lim)
}

// guardGitTreeSize enforces the entry-count and byte caps on a fetched but
// not-yet-checked-out git tree. It reads `git ls-tree -r --long FETCH_HEAD`,
// whose blob lines carry each file's size, so the caps are validated from the
// object store before any working-tree bytes are written (F27). Its errors are
// worded "before checkout" to distinguish them from the post-walk cap errors.
func guardGitTreeSize(gitcOut func(args ...string) ([]byte, error), lim Limits) error {
	out, err := gitcOut("ls-tree", "-r", "--long", "FETCH_HEAD")
	if err != nil {
		return err
	}
	var total int64
	count := 0
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		// Format: "<mode> <type> <sha> <size>\t<path>". Only blobs carry a numeric
		// size; trees/submodules carry "-" and are skipped.
		meta := line
		if tab := strings.IndexByte(line, '\t'); tab >= 0 {
			meta = line[:tab]
		}
		fields := strings.Fields(meta)
		if len(fields) < 4 || fields[1] != "blob" {
			continue
		}
		count++
		if count > lim.MaxEntries {
			return fmt.Errorf("remote: git source exceeds %d entries before checkout", lim.MaxEntries)
		}
		size, perr := strconv.ParseInt(fields[3], 10, 64)
		if perr != nil {
			return fmt.Errorf("remote: git ls-tree: unparseable size %q before checkout", fields[3])
		}
		if size > lim.MaxEntryBytes {
			return fmt.Errorf("remote: git source file exceeds the %d-byte per-entry cap before checkout", lim.MaxEntryBytes)
		}
		total += size
		if total > lim.MaxTotalBytes {
			return fmt.Errorf("remote: git source exceeds the %d-byte total cap before checkout", lim.MaxTotalBytes)
		}
	}
	return nil
}

// treeFromDir walks a directory into a validated Tree, rejecting symlinks and
// enforcing the same caps as archive extraction.
func treeFromDir(root string, lim Limits) (Tree, int64, error) {
	var (
		files []FileEntry
		total int64
		count int
	)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		// WalkDir uses Lstat, so a symlink shows its own type here.
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("remote: source directory contains a symlink %q", rel)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("remote: source directory contains a non-regular file %q", rel)
		}
		count++
		if count > lim.MaxEntries {
			return fmt.Errorf("remote: source exceeds %d entries", lim.MaxEntries)
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if info.Size() > lim.MaxEntryBytes {
			return fmt.Errorf("remote: source file %q exceeds the %d-byte per-entry cap", rel, lim.MaxEntryBytes)
		}
		total += info.Size()
		if total > lim.MaxTotalBytes {
			return fmt.Errorf("remote: source exceeds the %d-byte total cap", lim.MaxTotalBytes)
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		files = append(files, FileEntry{Path: rel, Mode: uint32(info.Mode()) & 0o777, Data: data})
		return nil
	})
	if err != nil {
		return Tree{}, 0, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return Tree{Files: files}, total, nil
}
