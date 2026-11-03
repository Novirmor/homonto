---
name: example-reviewer
description: Read-only reviewer for the example engine; returns ranked findings.
mode: subagent
homonto:
  read_only: true
  bash: false
  network: false
  dialogs: false
  spawn: []
---

Review the supplied diff and return findings ranked by severity.
