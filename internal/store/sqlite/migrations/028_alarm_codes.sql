-- +goose Up
-- +goose StatementBegin

-- alarm_codes: alarm-system user codes and hardware-identity bindings
-- (docs/alarm-concept.md §11). kind distinguishes a typed PIN ("pin",
-- argon2id-hashed in hash) from two hardware-binding kinds that carry
-- no secret of their own: "keypad_slot" maps a WKP on-device user slot
-- (binding_json: central/device_address/slot/arm_mode/area_id) and
-- "remote_key" maps a remote-control key press (binding_json:
-- central/channel_address/parameter/action/area_id) onto a named
-- identity for changed-by attribution. hash is empty for the two
-- hardware kinds. duress marks a pin-kind code that disarms normally
-- but raises a silent duress event instead of the visible action.
-- perms_json/areas_json/binding_json are always loaded and saved as a
-- whole; areas_json = '[]' means every area.
CREATE TABLE alarm_codes (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL DEFAULT '',
    kind           TEXT NOT NULL,
    hash           TEXT NOT NULL DEFAULT '',
    duress         INTEGER NOT NULL DEFAULT 0,
    perms_json     TEXT NOT NULL DEFAULT '{}',
    areas_json     TEXT NOT NULL DEFAULT '[]',
    binding_json   TEXT NOT NULL DEFAULT '{}',
    valid_from_ms  INTEGER NOT NULL DEFAULT 0,
    valid_until_ms INTEGER NOT NULL DEFAULT 0,
    enabled        INTEGER NOT NULL DEFAULT 1,
    created_at_ms  INTEGER NOT NULL,
    updated_at_ms  INTEGER NOT NULL
);

CREATE INDEX alarm_codes_by_kind ON alarm_codes(kind);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE alarm_codes;
-- +goose StatementEnd
