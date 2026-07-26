No code-intelligence provider is declared. Ground every codebase claim in
direct file reading — read the relevant files rather than guessing, and record
`grounding: direct file reading (no provider declared)` in the change's
Grounding section. There is nothing to probe and nothing to warn about.

To ground against an index instead, set `code_intel` under `[tooling]` in
`homonto.toml` and re-apply.
