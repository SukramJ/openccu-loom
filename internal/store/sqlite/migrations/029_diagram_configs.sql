-- +goose Up
-- +goose StatementBegin

-- diagram_configs: named multi-series measurement-history diagram
-- definitions (SV03). These are Loom-native config-like metadata — a
-- diagram references data points (central, interface, channel, parameter)
-- that the history recorder samples; the definition itself must be
-- listable/editable even when the opt-in history feature is off, so it
-- lives in the MAIN app DB, not the history DB. The series list and
-- default range live in config_json (SPA-owned, opaque to the daemon
-- except for a series[].central non-empty guard). visibility is
-- 'private' (owner only) or 'shared' (any authenticated user may read).
CREATE TABLE diagram_configs (
    id            TEXT PRIMARY KEY,
    owner_subject TEXT NOT NULL,
    name          TEXT NOT NULL,
    visibility    TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'shared')),
    config_json   TEXT NOT NULL DEFAULT '{}',
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

CREATE INDEX diagram_configs_by_owner ON diagram_configs (owner_subject);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS diagram_configs;
-- +goose StatementEnd
