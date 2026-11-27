# ADR 0092 — Matter secrets sealed at rest under the master key

- **Status**: accepted (2026-11-26)
- **Related**: [ADR 0027 — encrypt config secrets at rest](./0027-encrypt-config-secrets-at-rest.md),
  [ADR 0084 — the Matter device layer and node facade](./0084-matter-device-layer-and-node-facade.md)

## Context

The Matter bridge keeps its credentials in the daemon's own database,
`openccu-loom.db`, through go-fabric's credential store: the fabrics'
operational private keys, their IPKs, group keys, CASE resumption secrets,
ICD keys and DoorLock credential data. They are stored in clear. A copy of
the file — a backup, a snapshot, a support bundle, the free pages and the
`-wal` of a disk image — hands whoever holds it every paired controller's
trust in this bridge. ADR 0027 sealed the configuration secrets in the same
file under a master key kept outside it, and left the Matter store out
because go-fabric could not seal it then.

go-fabric v0.26.0 can: `store.NewSealed(ctx, db, sealer)` seals every secret
column under a host-supplied `store.Sealer`, binds each value to its table,
column and row, marks the database with a seal canary and scrubs the file;
`store.Reseal(ctx, db, old, next)` moves a database to a new key in one
transaction. The node facade's own `Store.Sealer` does not apply to this
daemon, which hands the node its credential store (`Store.Credentials`), so
the daemon opens the store sealed itself. Sealing is one-way: there is no
unseal, and every later start needs the key (go-fabric `store/seal.go`,
`docs/security.md` "Sealing at rest").

## Decision

### Key custody: derived from the master key

The sealing key is derived from ADR 0027's master key
(`OPENCCU_LOOM_SECRET_KEY`, else `<data_dir>/secret.key`) with HKDF-SHA256
and a label of its own (`openccu-loom matter-store seal v1`); the store
seals with go-fabric's AES-256-GCM sealer under it. One secret stays the
only thing an operator keeps outside the database, and the two uses of it
are cryptographically separate.

### Activation: whenever a master key exists

As for the configuration secrets, sealing needs no switch: a start that
resolves a master key opens the Matter store sealed, which seals an
existing clear database on the way in and scrubs the file. A start without
a master key keeps today's behaviour (clear, with ADR 0027's warning) as
long as the database was never sealed. A scrub go-fabric cannot finish (a
reader holding the `-wal`) is no error: it stays owed, every start retries
it, and the node pages warn while it is owed.

### Fail closed when the key does not open the store

A sealed database that meets no key, or another key, does not start the
bridge (`ErrNoSealer`, `ErrUnseal`). The rest of the daemon runs; the
health component `matter.bridge` reports the reason and what to do. Nothing is
ever erased automatically. The recovery path is the key: restore it, or set
the environment variable the store was sealed under. When the key is gone
for good, `openccu-loom matter reset`, run with the daemon stopped, erases
the Matter credential store and endpoint records so the bridge comes back
unpaired; the device selection stays.

### Rotation: one offline command for both stores

`openccu-loom secret rotate`, run with the daemon stopped, moves both
sealed stores to a new master key: the configuration secrets are re-sealed
in one transaction and the Matter store with `store.Reseal`, then the file
is scrubbed. The new key replaces `secret.key`; for an environment-managed
key the command takes or prints the new value and the operator sets it
before the next start. Both keys are kept until every step has committed,
so an interrupted rotation is finished by running the command again rather
than leaving a database that neither key opens.

### Backup and restore

Unchanged by construction. The CLI archive never contains the key and is
itself sealed with the master key; restoring it already needs that key, and
the same key opens the Matter store inside it. An archive written before a
rotation opens with the key it was written under, so an operator keeps the
old key for as long as they keep such archives.

## Is this the best architecture?

**Better than the comparison targets.** The CCU's WebUI and the
aiohomematic family hold no Matter state to compare. go-fabric's reference
bridge seals under a key file of its own (`--seal-key file:<path>`,
go-fabric `CHANGELOG.md` v0.26.0); deriving from the one key an operator
already keeps gives the same protection without a second thing to lose.

**Better than before.** A copied database, backup or disk image no longer
gives away the fabric keys. Rotation, deferred in ADR 0027, exists for the
first time and covers every sealed value the daemon holds in one operation.
The key-loss case, until now "pairings silently lost", becomes a named
health state with a documented way out.

**The right cut.** go-fabric owns what is sealed, the canary, the scrub and
`Reseal`; the daemon owns the key: where it lives, how it is derived, when
it rotates and what a missing key means for the rest of the process.
`internal/secret` stays the single owner of the master key and gains the
derivation; the Matter composition root only asks it for a sealer.

## Consequences

- Every start after the first sealed one needs the master key. Losing it
  loses the Matter pairings (recoverable only by re-pairing after
  `openccu-loom matter reset`), as it already loses the sealed
  configuration secrets.
- A database sealed by this release cannot be read by an older one. The
  daemon does not support downgrades across migrations; this makes one more
  thing that would break.
- Copies taken before the first sealed start keep the clear values; the
  scrub removes them from the live file only.
- Sealing protects the confidentiality of a copy, not its integrity: the
  non-secret columns and the seal marks are not authenticated.
