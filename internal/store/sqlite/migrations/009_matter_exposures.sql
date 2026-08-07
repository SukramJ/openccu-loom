-- +goose Up
-- +goose StatementBegin

-- matter_exposures persists the operator-managed allowlist that the
-- endpoint assembler consults at materialisation time. A bridged DP is
-- exposed to commissioners only when:
--
--   * a row exists with `(central_name, device_address, channel_no,
--     dp_kind, dp_key)` matching the source, AND
--   * `enabled = 1`, AND
--   * the cluster mapper (internal/north/matter/eligibility) classifies
--     the source as `mappable` or `partially_mappable`.
--
-- Default state is empty: no rows = nothing exposed. This implements
-- the §1 "Allowlist instead of Denylist" rule from
-- `docs/matter-ui-concept.md`.
--
-- The 5-tuple primary key matches the matter_endpoints table from
-- migration 007 so endpoint_id assignment can JOIN cleanly when the
-- allowlist enables a previously-known source.
CREATE TABLE matter_exposures (
    central_name    TEXT    NOT NULL,
    device_address  TEXT    NOT NULL,
    channel_no      INTEGER NOT NULL,
    dp_kind         TEXT    NOT NULL CHECK(dp_kind IN ('custom','generic','calculated','combined','measurement')),
    dp_key          TEXT    NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
    friendly_name   TEXT    NOT NULL DEFAULT '',
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    actor           TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY(central_name, device_address, channel_no, dp_kind, dp_key)
);

-- Fast-path lookup for the assembler's "is this central's source
-- enabled?" probe — avoids a full-table scan when one CCU has a few
-- hundred channels.
CREATE INDEX matter_exposures_central
    ON matter_exposures(central_name, enabled);

-- +goose StatementEnd

-- Down is destructive: the operator-curated Matter exposure allowlist is
-- deleted outright, including every enable/disable decision and every
-- friendly name. Because the default is "nothing exposed", the bridge does
-- not fail open — it silently goes back to exposing nothing until the
-- operator recreates every entry.
-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS matter_exposures_central;
DROP TABLE IF EXISTS matter_exposures;

-- +goose StatementEnd
