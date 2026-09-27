-- +goose Up
-- +goose StatementBegin

-- A central now names the kind of system behind it. The empty string
-- means "ccu", so every existing row keeps its CCU behaviour without a
-- data migration. An openccu-lite central authenticates with an API
-- token instead of a username and password; like the password it is
-- stored either as the name of an environment variable (api_token_env)
-- or as a value sealed at rest (api_token_plain). tls_fingerprint pins
-- such a system's certificate by its SHA-256.
ALTER TABLE centrals ADD COLUMN system_type TEXT NOT NULL DEFAULT '';
ALTER TABLE centrals ADD COLUMN api_token_env TEXT NOT NULL DEFAULT '';
ALTER TABLE centrals ADD COLUMN api_token_plain TEXT NOT NULL DEFAULT '';
ALTER TABLE centrals ADD COLUMN tls_fingerprint TEXT NOT NULL DEFAULT '';

-- +goose StatementEnd

-- Down loses the system type, the API token and the pin of every row: an
-- openccu-lite central then reads as a CCU without credentials and fails
-- its bring-up until the operator re-enters it. CCU rows lose nothing,
-- their values in these columns are empty.
-- +goose Down
-- +goose StatementBegin
ALTER TABLE centrals DROP COLUMN tls_fingerprint;
ALTER TABLE centrals DROP COLUMN api_token_plain;
ALTER TABLE centrals DROP COLUMN api_token_env;
ALTER TABLE centrals DROP COLUMN system_type;
-- +goose StatementEnd
