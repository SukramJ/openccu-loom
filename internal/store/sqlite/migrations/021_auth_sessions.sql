-- +goose Up
-- +goose StatementBegin

-- auth_sessions: SQLite-backed durable store for browser auth sessions.
-- The in-memory auth.SessionStore is a save-through cache over this table
-- so a daemon restart no longer logs every active browser out — the store
-- hydrates from here on boot and best-effort persists each Issue/Revoke.
-- Times are stored as Unix seconds (epoch). The expires_unix index backs
-- the periodic purge sweep and the boot-time active-session load.
CREATE TABLE auth_sessions (
    id           TEXT NOT NULL PRIMARY KEY,
    subject      TEXT NOT NULL,
    scheme       TEXT NOT NULL,
    role         TEXT NOT NULL,
    token_id     TEXT NOT NULL DEFAULT '',
    created_unix INTEGER NOT NULL,
    expires_unix INTEGER NOT NULL
);

CREATE INDEX idx_auth_sessions_expires ON auth_sessions(expires_unix);

-- +goose StatementEnd

-- Down is destructive: every stored session is deleted. Every operator
-- currently logged in is signed out and must authenticate again on the
-- daemon's next start.
-- +goose Down
-- +goose StatementBegin
DROP TABLE auth_sessions;
-- +goose StatementEnd
