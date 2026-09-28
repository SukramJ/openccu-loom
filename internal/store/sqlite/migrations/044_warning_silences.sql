-- +goose Up
-- +goose StatementBegin

-- A warning silence is per-operator, not per-daemon: two users open the
-- same operator-warnings surface and may each want a different set of
-- warnings hidden. It expires on its own (until) rather than requiring an
-- explicit unsilence, so a stale silence never hides a warning forever.
CREATE TABLE warning_silences (
    username   TEXT      NOT NULL,
    warning_id TEXT      NOT NULL,
    until      TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (username, warning_id)
);

-- +goose StatementEnd

-- Loss note: dropping warning_silences discards every user's warning
-- silences; after a down-migration every previously muted warning shows
-- again and users re-silence by hand. No device or config data is lost.
-- +goose Down
-- +goose StatementBegin
DROP TABLE warning_silences;
-- +goose StatementEnd
