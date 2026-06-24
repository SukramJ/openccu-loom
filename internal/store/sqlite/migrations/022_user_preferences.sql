-- +goose Up
-- +goose StatementBegin

-- user_preferences: per-user UI state the SPA persists server-side so it
-- survives across browsers and devices. Each row is one opaque JSON blob
-- addressed by (subject, key) — e.g. key='favorites' holds the user's
-- pinned devices/channels/sysvars. The daemon never interprets the value;
-- the SPA owns the schema. updated_unix is epoch seconds for the
-- last write.
CREATE TABLE user_preferences (
    subject      TEXT NOT NULL,
    key          TEXT NOT NULL,
    value_json   TEXT NOT NULL,
    updated_unix INTEGER NOT NULL,
    PRIMARY KEY (subject, key)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE user_preferences;
-- +goose StatementEnd
