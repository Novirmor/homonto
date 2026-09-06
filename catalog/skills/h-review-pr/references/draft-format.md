# Review draft format

Every review workflow presents findings in this shape — one finding per
line, severity first, so a reader can stop reading when the severity drops.

```markdown
Findings

- <Severity>: `path/to/file.ext:123` — <the defect and its impact>.
  <Smallest suggested fix, when useful.>

Open Questions

- <Question or assumption, if any.>

Verification

- <Commands run and their outcomes, or `Not run: <reason>`.>

Posting Recommendation

- <Recommend posting / not posting yet, with reason.>
```

Severity labels:

- `critical` — likely bug, security issue, data loss, broken API/contract,
  unmet acceptance criteria, or unsafe migration/deploy risk.
- `major` — meaningful correctness, reliability, performance, or
  maintainability risk that should be fixed before merge.
- `minor` — low-risk issue worth considering.

Rules:

- If there are no findings, state that explicitly and name residual risks or
  testing gaps; never invent nits to fill the section.
- Never claim a check passed without observed passing output; a batch draft
  with no local verification says `Not run: review only`.
- The draft is the only thing a publication decision approves. Changing the
  findings after approval reopens the approval.
