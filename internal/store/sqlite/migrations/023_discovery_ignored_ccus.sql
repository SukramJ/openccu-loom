-- +goose Up
-- +goose StatementBegin

-- discovery_ignored_ccus: central units found via SSDP/UPnP discovery that the
-- operator chose to hide. Keyed by the discovery serial (stable per CCU). The
-- daemon filters these out of the "discovered CCUs" surface so an unwanted CCU
-- stops reappearing on every scan. name/host are kept only for display in an
-- "ignored" management view; ignored_at/ignored_by are audit metadata.
CREATE TABLE discovery_ignored_ccus (
    serial     TEXT PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    host       TEXT NOT NULL DEFAULT '',
    ignored_at TEXT NOT NULL,
    ignored_by TEXT NOT NULL DEFAULT ''
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE discovery_ignored_ccus;
-- +goose StatementEnd
