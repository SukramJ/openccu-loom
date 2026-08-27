-- +goose Up
-- +goose StatementBegin

-- pending_devices: devices the daemon is holding back from the model
-- because `delay_new_device_creation` is enabled and no operator has
-- accepted them yet.
--
-- Only the DECISION is stored, never the descriptions. The CCU delivers a
-- full description set on every boot pull, so a second copy here would be
-- a duplicate that can go stale — and would resurrect a device that was
-- unpaired while the daemon was down. A row means "hold this address
-- back"; the payload comes from the live pull, or from nowhere when the
-- CCU no longer reports the device, which is how the row becomes
-- collectable.
--
-- Keyed on (central_name, interface_id, address). interface_id is the
-- canonical wire id, the same key the device- and description registries
-- use: an address is only unique within its interface.
CREATE TABLE pending_devices (
    central_name TEXT NOT NULL,
    interface_id TEXT NOT NULL,
    address      TEXT NOT NULL,
    model        TEXT NOT NULL DEFAULT '',
    first_seen   TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (central_name, interface_id, address)
);

-- +goose StatementEnd

-- Down is destructive in the direction that matters: every held-back
-- device loses its "waiting for approval" mark, so the next boot pull
-- materialises it as if the operator had accepted it. Nothing is lost
-- from the CCU's side — the devices exist there either way — but a
-- deliberate decision to withhold them is.
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS pending_devices;
-- +goose StatementEnd
