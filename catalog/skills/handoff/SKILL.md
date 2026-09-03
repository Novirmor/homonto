---
name: handoff
description: Compact the current conversation into a handoff document so a fresh agent with no prior context can continue the work. Use when ending a session, switching agents, or before a context reset.
metadata:
  source: adapted from https://github.com/mattpocock/skills (MIT), skills/productivity/handoff
---

# handoff

Write a handoff document so a fresh agent with no memory of this conversation
can pick up the work and keep going. Save it to the OS temp directory (not the
workspace), named `handoff-<YYYYMMDD-HHMMSS>.md`.

Write for a reader who has the repository but none of this chat. Capture the
decisions and the reasons behind them, not a transcript. If a fact already lives
in a durable artifact — a spec, plan, ADR, issue, commit, or diff — reference it
by path or URL instead of restating it.

## Sections

- **Goal** — what the work is trying to achieve, in one or two sentences.
- **Current state** — where things stand right now: what runs, what's committed,
  what branch, what's green or red.
- **Done** — what this session finished, with pointers (commit shas, file paths).
- **Next** — the concrete next steps, ordered, specific enough to start on.
- **Key files & pointers** — the paths, symbols, and artifacts the next agent
  needs, each with one line on why it matters.
- **Open questions & decisions** — unresolved choices, and the decisions already
  made with their rationale so the next agent doesn't relitigate them.
- **Gotchas** — traps, flaky steps, non-obvious constraints, and anything that
  cost time to learn.
- **Suggested skills** — skills the next agent should invoke, and when.

## Rules

- Self-contained: a cold agent reading only this doc and the repo can continue.
  If a section would send the reader back to this chat, write the answer instead.
- Redact secrets: no API keys, tokens, passwords, or personal data.
- If the user passed an argument, treat it as the next session's focus and
  weight the Next and Key-files sections toward it.
- End by printing the saved path.
