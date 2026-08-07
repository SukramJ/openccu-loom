# ADR 0061 — The migration Down path is unsupported in production

- Status: accepted
- Date: 2026-08-07

## Context

Every `goose` migration under `internal/store/sqlite/migrations/` and
`internal/store/sqlite/migrations_history/` carries a syntactically valid
`-- +goose Down` block, because `goose` requires one. An audit of all of
them found that most Down blocks are not actually reversible: they drop
the table or column the matching Up added, which destroys whatever data
that table or column held in the meantime.

For some tables that is a cheap re-fetch — `values_cache` and
`master_values` are CCU-side caches; dropping them just forces a re-read on
next boot. For others it is not reversible by any means available to the
daemon:

- `users` / `tokens` (migration 017): `password_hash` and `token_hash` are
  bcrypt hashes with no plaintext stored anywhere. There is nothing to
  reconstruct them from.
- `alarm_journal` / `alarm_incidents` (migration 027): the journal is
  documented as append-only, deletable only through the privileged
  retention path in normal operation; the incident ledger carries
  safety-critical counters (silenced flag, retrigger cycles, acoustic
  seconds) with no other source.
- `alarm_codes.hash` (migration 028): argon2id PIN hashes, same
  irreversibility as the bcrypt case above.
- `matter_node_identities` (migration 006): the NOC, the operational
  private key, and the Identity Protection Key. The only way back is
  re-commissioning every fabric.
- `config_sections` (migration 002 for `audit_log`; migration 020 for the
  `schema_version` column): a Down→Up round trip on migration 020 does not
  merely drop a column — every surviving `config_sections` row comes back
  at `schema_version = 0`, and `ConfigSectionStore.WipeOutdatedSections`
  deletes every row whose version does not match the current one on the
  next boot. For an installation with no rows older than migration 020,
  that is the entire saved configuration.
- `alarm_zones.slug` (migration 034): the slug is a frozen,
  external-identifier-safe name baked into Home Assistant entity ids and
  MQTT topics. The Up migration derives it once from the zone name at
  creation time specifically so a later rename does not change it. Down
  drops it; the next Up re-derives it from whatever the zone is named
  *then*, which is a different value for any zone renamed in between —
  orphaning every entity of that zone in a consumer's registry, the exact
  failure the freeze exists to prevent.

None of this was ever exposed to an operator: there is no `make`
target, no `hmcli` subcommand, and no documented procedure that runs
`goose down`. `Makefile:47` installs the `goose` binary only as a
development dependency for authoring and testing new migrations. The risk
was real in principle — the Down blocks exist and `goose down`/`goose
down-to` work exactly as goose intends — but latent in practice.

## Decision

The migration Down path is deliberately unsupported for production and
operator use. It exists only so `goose` migrations stay well-formed and so
a contributor can exercise Up→Down→Up locally while developing or testing
a new migration.

Concretely:

1. Every Down block that drops a table or column carries a short, factual
   comment directly above `-- +goose Down` naming what is destroyed and,
   where relevant, why it cannot be recovered. This is enforced by
   `TestMigrationDownDropsHaveLossNotes` in `tests/contract/`.
2. No tooling is added to make `goose down` easier to reach — no Make
   target, no `hmcli` subcommand, no CI job that exercises it against a
   real database. The existing `goose` CLI dependency (installed by `make
   setup`) remains the only way to invoke it, and `CONTRIBUTING.md`
   documents that this is by design, not an oversight.
3. Restoring a daemon to an earlier schema version is a backup-and-restore
   operation (file-level SQLite backup, or the daemon's own backup/restore
   surface where one exists), never a `goose down` invocation against a
   live database.
4. `config_sections` migration 020 is deliberately left as-is rather than
   patched to dodge the Down→Up config wipe: the `schema_version = 0`
   default it writes is exactly what makes `WipeOutdatedSections` do its
   real job on a genuine upgrade from a pre-versioning database, discarding
   rows whose serialisation format cannot be trusted. There is no value
   that reads as "known-stale" for a real upgrade and "known-current" for
   a Down→Up round trip at the same time; changing the default to dodge
   the round-trip case would silently defeat the wipe on the upgrade case
   it exists for.

## Consequences

- A contributor developing a migration still gets a real Up→Down→Up cycle
  to test against — the Down blocks work, they are just understood to be a
  development tool, not an operator rollback mechanism.
- An operator who runs `goose down` outside of documented guidance does so
  against an explicit warning in the migration file itself, not silently.
- Future migrations that drop a table or column must add the same kind of
  note or `TestMigrationDownDropsHaveLossNotes` fails the build — the rule
  is mechanically enforced going forward, not just documented for the
  audited set.
- Genuinely reversible Down blocks (renames, additive-only rollbacks — see
  `migrations/031_alarm_zones_rename.sql`) are unaffected; the guard only
  fires on `DROP TABLE` / `DROP COLUMN`.
