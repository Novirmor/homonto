package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/remote"
)

// HasRemoteResources reports whether any declared subagent uses a remote:
// source. The apply CLI uses this to force a remote re-resolution even when the
// symlink projection is unchanged (a digest-only repin does not alter the
// name-based symlink plan but must still re-fetch, verify, and re-materialize).
func (e *Engine) HasRemoteResources() bool {
	declared, err := e.declaredRemoteSubagents()
	return err == nil && len(declared) > 0
}

// RemoteRepin describes a declared remote subagent whose pinned digest differs
// from the digest recorded in the remote lockfile. A digest-only repin does not
// alter the name-based symlink plan, so apply must surface these separately and
// require confirmation before mutating remote content (F6).
type RemoteRepin struct {
	Name string
	Old  string // digest recorded in the lock
	New  string // digest declared in config
}

// PendingRemoteRepins returns declared remote subagents whose pinned digest
// differs from the lockfile record, in stable name order. A remote that is
// declared but not yet locked is NOT reported here: it already surfaces as a new
// symlink in the projection plan. Only an invisible digest-only change (same
// name, same link target, different pinned content) is returned.
func (e *Engine) PendingRemoteRepins() ([]RemoteRepin, error) {
	declared, err := e.declaredRemoteSubagents()
	if err != nil {
		return nil, err
	}
	lock, err := remote.LoadLock(e.remoteLockPath())
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []RemoteRepin
	for _, name := range names {
		entry, ok := lock.Get("subagent", name)
		if !ok {
			continue
		}
		if entry.Digest != declared[name].Digest {
			out = append(out, RemoteRepin{Name: name, Old: entry.Digest, New: declared[name].Digest})
		}
	}
	return out, nil
}

// remoteSubagentDir is the single source of truth for where verified remote
// subagent content is materialized. materializeRemotes writes it, the adapters
// link from it, and doctor reads it — they must stay in lockstep.
func (e *Engine) remoteSubagentDir() string { return filepath.Join(e.RemoteRoot, "subagents") }

// remotePaths returns the lockfile and revocation-list paths under the state dir.
func (e *Engine) remoteLockPath() string { return filepath.Join(e.StateDir, "remote.lock.json") }
func (e *Engine) remoteRevokedPath() string {
	return filepath.Join(e.StateDir, "revoked.json")
}

// materializeRemotes resolves every declared remote subagent through the trust
// pipeline (fetch → validate → pin-match → revocation → cache), materializes the
// verified content into the deterministic remote root, records provenance in the
// remote lockfile, prunes de-declared installs, and GCs unreferenced cache
// entries. It aborts before any adapter write if a remote resource fails closed.
// The context bounds every network fetch so the caller can interrupt a hung or
// slow source.
func (e *Engine) materializeRemotes(ctx context.Context) error {
	declared, err := e.declaredRemoteSubagents()
	if err != nil {
		return err
	}

	rev, err := remote.LoadRevocations(e.remoteRevokedPath())
	if err != nil {
		return err
	}
	resolver := &remote.Resolver{
		Cache:       &remote.Cache{Root: e.RemoteCacheRoot},
		Revocations: rev,
		Limits:      remote.DefaultLimits,
	}
	lock, err := remote.LoadLock(e.remoteLockPath())
	if err != nil {
		return err
	}

	subagentRoot := e.remoteSubagentDir()

	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)

	// Revoked-but-still-declared content must never remain active. Quarantine it
	// (remove its materialized file and drop its lock entry) and fail closed
	// BEFORE any fetch, prune, or activation, so a banned pin's bytes are never
	// served after a revocation (F30).
	var revokedNames []string
	for _, name := range names {
		pin, err := remote.ParseDigest(declared[name].Digest)
		if err != nil {
			return fmt.Errorf("remote subagent %q: %w", name, err)
		}
		if rev.Contains(pin) {
			revokedNames = append(revokedNames, name)
		}
	}
	if len(revokedNames) > 0 {
		for _, name := range revokedNames {
			if err := os.Remove(filepath.Join(subagentRoot, name+".md")); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remote: deactivating revoked %q: %w", name, err)
			}
			lock.Remove("subagent", name)
		}
		if err := lock.Save(e.remoteLockPath()); err != nil {
			return err
		}
		return fmt.Errorf("remote subagent %q: content is revoked", revokedNames[0])
	}

	// Stage: fetch AND verify every declared remote into the content-addressed
	// cache before touching any active content or the lock. Cache writes are
	// content-keyed staging — they never mutate the active remote root or the
	// lockfile — so if any remote fails here the whole apply aborts with the
	// active content and lock untouched (F8: all-or-nothing across remotes).
	type staged struct {
		src      remote.RemoteSource
		pin      remote.Digest
		cacheDir string
	}
	stagedByName := make(map[string]staged, len(names))
	for _, name := range names {
		res := declared[name]
		src, err := remote.ParseRemoteSource(res.Source)
		if err != nil {
			return fmt.Errorf("remote subagent %q: %w", name, err)
		}
		pin, err := remote.ParseDigest(res.Digest)
		if err != nil {
			return fmt.Errorf("remote subagent %q: %w", name, err)
		}
		cacheDir, err := resolver.Resolve(ctx, src, pin)
		if err != nil {
			return fmt.Errorf("remote subagent %q: %w", name, err)
		}
		stagedByName[name] = staged{src: src, pin: pin, cacheDir: cacheDir}
	}

	// Activate: every remote is staged and verified — now mutate active content
	// and the lock. Prune de-declared installs first, then materialize each
	// verified remote and record its provenance, then save the lock atomically.
	if err := e.pruneRemoteSubagents(&lock, declared, subagentRoot); err != nil {
		return err
	}
	for _, name := range names {
		s := stagedByName[name]
		if err := materializeRemoteFile(s.cacheDir, name, subagentRoot); err != nil {
			return fmt.Errorf("remote subagent %q: %w", name, err)
		}
		lock.Set(remote.LockEntry{
			Kind:      "subagent",
			Name:      name,
			Locator:   s.src.URL,
			Transport: string(s.src.Transport),
			Digest:    s.pin.String(),
			Size:      fileSize(filepath.Join(subagentRoot, name+".md")),
		})
	}

	// Note: the cache is deliberately NOT garbage-collected here. Keeping a
	// de-declared pin's content lets a config revert roll back from cache with no
	// network. Reclamation is an explicit, separate operation (GCRemoteCache).
	return lock.Save(e.remoteLockPath())
}

// resolveRemoteFrameworks resolves every declared [frameworks.X]
// source="remote:<url>" through the SAME trust pipeline as remote subagents
// (resolver + revocation list), returning name → verified cache dir. Each cache
// dir is a framework root (framework.toml + resources) that FrameworkCatalog
// overlays exactly like a local framework. A digest mismatch, fetch failure, or
// revoked pin returns an error the caller propagates fail-closed — no new
// fetch/verify/digest code lives here; resolver.Resolve verifies the content
// against the pin before returning the dir. A config with no remote frameworks
// returns (nil, nil) with no network access, so the builtin/local path is
// unchanged. The context bounds every fetch.
func (e *Engine) resolveRemoteFrameworks(ctx context.Context) (map[string]string, error) {
	declared := map[string]config.Resource{}
	for name, fw := range e.Cfg.Frameworks {
		if remote.IsRemoteSource(fw.Source) {
			declared[name] = fw
		}
	}
	if len(declared) == 0 {
		return nil, nil
	}

	rev, err := remote.LoadRevocations(e.remoteRevokedPath())
	if err != nil {
		return nil, err
	}
	resolver := &remote.Resolver{
		Cache:       &remote.Cache{Root: e.RemoteCacheRoot},
		Revocations: rev,
		Limits:      remote.DefaultLimits,
	}

	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make(map[string]string, len(names))
	for _, name := range names {
		res := declared[name]
		src, err := remote.ParseRemoteSource(res.Source)
		if err != nil {
			return nil, fmt.Errorf("remote framework %q: %w", name, err)
		}
		pin, err := remote.ParseDigest(res.Digest)
		if err != nil {
			return nil, fmt.Errorf("remote framework %q: %w", name, err)
		}
		// Revoked content must never be served, even from a warm cache: fail
		// closed before Resolve, mirroring materializeRemotes (F30).
		if rev.Contains(pin) {
			return nil, fmt.Errorf("remote framework %q: content is revoked", name)
		}
		cacheDir, err := resolver.Resolve(ctx, src, pin)
		if err != nil {
			return nil, fmt.Errorf("remote framework %q: %w", name, err)
		}
		out[name] = cacheDir
	}
	return out, nil
}

// GCRemoteCache reclaims content-addressed cache entries that no remote lock
// entry references. With dryRun it reports what would be removed without
// deleting. This is an explicit maintenance operation, kept out of apply so a
// config revert can still roll back from a warm cache.
func (e *Engine) GCRemoteCache(dryRun bool) ([]remote.Digest, error) {
	lock, err := remote.LoadLock(e.remoteLockPath())
	if err != nil {
		return nil, err
	}
	cache := &remote.Cache{Root: e.RemoteCacheRoot}
	return cache.GC(lock.Digests(), dryRun)
}

// declaredRemoteSubagents collects the remote subagents declared for the tool,
// de-duplicated by name.
func (e *Engine) declaredRemoteSubagents() (map[string]config.Resource, error) {
	out := map[string]config.Resource{}
	for _, tool := range []string{"opencode"} {
		entries, err := e.Cfg.ExpandedSubagentEntriesForTool(tool)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if remote.IsRemoteSource(entry.Resource.Source) {
				out[entry.Name] = entry.Resource
			}
		}
	}
	return out, nil
}

// pruneRemoteSubagents removes materialized content files and lock entries for
// remote subagents that are recorded but no longer declared.
func (e *Engine) pruneRemoteSubagents(lock *remote.Lock, declared map[string]config.Resource, subagentRoot string) error {
	for key, entry := range lock.Entries {
		if entry.Kind != "subagent" {
			continue
		}
		if _, ok := declared[entry.Name]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(subagentRoot, entry.Name+".md")); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remote: pruning %q: %w", entry.Name, err)
		}
		delete(lock.Entries, key)
	}
	return nil
}

// materializeRemoteFile copies <cacheDir>/<name>.md into the remote content root
// atomically. A remote subagent archive must contain <name>.md at its root.
func materializeRemoteFile(cacheDir, name, destRoot string) error {
	srcFile := filepath.Join(cacheDir, name+".md")
	data, err := os.ReadFile(srcFile)
	if err != nil {
		return fmt.Errorf("remote archive must contain %q at its root: %w", name+".md", err)
	}
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return err
	}
	// The remote content root is under .homonto (control plane): write no-follow
	// so a planted symlink cannot redirect the materialized file.
	return fsutil.WriteControlPlane(filepath.Join(destRoot, name+".md"), data, 0o600)
}

func fileSize(p string) int64 {
	if info, err := os.Stat(p); err == nil {
		return info.Size()
	}
	return 0
}
