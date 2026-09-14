# Review draft format

Every review skill presents findings in this shape — one finding per
line, severity first, so a reader can stop reading when the severity drops.

```markdown
Reviewed: <canonical PR URL>, base <OID>, head <OID>

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
- Approval applies to the shown base/head OIDs. Recheck identity, state, and
  refs before posting; changed refs require a new review and approval. Formal
  reviews bind `commit_id` to the reviewed head, as specified in the shared
  [context-pack contract](context-pack.md#publication-freshness).
