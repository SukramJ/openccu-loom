-- +goose Up
-- +goose StatementBegin

-- matter_fabrics records the operational fabrics the bridge participates
-- in. Mirrors the Fabric Descriptor from Matter Core Spec §11.18.5.
-- fabric_index is the stack-assigned 1..254 identifier used as foreign
-- key by every per-fabric child table.
CREATE TABLE matter_fabrics (
    fabric_index    INTEGER PRIMARY KEY CHECK(fabric_index BETWEEN 1 AND 254),
    fabric_id       BLOB    NOT NULL,
    node_id         BLOB    NOT NULL,
    root_public_key BLOB    NOT NULL,
    vendor_id       INTEGER NOT NULL CHECK(vendor_id BETWEEN 0 AND 65535),
    label           TEXT    NOT NULL DEFAULT '',
    compressed_id   BLOB    NOT NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- (fabric_id, root_public_key) is unique per Matter §11.18.5: the same
-- root MAY NOT add the same fabric twice.
CREATE UNIQUE INDEX matter_fabrics_id_root
    ON matter_fabrics(fabric_id, root_public_key);

-- matter_node_identities holds the per-fabric node operational
-- credentials: the NOC + optional ICAC + the private key matching the
-- NOC's public key + the Identity Protection Key. One identity per
-- fabric (the bridge has exactly one node per fabric).
CREATE TABLE matter_node_identities (
    fabric_index    INTEGER PRIMARY KEY,
    noc             BLOB    NOT NULL,
    icac            BLOB,
    private_key     BLOB    NOT NULL,
    ipk             BLOB    NOT NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(fabric_index) REFERENCES matter_fabrics(fabric_index) ON DELETE CASCADE
);

-- matter_group_keys persists the EpochKey triples per
-- (fabric, group_key_set_id) per Matter §11.2.10. epoch_key_1 and
-- epoch_key_2 are optional (nullable) — the active key may rotate
-- without filling all three slots.
CREATE TABLE matter_group_keys (
    fabric_index        INTEGER NOT NULL,
    group_key_set_id    INTEGER NOT NULL CHECK(group_key_set_id BETWEEN 0 AND 65535),
    security_policy     INTEGER NOT NULL CHECK(security_policy BETWEEN 0 AND 1),
    epoch_key_0         BLOB    NOT NULL,
    epoch_start_0       INTEGER NOT NULL,
    epoch_key_1         BLOB,
    epoch_start_1       INTEGER,
    epoch_key_2         BLOB,
    epoch_start_2       INTEGER,
    PRIMARY KEY(fabric_index, group_key_set_id),
    FOREIGN KEY(fabric_index) REFERENCES matter_fabrics(fabric_index) ON DELETE CASCADE
);

-- matter_group_key_map binds GroupID → GroupKeySetID per fabric
-- (Matter §11.2.10.4 GroupKeyMap attribute).
CREATE TABLE matter_group_key_map (
    fabric_index        INTEGER NOT NULL,
    group_id            INTEGER NOT NULL CHECK(group_id BETWEEN 0 AND 65535),
    group_key_set_id    INTEGER NOT NULL,
    PRIMARY KEY(fabric_index, group_id),
    FOREIGN KEY(fabric_index) REFERENCES matter_fabrics(fabric_index) ON DELETE CASCADE,
    FOREIGN KEY(fabric_index, group_key_set_id)
        REFERENCES matter_group_keys(fabric_index, group_key_set_id) ON DELETE CASCADE
);

-- matter_acl_entries persists the per-fabric AccessControl list
-- (Matter §11.2.12). Subjects + Targets are JSON-encoded inline because
-- they are short list-of-records and the access path is always
-- "load whole ACL for fabric" — relational normalisation buys nothing
-- here. position orders entries; the Matter spec evaluates ACEs in
-- list order.
CREATE TABLE matter_acl_entries (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    fabric_index    INTEGER NOT NULL,
    privilege       INTEGER NOT NULL CHECK(privilege BETWEEN 1 AND 5),
    auth_mode       INTEGER NOT NULL CHECK(auth_mode BETWEEN 1 AND 3),
    subjects_json   TEXT    NOT NULL,
    targets_json    TEXT,
    position        INTEGER NOT NULL,
    FOREIGN KEY(fabric_index) REFERENCES matter_fabrics(fabric_index) ON DELETE CASCADE
);

CREATE UNIQUE INDEX matter_acl_position
    ON matter_acl_entries(fabric_index, position);

-- +goose StatementEnd

-- Down is destructive: every paired Matter fabric is deleted, including the
-- NOC, the operational private key, and the Identity Protection Key in
-- matter_node_identities. None of those three are recoverable — the only
-- way back is re-commissioning every fabric from scratch.
-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS matter_acl_position;
DROP TABLE IF EXISTS matter_acl_entries;
DROP TABLE IF EXISTS matter_group_key_map;
DROP TABLE IF EXISTS matter_group_keys;
DROP TABLE IF EXISTS matter_node_identities;
DROP INDEX IF EXISTS matter_fabrics_id_root;
DROP TABLE IF EXISTS matter_fabrics;

-- +goose StatementEnd
