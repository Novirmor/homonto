# Pull request body contract

When repository policy selects `integration: pr`, assemble the pull request body
from the archived change:

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

Use this body directly with the repository's PR command. If no remote or PR tool
is available, write it to
`<workflow-root>/changes/archive/YYYY-MM-DD-<name>/ship.md` and commit it. This is the
archive contract's sanctioned post-archive addition. Report the fallback as an
integration gap; do not ask whether to prepare text that the selected PR path
already requires.
