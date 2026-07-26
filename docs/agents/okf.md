# Grounding with OKF

OKF (Open Knowledge Format) bundles are this repository's default way to ground
claims about its own code. The point is to answer "what does this do and who
calls it" from an index rather than from memory or a guess.

homonto also declares OKF as a tooling provider it can require of *user*
repositories ([`catalog/tooling/okf.md`](../../catalog/tooling/okf.md)). Using
it here keeps our own practice and what we ship consistent.

## Using it

The bundle lives at `okf_bundle/` in the repo root and is **gitignored** — it is
generated from the code, so committing it would just create a second thing to
keep in sync.

- Look a concept up: `okf_lookup.py <name>`
- Generate or regenerate: `okf_generator.py`

The `okf-generator` skill is external and installed per developer
(<https://github.com/UmairBaig8/okf-generator>). homonto never installs,
updates, or runs it.

## Absence and staleness

Both are normal. Neither is a reason to stop.

- **No bundle and no skill** — read the files directly and say so.
- **Bundle exists** — use it, and prefer it over grep for "where is X" and
  "what depends on X".
- **Stale bundle** — treat as absent. A bundle predating recent work describes
  code that no longer exists, and a confidently stale answer is worse than an
  admitted gap. Rule of thumb: older than roughly the last 20 commits, or weeks
  old. Regenerate, or fall back to reading.

Generating a bundle is the user's call, not yours. If one is missing or stale
and it would materially change your answer, ask before generating; if the user
declines, fall back to direct reading and record that.

## Saying which grounding you used

When grounding affects a conclusion, name it: "from the OKF bundle
(regenerated today)", "the bundle is three weeks old so I read the files
directly". This costs one clause and tells the reader how much to trust the
claim.

Direct file reading is always a legitimate fallback. What is not legitimate is
implying an index confirmed something when it did not.
