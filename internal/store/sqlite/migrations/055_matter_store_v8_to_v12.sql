-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 SukramJ.
-- +goose Up
-- go-fabric's Matter store writes into this daemon's database, so every
-- table and column its store.Schema() creates exists here too. Copied
-- verbatim from go-fabric v0.25.0 store/migrations/v8..v12.up.sql:
--   v8  the fabric row's commit state (pending), the VID verification data,
--       the prior values of an uncommitted UpdateNOC, the subscriptions'
--       save order;
--   v9  the session parameters of a resumption record, and the fabric
--       events owed to a controller (matter_owed_fabric_events);
--   v10 the seal mark of every table that holds a secret, and the DoorLock
--       users' and credentials' record source;
--   v11 the GroupKeyMap entry's position and the resumption write order;
--   v12 the role that wrote a resumption record (initiator), so the
--       per-fabric cap of 16 counts the CASE responder's records only.
-- Every added column has a default, so existing rows read as before. The
-- data step of v8 (the VID settings moving into their columns) is not
-- DDL; go-fabric's store.Upgrade does it, which the Matter start runs after
-- these migrations.
-- go-fabric store/migrations/v8.up.sql
ALTER TABLE matter_fabrics ADD COLUMN pending INTEGER NOT NULL DEFAULT 0 CHECK(pending BETWEEN 0 AND 2);
ALTER TABLE matter_fabrics ADD COLUMN vid_statement BLOB;
ALTER TABLE matter_fabrics ADD COLUMN vvsc BLOB;
ALTER TABLE matter_fabrics ADD COLUMN prior_node_id BLOB;
ALTER TABLE matter_fabrics ADD COLUMN prior_vendor_id INTEGER CHECK(prior_vendor_id BETWEEN 0 AND 65535);
ALTER TABLE matter_fabrics ADD COLUMN prior_vid_statement BLOB;
ALTER TABLE matter_fabrics ADD COLUMN prior_vvsc BLOB;
ALTER TABLE matter_node_identities ADD COLUMN prior_noc BLOB;
ALTER TABLE matter_node_identities ADD COLUMN prior_icac BLOB;
ALTER TABLE matter_node_identities ADD COLUMN prior_private_key BLOB;
ALTER TABLE matter_server_subscriptions ADD COLUMN save_seq INTEGER NOT NULL DEFAULT 0;

-- go-fabric store/migrations/v9.up.sql
ALTER TABLE matter_resumption ADD COLUMN session_idle_interval INTEGER NOT NULL DEFAULT 0 CHECK(session_idle_interval BETWEEN 0 AND 4294967295);
ALTER TABLE matter_resumption ADD COLUMN session_active_interval INTEGER NOT NULL DEFAULT 0 CHECK(session_active_interval BETWEEN 0 AND 4294967295);
ALTER TABLE matter_resumption ADD COLUMN session_active_threshold INTEGER NOT NULL DEFAULT 0 CHECK(session_active_threshold BETWEEN 0 AND 65535);
ALTER TABLE matter_resumption ADD COLUMN data_model_revision INTEGER NOT NULL DEFAULT 0 CHECK(data_model_revision BETWEEN 0 AND 65535);
ALTER TABLE matter_resumption ADD COLUMN interaction_model_revision INTEGER NOT NULL DEFAULT 0 CHECK(interaction_model_revision BETWEEN 0 AND 65535);
ALTER TABLE matter_resumption ADD COLUMN specification_version INTEGER NOT NULL DEFAULT 0 CHECK(specification_version BETWEEN 0 AND 4294967295);
ALTER TABLE matter_resumption ADD COLUMN max_paths_per_invoke INTEGER NOT NULL DEFAULT 0 CHECK(max_paths_per_invoke BETWEEN 0 AND 65535);
ALTER TABLE matter_resumption ADD COLUMN supported_transports INTEGER NOT NULL DEFAULT 0 CHECK(supported_transports BETWEEN 0 AND 65535);
ALTER TABLE matter_resumption ADD COLUMN max_tcp_message_size INTEGER NOT NULL DEFAULT 0 CHECK(max_tcp_message_size BETWEEN 0 AND 4294967295);

CREATE TABLE IF NOT EXISTS matter_owed_fabric_events (
    id                  INTEGER  PRIMARY KEY AUTOINCREMENT,
    kind                INTEGER  NOT NULL CHECK(kind BETWEEN 1 AND 2),
    fabric_index        INTEGER  NOT NULL CHECK(fabric_index BETWEEN 1 AND 254),
    fabric_id           BLOB     NOT NULL,
    node_id             BLOB     NOT NULL,
    root_public_key     BLOB     NOT NULL,
    compressed_id       BLOB     NOT NULL,
    vendor_id           INTEGER  NOT NULL CHECK(vendor_id BETWEEN 0 AND 65535),
    label               TEXT     NOT NULL DEFAULT ''
);

-- go-fabric store/migrations/v10.up.sql
ALTER TABLE matter_node_identities ADD COLUMN sealed INTEGER NOT NULL DEFAULT 0 CHECK(sealed IN (0, 1));
ALTER TABLE matter_group_keys ADD COLUMN sealed INTEGER NOT NULL DEFAULT 0 CHECK(sealed IN (0, 1));
ALTER TABLE matter_resumption ADD COLUMN sealed INTEGER NOT NULL DEFAULT 0 CHECK(sealed IN (0, 1));
ALTER TABLE matter_settings ADD COLUMN sealed INTEGER NOT NULL DEFAULT 0 CHECK(sealed IN (0, 1));
ALTER TABLE matter_doorlock_users ADD COLUMN created_through_matter INTEGER NOT NULL DEFAULT 1 CHECK(created_through_matter IN (0, 1));
ALTER TABLE matter_doorlock_users ADD COLUMN modified_through_matter INTEGER NOT NULL DEFAULT 1 CHECK(modified_through_matter IN (0, 1));
ALTER TABLE matter_doorlock_credentials ADD COLUMN sealed INTEGER NOT NULL DEFAULT 0 CHECK(sealed IN (0, 1));
ALTER TABLE matter_doorlock_credentials ADD COLUMN created_through_matter INTEGER NOT NULL DEFAULT 1 CHECK(created_through_matter IN (0, 1));
ALTER TABLE matter_doorlock_credentials ADD COLUMN modified_through_matter INTEGER NOT NULL DEFAULT 1 CHECK(modified_through_matter IN (0, 1));
ALTER TABLE matter_icd_registrations ADD COLUMN sealed INTEGER NOT NULL DEFAULT 0 CHECK(sealed IN (0, 1));

-- go-fabric store/migrations/v11.up.sql
ALTER TABLE matter_group_key_map ADD COLUMN position INTEGER NOT NULL DEFAULT 0 CHECK(position >= 0);
ALTER TABLE matter_resumption ADD COLUMN write_seq INTEGER NOT NULL DEFAULT 0 CHECK(write_seq >= 0);

-- go-fabric store/migrations/v12.up.sql
ALTER TABLE matter_resumption ADD COLUMN initiator INTEGER NOT NULL DEFAULT 0 CHECK(initiator IN (0, 1));

-- Loss on Down: go-fabric's own down steps, newest first. v10's refuses
-- while any row is sealed; v8's commits nothing an uncommitted AddNOC or
-- UpdateNOC left (the pending fabric is dropped, the prior NOC restored);
-- the owed fabric events, the session parameters, the key-map positions,
-- the write order and the record roles are discarded.
-- +goose Down
-- go-fabric store/migrations/v12.down.sql
ALTER TABLE matter_resumption DROP COLUMN initiator;

-- go-fabric store/migrations/v11.down.sql
ALTER TABLE matter_resumption DROP COLUMN write_seq;
ALTER TABLE matter_group_key_map DROP COLUMN position;

-- go-fabric store/migrations/v10.down.sql
DROP TABLE IF EXISTS temp.matter_v10_down_guard;
CREATE TEMP TABLE matter_v10_down_guard (
    sealed_rows INTEGER CONSTRAINT v10_down_refuses_sealed_rows CHECK(sealed_rows = 0)
);
INSERT INTO matter_v10_down_guard (sealed_rows) SELECT
    (SELECT COUNT(*) FROM matter_node_identities WHERE sealed <> 0) +
    (SELECT COUNT(*) FROM matter_group_keys WHERE sealed <> 0) +
    (SELECT COUNT(*) FROM matter_resumption WHERE sealed <> 0) +
    (SELECT COUNT(*) FROM matter_settings WHERE sealed <> 0) +
    (SELECT COUNT(*) FROM matter_doorlock_credentials WHERE sealed <> 0) +
    (SELECT COUNT(*) FROM matter_icd_registrations WHERE sealed <> 0);
DROP TABLE matter_v10_down_guard;

ALTER TABLE matter_icd_registrations DROP COLUMN sealed;
ALTER TABLE matter_doorlock_credentials DROP COLUMN modified_through_matter;
ALTER TABLE matter_doorlock_credentials DROP COLUMN created_through_matter;
ALTER TABLE matter_doorlock_credentials DROP COLUMN sealed;
ALTER TABLE matter_doorlock_users DROP COLUMN modified_through_matter;
ALTER TABLE matter_doorlock_users DROP COLUMN created_through_matter;
ALTER TABLE matter_settings DROP COLUMN sealed;
ALTER TABLE matter_resumption DROP COLUMN sealed;
ALTER TABLE matter_group_keys DROP COLUMN sealed;
ALTER TABLE matter_node_identities DROP COLUMN sealed;

-- go-fabric store/migrations/v9.down.sql
DROP TABLE IF EXISTS matter_owed_fabric_events;
ALTER TABLE matter_resumption DROP COLUMN max_tcp_message_size;
ALTER TABLE matter_resumption DROP COLUMN supported_transports;
ALTER TABLE matter_resumption DROP COLUMN max_paths_per_invoke;
ALTER TABLE matter_resumption DROP COLUMN specification_version;
ALTER TABLE matter_resumption DROP COLUMN interaction_model_revision;
ALTER TABLE matter_resumption DROP COLUMN data_model_revision;
ALTER TABLE matter_resumption DROP COLUMN session_active_threshold;
ALTER TABLE matter_resumption DROP COLUMN session_active_interval;
ALTER TABLE matter_resumption DROP COLUMN session_idle_interval;

-- go-fabric store/migrations/v8.down.sql
UPDATE matter_node_identities
SET noc = prior_noc, icac = prior_icac, private_key = prior_private_key
WHERE fabric_index IN (SELECT fabric_index FROM matter_fabrics WHERE pending = 2);
UPDATE matter_fabrics
SET node_id = prior_node_id, vendor_id = prior_vendor_id,
    vid_statement = prior_vid_statement, vvsc = prior_vvsc
WHERE pending = 2;
DELETE FROM matter_fabrics WHERE pending = 1;

INSERT OR REPLACE INTO matter_settings (key, value)
SELECT 'matter.fabric.' || fabric_index || '.vid.statement', lower(hex(vid_statement))
FROM matter_fabrics WHERE vid_statement IS NOT NULL;
INSERT OR REPLACE INTO matter_settings (key, value)
SELECT 'matter.fabric.' || fabric_index || '.vid.vvsc', lower(hex(vvsc))
FROM matter_fabrics WHERE vvsc IS NOT NULL;

ALTER TABLE matter_server_subscriptions DROP COLUMN save_seq;
ALTER TABLE matter_node_identities DROP COLUMN prior_private_key;
ALTER TABLE matter_node_identities DROP COLUMN prior_icac;
ALTER TABLE matter_node_identities DROP COLUMN prior_noc;
ALTER TABLE matter_fabrics DROP COLUMN prior_vvsc;
ALTER TABLE matter_fabrics DROP COLUMN prior_vid_statement;
ALTER TABLE matter_fabrics DROP COLUMN prior_vendor_id;
ALTER TABLE matter_fabrics DROP COLUMN prior_node_id;
ALTER TABLE matter_fabrics DROP COLUMN vvsc;
ALTER TABLE matter_fabrics DROP COLUMN vid_statement;
ALTER TABLE matter_fabrics DROP COLUMN pending;
