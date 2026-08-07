-- +goose Up
-- +goose StatementBegin

-- users: SQLite-backed user store replacing the in-memory + YAML
-- bootstrap path. password_hash is bcrypt (Wave E will populate via
-- the live-edit handlers; the bootstrap path seeds entries from
-- BootstrapConfig.* env when the table is empty on first start).
CREATE TABLE users (
    subject       TEXT NOT NULL PRIMARY KEY,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at  TIMESTAMP
);

-- tokens: API tokens stored as bcrypt hashes so the plaintext is
-- never recoverable post-creation. fingerprint is the last six
-- characters of the original token, used for UI display.
CREATE TABLE tokens (
    fingerprint TEXT NOT NULL PRIMARY KEY,
    token_hash  TEXT NOT NULL,
    subject     TEXT NOT NULL,
    role        TEXT NOT NULL,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMP
);

CREATE INDEX tokens_by_subject ON tokens(subject);

-- centrals: one row per configured CCU. Replaces the YAML
-- centrals[] array. password_env stores the env-variable NAME the
-- daemon resolves at runtime; plaintext is only persisted when the
-- operator explicitly opts in to allow_plaintext_secrets via the
-- config_sections row for "security".
CREATE TABLE centrals (
    name                       TEXT NOT NULL PRIMARY KEY,
    host                       TEXT NOT NULL,
    port                       INTEGER NOT NULL DEFAULT 0,
    json_rpc_port              INTEGER NOT NULL DEFAULT 0,
    username                   TEXT NOT NULL DEFAULT '',
    password_env               TEXT NOT NULL DEFAULT '',
    password_plain             TEXT NOT NULL DEFAULT '',
    tls                        INTEGER NOT NULL DEFAULT 0,
    tls_insecure_skip_verify   INTEGER NOT NULL DEFAULT 0,
    primary_interface          TEXT NOT NULL DEFAULT '',
    interfaces_json            TEXT NOT NULL DEFAULT '[]',
    ports_json                 TEXT NOT NULL DEFAULT '{}',
    visibility_json            TEXT NOT NULL DEFAULT '{}',
    enabled                    INTEGER NOT NULL DEFAULT 1,
    created_at                 TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at                 TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- config_sections: typed JSON document store for the runtime-tier
-- config sections (north.mqtt, north.matter, north.rest.cors,
-- north.rest.auth.oidc, north.discovery, callback, ccu_data,
-- reliability, persistence, security). Each row carries the full
-- JSON snapshot of one section so writes are atomic and the
-- per-section audit trail aligns naturally with the SPA's Settings
-- tabs.
CREATE TABLE config_sections (
    section     TEXT NOT NULL PRIMARY KEY,
    value_json  TEXT NOT NULL,
    version     INTEGER NOT NULL DEFAULT 1,
    updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_by  TEXT NOT NULL DEFAULT ''
);

-- +goose StatementEnd

-- Down is destructive: every user account and API token is deleted, along
-- with every configured central and every saved config section.
-- password_hash and token_hash are bcrypt hashes with no plaintext stored
-- anywhere — by design they cannot be reconstructed, so this is not a
-- reversible operation for accounts or tokens, only a data-loss event.
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS config_sections;
DROP TABLE IF EXISTS centrals;
DROP TABLE IF EXISTS tokens;
DROP TABLE IF EXISTS users;
-- +goose StatementEnd
