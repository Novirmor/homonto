Confirm codebase-understanding tooling is available: the `okf-generator` skill
is loadable, or an OKF bundle exists at the repo root. The open and design
phases ground every codebase claim in OKF concept lookups (`okf_lookup.py`)
when they are available, rather than guesswork. Indexing is the user's
decision: if only the skill is available and no bundle exists, ask the user
whether to generate one (`okf_generator.py`) before open/design proceeds; if
they decline, grounding falls back to direct file reading and that fallback is
recorded in the proposal/design.

**Staleness counts as absence**: a bundle older than the recent work (rule of
thumb: predates the last ~20 commits or is weeks old) gets the same
ask-to-regenerate treatment — ask or proceed, never a halt — and the Grounding
section records the bundle's age either way — a confidently stale bundle is
worse than none. If neither the skill nor a bundle exists, WARN, record
`grounding: direct file reading (okf unavailable)` in the change's Grounding
section, and proceed.

homonto never installs, updates, or runs okf-generator; it only records that
this repository grounds against it. Install it yourself from
https://github.com/UmairBaig8/okf-generator.
