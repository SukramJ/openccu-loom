-- +goose Up
-- A controller may rename a bridged endpoint by writing its
-- BridgedDeviceBasicInformation NodeLabel. The label belongs to the
-- endpoint's identity, so it lives on the matter_endpoints row and is
-- served again when the endpoint is re-assembled after a restart. An
-- empty value means no controller renamed the endpoint.
ALTER TABLE matter_endpoints ADD COLUMN node_label TEXT NOT NULL DEFAULT '';

-- Loss on Down: every controller-written bridged NodeLabel is discarded;
-- after a down-migration the bridged endpoints answer their composed
-- default label again and the operator renames them in the controller
-- by hand. Endpoint ids, and with them every commissioning, survive.
-- +goose Down
ALTER TABLE matter_endpoints DROP COLUMN node_label;
