# Updates

The current binary has no user-operable command that fetches, stages, or
installs an update.

```bash
homonto update trust
```

`update trust` reports the signing roots compiled into the binary. A locally
built binary carries no signing root and therefore cannot verify or install an
update. No published workflow release currently carries a signing root either.

## Implemented But Unwired

`internal/update` contains the self-update mechanism: signed manifest
verification, candidate inspection, staging, compatibility checks, journaled
activation, and rollback. The public CLI exposes none of the fetch or activation
steps, so treat that implementation as unavailable product behavior.

An interrupted update journal is recovered during a read-write workspace open.
The recovery path exists even though update activation is not exposed. Use
`homonto doctor` to inspect a reported interrupted update, and see [Recover or
transfer work](../how-to/recover-or-transfer-work.md) for workspace recovery.
