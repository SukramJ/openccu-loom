-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 SukramJ.
-- +goose Up
-- The SPA no longer has a favourites view, so nothing reads the pinned-items
-- list an operator stored under this key. Removing the rows keeps the table
-- to preferences a client still asks for.
DELETE FROM user_preferences WHERE key = 'favorites';

-- Down restores nothing: the deleted lists are gone, and no released SPA
-- that this schema version serves would read them.
-- +goose Down
SELECT 1;
