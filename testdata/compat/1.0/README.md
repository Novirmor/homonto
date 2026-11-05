# 1.0 compatibility fixtures

Files in the formats homonto 1.0 writes and reads. `internal/compat` loads
each with the current binary's own loaders. A 1.x release must keep reading
every one of them with the same meaning (ADR 0109).

**Never edit these files to make a test pass.** A failure here means a
change broke the 1.x compatibility promise. Fix the change, or, if the break
is intended, it belongs in 2.0. A later 1.x release that adds a format
version adds a new sibling directory (for example `1.3/`), and its fixtures
are frozen the same way.
