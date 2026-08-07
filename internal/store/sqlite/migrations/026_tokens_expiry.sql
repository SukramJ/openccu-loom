-- +goose Up
-- expires_at bounds an API token's lifetime. NULL means "never expires"
-- so every pre-existing token keeps its current unlimited validity; a
-- non-null value is enforced at authentication time.
ALTER TABLE tokens ADD COLUMN expires_at TIMESTAMP;

-- Down is destructive: the per-token expiry is deleted. A token an operator
-- deliberately scoped to a limited lifetime becomes unlimited again once the
-- column returns with its all-NULL default.
-- +goose Down
ALTER TABLE tokens DROP COLUMN expires_at;
