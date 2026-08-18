# Backup & Restore

How to back up the OpenCCU-Loom daemon's own state, restore it, and
keep the encryption key together with the data so secrets survive the
round-trip.

!!! info "Who this page is for"
    Operators responsible for disaster recovery and migrations. This
    page covers the **daemon's** state (its database and key). Backing
    up the **CCU itself** is a separate, CCU-side operation, exposed
    over the REST API and described at the end.

!!! warning "Before you upgrade"
    Run `openccu-loom backup create` before upgrading the daemon to a
    new version. Every schema migration under
    `internal/store/sqlite/migrations/` carries a `Down` block, but the
    daemon only ever runs `goose.UpContext` at startup — there is no
    `backup rollback` or `migrate down` subcommand exposed to
    operators. If a migration or the new version misbehaves, the only
    supported recovery path is `openccu-loom backup restore` from a
    backup taken **before** the upgrade, not an automatic downgrade.

## What to back up

OpenCCU-Loom keeps all persistent state in its data directory
(`data_dir`, default `./var`). The two files that matter for a full
restore are:

| File | Contents | Why it matters |
|---|---|---|
| `<data_dir>/openccu-loom.db` | SQLite database: centrals, config sections, users/tokens, sessions, paramset caches, audit log, Matter fabrics | the daemon's entire runtime state |
| `<data_dir>/secret.key` | AES-256 master key (auto-generated, mode `0600`) | decrypts the secret-classed values stored in the database |

!!! danger "Back up `secret.key` together with the database"
    Secret-classed fields (CCU passwords, API tokens, OIDC client
    secret, MQTT password, Matter passcode) are stored encrypted with
    the prefix `enc:v1:`. If you restore the database **without** the
    matching `secret.key`, those values cannot be decrypted and the
    daemon will log `encrypted value but no master key available`.

    If you provide the master key through `OPENCCU_LOOM_SECRET_KEY`
    instead of the key file, back up *that* value securely instead —
    it plays the same role.

The live WAL/SHM sidecar files (`openccu-loom.db-wal`,
`openccu-loom.db-shm`) do **not** need to be backed up; the CLI takes a
clean, checkpointed snapshot.

## Using the `backup` CLI

The daemon binary ships a `backup` subcommand with `create` and
`restore`. It snapshots the database with `VACUUM INTO` (a clean,
WAL-checkpointed copy) and packages it as a gzipped tar archive with a
`manifest.json` carrying per-file SHA-256 sums.

### Create

```sh
openccu-loom backup create --config /etc/openccu-loom/config.yaml
```

| Flag | Default | Purpose |
|---|---|---|
| `--config` | — | path to `config.yaml` (resolves `data_dir`; without it, `./var` is used) |
| `--out` | `backup-<hostname>-<utc>.tar.gz` | output archive path |
| `--include-secrets` | off | adds a `secrets/` placeholder note (env-resolved secrets are not stored) |
| `--json` | off | emit a single-line JSON result for scripting |

The archive contains `state/openccu-loom.db` (the VACUUMed snapshot),
any other files under `data_dir` **except `secret.key`**, optionally
the `config.yaml` you passed, and `manifest.json`.

!!! danger "The archive is NOT self-contained for decryption"
    `backup create` deliberately **skips `secret.key`** while walking
    `data_dir` — bundling the at-rest master key alongside the
    ciphertext it protects would let anyone who steals the archive
    decrypt it, defeating the point of encrypting it. When a master
    key is available, the whole archive is instead sealed with
    AES-256-GCM using that key on the way out.

    This means `secret.key` (or the `OPENCCU_LOOM_SECRET_KEY` value,
    if that's how you provision it) **must be preserved out-of-band**,
    separately from the archive. Without it you cannot open the
    archive on restore, and even after a successful restore you
    cannot decrypt the `enc:v1:`-prefixed secret fields inside the
    database. `backup create` prints a one-line reminder of this to
    stderr on every run.

    Secrets that you supply only through environment variables (e.g.
    `OPENCCU_LOOM_MQTT_PASSWORD`) are never written to the database or
    the archive either — re-supply those env vars before starting the
    restored daemon.

### Restore

Stop the daemon first, then:

```sh
openccu-loom backup restore --config /etc/openccu-loom/config.yaml backup-host-20260608T120000Z.tar.gz
```

| Flag | Default | Purpose |
|---|---|---|
| `--config` | — | path to `config.yaml`; also receives an extracted `config.yaml` if present |
| `--data-dir` | from `--config` or `./var` | override the restore target |
| `--force` | off | overwrite an existing `openccu-loom.db` |

Restore validates every file's SHA-256 against the manifest, writes to
a staging directory, then atomically renames the database into place.
If `openccu-loom.db` already exists it refuses unless `--force` is
given. Start the daemon again after a successful restore.

!!! info "Restore also clears the old WAL/SHM sidecars"
    A database restored next to the previous database's
    `openccu-loom.db-wal` / `-shm` would not be the database you
    restored: SQLite recovers a write-ahead log it finds beside a
    database file on the next open and replays the *old* pages into the
    new file. Restore therefore moves those sidecars aside together with
    the database, in the same all-or-nothing set — a failed commit puts
    the original database and its sidecars back together, a successful
    one discards them. You do not have to delete them by hand, and you
    must not copy them back in afterwards.

## A scheduled backup (cron)

```sh
# /etc/cron.d/openccu-loom-backup
# nightly at 03:15, keep archives under /var/backups/openccu-loom
15 3 * * * openccu  openccu-loom backup create \
  --config /etc/openccu-loom/config.yaml \
  --out /var/backups/openccu-loom/backup-$(date -u +\%Y\%m\%dT\%H\%M\%SZ).tar.gz
```

Prune old archives with your usual retention tooling
(e.g. `find /var/backups/openccu-loom -name 'backup-*.tar.gz' -mtime +14 -delete`).

## Docker volumes

Persist the data directory on a named volume so the database and
`secret.key` survive container recreation, and run the backup against
that volume.

=== "Docker Compose"

    ```yaml
    services:
      openccu-loom:
        image: ghcr.io/sukramj/openccu-loom:latest
        environment:
          OPENCCU_LOOM_DATA_DIR: /data
        volumes:
          - openccu-loom-data:/data
    volumes:
      openccu-loom-data:
    ```

=== "Backup the volume"

    ```sh
    # archive inside the container against the mounted /data volume
    docker compose exec openccu-loom \
      openccu-loom backup create --out /data/backup-$(date -u +%Y%m%dT%H%M%SZ).tar.gz
    # copy the archive (and the whole volume) off-host afterwards
    docker compose cp openccu-loom:/data/backup-...tar.gz ./
    ```

!!! tip "Pin the master key in containers"
    For ephemeral container filesystems, set `OPENCCU_LOOM_SECRET_KEY`
    (a base64 32-byte key from `openssl rand -base64 32`) so the
    encryption key does not depend on an auto-generated file inside the
    volume. Keep that value in your secrets manager.

## Exporting just the configuration

If you want to migrate configuration (sections + centrals) rather than
the whole database, use `openccu-loom config export` / `import`. The
export omits password hashes and token secrets; user and token rows are
skipped on import for safety. This is the right tool for moving config
between instances, not for disaster recovery.

```sh
openccu-loom config export --config /etc/openccu-loom/config.yaml --out config-dump.json
openccu-loom config import --config /etc/openccu-loom/config.yaml config-dump.json
```

### The redacted round-trip

By default the export is **redacted**: every secret-classed leaf (CCU
password, MQTT password, OIDC client secret, Matter passcode, …) is
withheld — written as JSON `null`, with an empty `password_plain` on
each central — and the document is stamped `"redacted": true`. Pass
`--include-secrets` to export the cleartext instead; that file then
carries live credentials and belongs in a secrets store, not in a chat
window.

Importing a redacted document **keeps the credentials already stored in
the target database**. The import reads the stored values first and
merges them into every withheld leaf, so re-importing your own export
cannot silently null out the daemon's own credentials. Two consequences
worth knowing:

- A redacted export cannot *transfer* secrets to a fresh instance.
  Importing one into an empty database leaves those fields empty — set
  them in the SPA afterwards, or export with `--include-secrets`.
- Clearing a credential is not something an import can express. A
  withheld leaf means "unchanged", not "delete"; clear it on the target
  instance instead.

`--replace` deletes the stored sections and centrals before writing, but
the merge still applies: the stored secrets are snapshotted before the
delete, so a redacted `--replace` import does not lose them either. Use
`--dry-run` to see how many sections and centrals a document would
touch before it writes anything. `import` prints a reminder to stderr
whenever it is fed a redacted document.

## CCU-side backups (REST)

Separately from daemon backups, OpenCCU-Loom can trigger and store a
backup **of the CCU** (the Homematic `.sbk` archive) through the REST
API. These are admin-gated:

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/v1/backups` | trigger a CCU backup (returns a job id) |
| `GET` | `/api/v1/backups` | list locally stored CCU backups (daemon-global, across all configured centrals) |
| `GET` | `/api/v1/backups/{id}/download` | stream a stored `.sbk` |
| `POST` | `/api/v1/backups/{id}/restore` | restore a stored CCU backup by its id |

`POST /api/v1/backups` accepts an optional JSON body
`{"central_name": "..."}` to target a specific central explicitly; an
omitted or empty body backs up the first registered central (the
multi-CCU-correct path is naming the central).

A listed entry carries `filename`, the archive's name in the CCU's own
convention (`<hostname>-<CCU firmware version>-<YYYY-MM-DD-HHMM>.sbk`),
recorded when the archive was taken; the download is served under it. Show
or store that name rather than deriving one from `id`, which is a storage
key and carries no firmware version. It is absent for archives taken
before the field existed — fall back to `<id>.sbk`.

### Where the archives are stored

By default in `<data_dir>/backups`. Set `backup.dir` to move them
elsewhere, for instance onto a mount that is not the daemon's state
directory.

The default is the wrong one in a single case that matters: installed as
CCU add-on software the daemon's data directory sits under
`/usr/local/addons/`, which is exactly the tree the CCU packs into its own
`.sbk`. Every CCU backup would then carry all previously downloaded
archives, and the next one would carry those again. Two things prevent it:

- the daemon writes a `.nobackup` marker into whichever directory it uses,
  which the CCU's own `tar` honours (`--exclude-tag`), and
- the CCU add-on's service script resolves `backup.dir` to the CCU's own
  backup target at every start — `/usr/local/etc/config/CronBackupPath`
  when the operator has set one, otherwise external storage when the
  system reports any — so the archives land beside the CCU's own rather
  than on its internal flash.

See the REST + WebSocket API reference (`docs/integrations/rest-ws.md`)
for request/response detail.

## See also

- [Configuration reference](configuration.md) — `data_dir` and secrets.
- [Security guide](../SECURITY.md) — at-rest encryption details.
- [Troubleshooting](troubleshooting.md) — recovering from a lost `secret.key`.
