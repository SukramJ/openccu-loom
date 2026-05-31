-- +goose Up
-- +goose StatementBegin

-- session_recorder persists in-memory SessionRecorder entries to disk so
-- that production-replay diagnosis can survive daemon restarts.
-- Mirrors the sessions_rec table shape from SPECIFICATION.md Appendix B
-- with an added central_name column for multi-CCU scoping (ADR 0002).
CREATE TABLE session_recorder (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    central_name    TEXT    NOT NULL,
    slug            TEXT    NOT NULL,
    rpc_type        TEXT    NOT NULL,
    method          TEXT    NOT NULL,
    frozen_params   TEXT    NOT NULL,
    response_json   TEXT    NOT NULL,
    recorded_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ttl_seconds     INTEGER NOT NULL DEFAULT 0
);

-- Composite index on the lookup dimensions: central, slug, and most-recent
-- first. Mirrors the aiohomematic SUB_DIRECTORY_SESSION/<slug>/ layout.
CREATE INDEX session_recorder_lookup
    ON session_recorder(central_name, slug, recorded_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS session_recorder_lookup;
DROP TABLE IF EXISTS session_recorder;

-- +goose StatementEnd
