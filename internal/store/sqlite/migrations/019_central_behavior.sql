-- +goose Up
-- +goose StatementBegin

-- Per-central custom-DP / hub / device-lifecycle behaviour toggles
-- (CentralConfig.Behavior). Stored as a JSON blob, mirroring the
-- visibility_json column, so the schema does not grow a column per
-- toggle. Empty object means "all defaults".
ALTER TABLE centrals ADD COLUMN behavior_json TEXT NOT NULL DEFAULT '{}';

-- +goose StatementEnd

-- Down is destructive: the per-central behaviour-toggle document is deleted.
-- Every non-default custom-DP/hub/device-lifecycle override the operator
-- configured reverts to the built-in defaults.
-- +goose Down
-- +goose StatementBegin
ALTER TABLE centrals DROP COLUMN behavior_json;
-- +goose StatementEnd
