-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 SukramJ.
-- +goose Up

-- on_behalf_of is who the authenticated credential said it acted for: a
-- relay that forwards a Home Assistant user's edit names that user in the
-- X-On-Behalf-Of header. It is an assertion the daemon cannot check, kept
-- beside the principal (user) and never read for authorization. NULL for
-- every row that carried no such header, which is every row written before
-- this column existed.
ALTER TABLE audit_log ADD COLUMN on_behalf_of TEXT;

-- Down drops the column and with it every recorded assertion; the principal
-- of each row stays.
-- +goose Down
ALTER TABLE audit_log DROP COLUMN on_behalf_of;
