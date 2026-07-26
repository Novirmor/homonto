Confirm codebase-understanding tooling is available: the `graphify` skill is
loadable, or a `graphify-out/` directory or `.codegraph/` index exists at the
repo root. The open and design phases ground every codebase claim in
graphify/codegraph queries when they are available, rather than guesswork.
Indexing is the user's decision: if only the skill is available and no index
exists, ask the user whether to build one before open/design proceeds; if they
decline, grounding falls back to direct file reading and that fallback is
recorded in the proposal/design.

**Staleness counts as absence**: an index older than the recent work (rule of
thumb: predates the last ~20 commits or is weeks old) gets the same
ask-to-reindex treatment — ask or proceed, never a halt — and the Grounding
section records the index's age either way — a confidently stale graph is
worse than none. If neither the skill nor an index exists, WARN, record
`grounding: direct file reading (graphify unavailable)` in the change's
Grounding section, and proceed.
