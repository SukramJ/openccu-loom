-- +goose Up
-- +goose StatementBegin

-- channel_flags: Loom-native, operator-set per-channel overrides (G12).
-- 'hidden' removes a channel from operation lists / MQTT discovery / Matter
-- exposure (guest views) without touching the CCU; 'locked' blocks control
-- writes (the VALUES paramset) to the channel while leaving reads intact.
-- Keyed on (central_name, channel_address) — one physical channel instance,
-- independent of the CCU. A row exists only while at least one flag is set;
-- clearing both flags deletes the row.
CREATE TABLE channel_flags (
    central_name    TEXT NOT NULL,
    channel_address TEXT NOT NULL,
    hidden          INTEGER NOT NULL DEFAULT 0,
    locked          INTEGER NOT NULL DEFAULT 0,
    updated_by      TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (central_name, channel_address)
);

-- +goose StatementEnd

-- Down is destructive: every hidden/locked channel override is deleted. A
-- channel an operator deliberately hid from guest views or locked against
-- writes reverts to fully visible and writable.
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS channel_flags;
-- +goose StatementEnd
