# ADR 0027 — Encrypt config secrets at rest

> **Historical paths.** Since 0.74.0 the Matter stack is a dependency, not a
> subtree: it lives in the [go-fabric](https://github.com/SukramJ/go-fabric)
> module and `internal/north/matter/` no longer exists in this repository. The
> paths below are left as they were when the decision was made — a record
> rewritten to match today's tree stops being a record. For where each piece
> lives now, see
> [`SPECIFICATION.md`](https://github.com/SukramJ/openccu-loom/blob/main/SPECIFICATION.md)
> §6.

- **Status**: accepted
- **Date**: 2026-06-05
- **Related**:
  [ADR 0002 — multi-CCU first class](./0002-multi-ccu-first-class.md),
  `internal/config` (`cfg:"secret"` field classification),
  `internal/configstore` (DB-tier config assembly),
  `SPECIFICATION.md` §2 (constraints)

## Context

Runtime-mutable configuration lives in SQLite (`config_sections`
JSON snapshots + the `centrals` table). Secret-classed fields —
`cfg:"secret"`: CCU passwords, MQTT password, OIDC `client_secret`,
REST `auth.users`/`tokens`, Matter attestation material — could be
stored there in plaintext:

- The SPA's `PUT /config/sections/{section}` persists whatever JSON it
  receives verbatim; the read endpoint masks secrets for *display*, but
  the stored value is not encrypted.
- The `centrals` table keeps a `password_plain` column (used when
  `allow_plaintext_secrets` is on, and as the seed target for a
  YAML-supplied CCU password).

The recommended path has always been env-var resolution (secrets never
touch the DB), but operators *can* and *do* enter passwords in the SPA,
so plaintext secrets reach the database. A leaked DB file, backup, or
`config export` then leaks those secrets.

This is compounded by ADR-0027's sibling change in the same workstream:
**first-run seeding** copies a full `config.yaml` into the DB so a new
environment can be stood up from one file. Without encryption that seed
would write YAML secrets to the DB in plaintext.

## Decision

Encrypt secret-classed config values **at rest** in the database.

### Threat model

- **Protects**: DB files, backups, snapshots, and `config export`
  output shared or stolen *without* the master key.
- **Does NOT protect**: an attacker who also holds the master key, or
  who can read the running process's memory. This is at-rest
  confidentiality, not a secrets-management system.

### Cipher

- **AES-256-GCM** (`crypto/aes` + `cipher.NewGCM`, pure Go — the same
  primitive family already used under `internal/north/matter/secure`).
  Random 12-byte nonce per value.
- Stored form: `enc:v1:<base64(nonce ‖ ciphertext ‖ tag)>`. The
  `enc:v1:` prefix distinguishes ciphertext from plaintext (enabling
  lazy migration) and versions the scheme for future algorithm changes.

### Master key — hybrid resolution

1. `OPENCCU_LOOM_SECRET_KEY` — base64-encoded 32 bytes. Operator-managed
   (12-factor); one key protects every DB secret.
2. Otherwise an auto-generated key file `<data_dir>/secret.key`
   (32 random bytes, mode `0600`), created on first run. Zero-config
   default that protects DB backups/exports out of the box.

### Resilient fallback

If no key is available and none can be created (e.g. a read-only
`data_dir` with no env key), the daemon logs a WARNING and falls back to
plaintext storage rather than failing to boot. Encryption is a
hardening layer, not a boot dependency.

### Scope of encrypted fields

All `cfg:"secret"` leaf fields, discovered by reflection over the `cfg`
tag (the same convention `internal/config/classify.go` already walks) —
so new secret fields are covered automatically:

- **string** fields and **`map[string]string`** values are sealed
  directly.
- **integer** fields (e.g. the Matter commissioning `passcode`, a
  `uint32`) are sealed as their decimal-string form: the JSON leaf
  becomes an `enc:v1:` string on write and is decoded back to a number on
  read. The non-destructive transform only changes the leaf's type when it
  is actually encrypted, so an unsealed value (no key) stays a number.

Empty values are left empty (never encrypted), so env-only secrets stay
absent from the DB and continue to resolve from their env var at load
time. The `centrals.password_plain` column is sealed the same way.

**Out of scope:** non-integer numeric secrets (floats) — none exist in
the config tree today; they are skipped rather than truncated.

### Integration

- Sealing/opening is applied at the persistence boundary via thin
  decorators around the section and centrals stores, so the SPA write
  path, the first-run seed, and the CLI import are all covered without
  per-call-site changes.
- Reads decrypt after load; a value without the `enc:v1:` prefix passes
  through unchanged, so pre-existing plaintext rows keep working and are
  re-encrypted lazily on their next write.
- Env-var overrides (`resolveEnvSecrets`) and the SPA's display masking
  (`maskSecrets`) operate on the decrypted, in-memory config and are
  unaffected.

## Consequences

- DB dumps / backups / `config export` no longer leak secrets unless the
  master key leaks too.
- Operators who relied on plaintext-in-DB lose nothing functionally; the
  value is transparently sealed on write and opened on read.
- **Key loss** ⇒ sealed secrets become unrecoverable and must be
  re-entered (via the SPA or a fresh seed). This is the cost of at-rest
  encryption and is documented in the example configs.
- A constant operating concern: the auto-generated `secret.key` must be
  included in any backup that also contains the DB, or restored secrets
  will not decrypt. The example configs call this out.
- Key rotation and envelope/passphrase-wrapped keys are deliberately
  deferred; v1 uses a single raw key. Rotation would re-encrypt all
  sealed rows under a new `enc:v2:` scheme.
