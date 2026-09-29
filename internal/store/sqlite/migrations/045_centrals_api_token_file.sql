-- +goose Up
-- An openccu-lite central may read its API token from a file per request
-- (occulited mints an add-on's token anew at every start; a container may
-- remount a secret), instead of storing the token itself.
ALTER TABLE centrals ADD COLUMN api_token_file TEXT NOT NULL DEFAULT '';

-- Loss on Down: a central reading its token from a file loses that
-- pointer and falls back to the stored token, if any — an auto-onboarded
-- add-on central then has no credential until re-onboarded or given a
-- token by hand. Rows without the field lose nothing.
-- +goose Down
ALTER TABLE centrals DROP COLUMN api_token_file;
