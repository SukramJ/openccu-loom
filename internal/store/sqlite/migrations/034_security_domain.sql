-- Security & Safety domain: the operator-facing source classification
-- and the persistent fault ledger.
--
-- Zones additionally gain a slug. Zone ids are UUIDs, which are fine as
-- internal keys but unusable as external identifiers: they end up in
-- Home Assistant entity ids and in MQTT topics, where a human has to
-- read and type them. The slug is derived once from the zone name and
-- then frozen — renaming a zone must not orphan every entity of that
-- zone in a consumer's registry.

-- +goose Up
ALTER TABLE alarm_zones ADD COLUMN slug TEXT NOT NULL DEFAULT '';

-- Backfill from the name, lower-cased with non-alphanumerics collapsed
-- to underscores. Collisions are resolved by the store on write; the
-- backfill accepts them because a duplicate slug is still better than an
-- empty one, and the uniqueness index below is deliberately absent for
-- exactly that reason.
UPDATE alarm_zones
SET slug = TRIM(
    REPLACE(REPLACE(REPLACE(REPLACE(LOWER(name), ' ', '_'), '-', '_'), '.', '_'), '/', '_'),
    '_')
WHERE slug = '';

-- security_sources: operator overrides on the automatic classification,
-- and inclusion for data points the classifier does not recognise.
--
-- The key is the data point, not a surrogate id: a source is the tuple
-- (central, interface, channel, parameter), and keying on that makes an
-- override idempotent without a lookup.
CREATE TABLE security_sources (
    central_name    TEXT NOT NULL,
    interface_id    TEXT NOT NULL,
    channel_address TEXT NOT NULL,
    parameter       TEXT NOT NULL,
    -- class overrides the classifier verdict; empty keeps it.
    class    TEXT    NOT NULL DEFAULT '',
    -- included = 0 removes the source from every aggregate. The default
    -- is 1 so a row exists only when the operator said something.
    included INTEGER NOT NULL DEFAULT 1,
    note     TEXT    NOT NULL DEFAULT '',
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (central_name, interface_id, channel_address, parameter)
);

-- security_faults: the open-fault ledger. It is persistent because
-- `since` is the interesting part — "unreachable for three days" is a
-- different fact from "unreachable", and a restart must not reset it.
CREATE TABLE security_faults (
    id              TEXT PRIMARY KEY,
    ref             TEXT NOT NULL,
    class           TEXT NOT NULL,
    reason          TEXT NOT NULL,
    severity        TEXT NOT NULL,
    central_name    TEXT NOT NULL DEFAULT '',
    interface_id    TEXT NOT NULL DEFAULT '',
    device_address  TEXT NOT NULL DEFAULT '',
    channel_address TEXT NOT NULL DEFAULT '',
    parameter       TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL DEFAULT '',
    since_ms           INTEGER NOT NULL,
    cleared_at_ms      INTEGER NOT NULL DEFAULT 0,
    acknowledged_at_ms INTEGER NOT NULL DEFAULT 0,
    acknowledged_by    TEXT    NOT NULL DEFAULT ''
);

-- One open fault per (ref, reason): a device that keeps reporting the
-- same fault must not accumulate rows, and the partial index lets a
-- cleared fault stay in history while a new one opens for the same ref.
CREATE UNIQUE INDEX security_faults_open_unique
    ON security_faults(ref, reason) WHERE cleared_at_ms = 0;

CREATE INDEX security_faults_by_state ON security_faults(cleared_at_ms, since_ms);

-- +goose Down
DROP INDEX security_faults_by_state;
DROP INDEX security_faults_open_unique;
DROP TABLE security_faults;
DROP TABLE security_sources;
ALTER TABLE alarm_zones DROP COLUMN slug;
