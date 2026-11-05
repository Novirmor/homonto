---
name: example-triage
description: Example workflow process — read a tracker issue, classify it with a typed decision, run the matching configured triage process, hand the work to an installed engine.
---

# example-triage

1. Read the issue through `tracker-cli issue view` and save its text to a
   file.
2. Classify it: run `scripts/classify.sh <issue-file>` from this skill's
   directory. It asks the configured decision model
   (`example-workflow.classify`, a choice of `bug`, `feature`, `question`)
   and prints the process name. When it prints `ask`, the model was
   uncertain or unavailable: ask the user which process applies. Never guess.
3. Read the configured processes with
   `homonto explain settings example-workflow --json` and follow the
   `steps` of the process named in step 2.
4. Ask the coordinator to select one installed engine for the change, with
   the requested endpoint. Never name an engine.
