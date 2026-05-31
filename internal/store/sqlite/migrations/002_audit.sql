-- +goose Up
-- +goose StatementBegin

CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    user TEXT,
    action TEXT NOT NULL,
    device_address TEXT,
    channel_no INTEGER,
    paramset TEXT,
    peer TEXT,
    parameter TEXT,
    note TEXT,
    changes_json TEXT
);

CREATE INDEX audit_log_by_time ON audit_log(timestamp DESC);
CREATE INDEX audit_log_by_device ON audit_log(device_address, timestamp DESC);
CREATE INDEX audit_log_by_action ON audit_log(action, timestamp DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audit_log;
-- +goose StatementEnd
