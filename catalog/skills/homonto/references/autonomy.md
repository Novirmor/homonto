# Autonomous workflow policy

Once a user starts or resumes a workflow, continue through its remaining phases
in the same invocation. A phase command selects the entry phase, not the stopping
point, unless the user names an endpoint or asks to pause.

## Decide before asking

Investigate repository evidence first. Then choose the safest reversible option
that satisfies the request and repository policy. This includes implementation
approach, workspace name, branch or worktree isolation, build mode, test mode,
task decomposition, and local integration mechanics.

Ask the user only when the missing answer cannot be recovered from the request,
code, tests, documentation, or established repository policy and different
answers would materially change one of these:

- product behavior, acceptance criteria, or scope
- compatibility, security posture, cost, or an external commitment
- acceptance of a known deviation or waiver of a required obligation
- abandonment, destructive recovery, or treatment of unattributed work that
  blocks safe progress

Do not ask for approval of a summary, proposal, plan, diff, phase transition, or
close plan that already matches the request. Present important decisions and
their evidence while continuing. Do not turn a reversible technical choice into
a multiple-choice question.

## Root and bootstrap

Do not ask where the project root is. Use the directory containing the active
`homonto.toml`; otherwise use the Git worktree top level; otherwise use the
working directory supplied by the host. Pass that directory explicitly to a
workflow command's `--dir` flag when needed.

Do not offer or run `git init` unless the user explicitly requests a new Git
repository. `homonto init [dir]` only scaffolds `homonto.toml`, `.gitignore`,
and local content; it does not initialize Git or install a framework.
Framework installation is declarative: add the requested `[frameworks.onto]`
or `[frameworks.to]` entry, inspect `homonto plan`, then run `homonto apply`.
If Git is a required later gate and no worktree exists, report that concrete
blocker rather than asking permission to initialize one.

## Handle uncertainty

A subagent's `Questions:` section is input to the coordinator, not automatically
a user question. Resolve factual and technical uncertainty by reading, testing,
or dispatching focused exploration. Ask the user only if the unresolved part
meets the test above.

On a failure, investigate and fix the root cause. A hard prerequisite or safety
failure may stop the run, but it is a blocker to report, not a request for
permission to continue. If a fix would cross the agreed scope boundary, ask
about that scope change; otherwise repair, re-run the evidence, and continue.

## Preserve state

Record every required workflow token with a truthful summary of the evidence or
decision. A token proves that the review happened; it does not imply that the
user personally supplied the decision. Never fabricate passing evidence, accept
a deviation silently, waive an obligation without authorization, discard
unattributed work, or use an exceptional recovery path as an automatic shortcut.

After compaction or restart, re-derive state from repository artifacts and
continue from the first incomplete step. Do not ask whether to resume merely
because the session is new.
