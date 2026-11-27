-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 SukramJ.
-- +goose Up

-- central is the CCU a change was made on. Device addresses repeat across
-- CCUs (virtual remotes, internal devices, CUxD devices), so a row's device
-- address alone cannot say which CCU's device it is about. NULL for a
-- daemon-wide change and for every row written before this column existed;
-- the read path derives a central for those from the address, as it always
-- did.
ALTER TABLE audit_log ADD COLUMN central TEXT;

-- Down drops the column and with it every recorded central; the rows stay,
-- and the read path falls back to deriving the central from the address.
-- +goose Down
ALTER TABLE audit_log DROP COLUMN central;
