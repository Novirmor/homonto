# Pull request body contract

When repository policy selects `integration: pr`, assemble a neutral base body
from the archived change:

Follow the shared [publication contract](../../homonto/references/publication.md).
Archived `ship.md` contains no closing directive, only an ordinary canonical
original issue URL when available. Origin-specific closing text belongs only in
the per-repo temporary rendering below, never the shared cached archive body.

```markdown
## <change title, from the proposal>

<proposal Why, condensed to 1–2 paragraphs>

### What changed

<proposal What Changes bullets, updated to what actually shipped>

### Verification

<verification.md summary: mode, Result, scenario count, adversarial pass
outcome, regression result>

Full records: `<workflow-root>/changes/archive/YYYY-MM-DD-<name>/`
(proposal · design · verification · notes)
```

Write this neutral base to
`<workflow-root>/changes/archive/YYYY-MM-DD-<name>/ship.md`. In managed mode use
`homonto workspace checkpoint --path changes/archive/YYYY-MM-DD-<name>/ship.md --message "Record PR handoff"`;
existing mode retains the named manual records commit. This is the existing
sanctioned post-archive addition; do not add a `ship/<repo>.md` archive subtree.
Once committed, leave it immutable. An older cached body may contain closing
text: sanitize that text only in the temporary rendering, never mutate history
or infer original issue identity from a cached bare number.

For every destination and every retry, generate a fresh per-repo session-tmp
body from the neutral content and canonical original issue identity. Add bare
`Closes #N` only when the destination ID/host is the confirmed issue origin;
otherwise retain the non-closing canonical URL. Unknown identity suppresses the
directive. Use only this absolute temporary path as `--body-file`, never shared
archived `ship.md`. Validate destination canonical ID/host, target, observed
headOID, body path and body hash in the session publication evidence before
publication and receipt. A filename's existence does not validate a cached body.

Do not imply local records are available in the source PR. Without a remote/PR
tool, report the per-repo temporary body path and pending integration; neutral
`ship.md` alone is not ready to post. Do not ask whether to prepare the text
that the selected PR route already requires.
