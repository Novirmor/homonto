# Remote sources

Agents and packages can come from a `remote:` source — content homonto
fetches over the network. Remote content is untrusted, so every remote install
is **pinned, verified, and fail-closed**: nothing reaches OpenCode until the
fetched content matches its declared digest and passes every safety check.

## Declaring

```toml
[subagents.reviewer]
source = "remote:https://example.com/agents/reviewer.tar.gz"
digest = "sha256:9f2c…"        # required
scope  = "project"

[engines.house-workflow]
source = "remote:git+https://github.com/acme/workflows#4b1e9c0d2f…"
digest = "sha256:51aa…"
scope  = "project"
```

| Transport | Example |
|---|---|
| HTTPS tarball | `remote:https://host/path.tar.gz` (or `.tgz`) |
| Git commit | `remote:git+https://host/repo#<commit>` |
| Local file | `remote:file:///mirror/agents.tar.gz` |

Plain `http://` is rejected. A `remote:` source without a valid
`sha256:<64 hex>` digest fails at load. A remote **agent** archive must have
`<name>.md` at its root; a remote **package** is a package root with its own
`framework.toml`.

## Verify before mutate

Every resolve runs the same steps, and any failure stops before a cache or
target write:

1. **Cache lookup** — a digest already in `.homonto/cache/remote/` resolves
   offline.
2. **Fetch** — bounded by redirect count, timeout, and size.
3. **Validate the archive** — reject absolute paths, `..`, symlinks,
   hardlinks, devices; cap entry size, total size, and entry count.
4. **Canonical digest** — sha256 over a deterministic tree serialization,
   independent of transport.
5. **Pin match** — must equal `digest`.
6. **Revocation** — the digest must not be listed in `.homonto/revoked.json`.

Provenance goes into `.homonto/remote.lock.json` (locator, transport,
digest, size).

## Threats and controls

| Attack | Control |
|---|---|
| Path traversal, absolute paths | Rejected during extraction |
| Symlink, hardlink, device escape | Non-regular entries rejected |
| Archive or decompression bomb | Per-entry, total, and count caps; gzip bounded while streaming |
| Tampered payload, compromised host, redirect swap | Content digest must match the pin; the host isn't trusted |
| Revoked content | Checked on fetch and cache hit |
| Moved Git tag or branch | Pin is a content digest; refs aren't trusted |

## Lifecycle

| Task | How |
|---|---|
| First install | Obtain the digest out of band if you can; otherwise the first fetch is trust-on-first-use. |
| Update | Edit `source` and `digest` together, then apply. homonto never moves to newer content on its own. |
| Roll back | Restore the previous `digest`; it resolves from cache. |
| Revoke | Add the digest to `.homonto/revoked.json`; the next resolve fails, even from cache. |
| Remove | Delete the declaration; apply prunes the install and its lock entry. |
| Reclaim cache | `homonto cache gc` (`--dry-run` first). Kept out of apply so rollbacks still work. |

## Limits

The content digest is the trust root. There is no signature or provenance
attestation layer yet.
