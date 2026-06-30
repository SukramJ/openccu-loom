-- +goose Up
-- +goose StatementBegin

-- Add the discovery serial to centrals. When a CCU is adopted from the SSDP/UPnP
-- discovery surface its stable hardware serial is persisted here, so the
-- "already configured" check can match a discovered CCU by serial instead of by
-- host — the host (a DHCP / docker IP) can change, the serial cannot. Rows
-- created before this migration (or via YAML / manual entry) carry an empty
-- serial and fall back to host matching.
ALTER TABLE centrals ADD COLUMN serial TEXT NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE centrals DROP COLUMN serial;
-- +goose StatementEnd
