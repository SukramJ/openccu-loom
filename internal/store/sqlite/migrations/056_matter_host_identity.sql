-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 SukramJ.
-- +goose Up

-- matter_host_identity holds what the daemon itself states about its Matter
-- node and must keep across restarts, beside go-fabric's own tables (which
-- this table is not part of):
--
--   root_serial         the root BasicInformation SerialNumber, fixed on the
--                       first Matter start so it no longer follows the node
--                       label: a controller identifies the bridge by it.
--   applied_node_label  the north.matter.node_label value last applied to
--                       the node. go-fabric restores a stored label over the
--                       configured one at every start, so the daemon applies
--                       the configured label only when it differs from this
--                       one; a label a controller wrote stays in force until
--                       the operator changes the setting.
--
-- The single-row CHECK keeps a second identity from ever appearing.
CREATE TABLE matter_host_identity (
    singleton          INTEGER PRIMARY KEY CHECK (singleton = 1),
    root_serial        TEXT    NOT NULL,
    applied_node_label TEXT    NOT NULL
);

-- Down is destructive: the next start pins the serial derived from the label
-- configured then, which a paired controller may see as a changed bridge,
-- and takes the label configured then as already applied, so an edit not
-- yet applied to the node is dropped.
-- +goose Down
DROP TABLE IF EXISTS matter_host_identity;
