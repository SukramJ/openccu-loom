-- +goose Up
-- +goose StatementBegin

-- matter_group_key_map loses its foreign key to matter_group_keys. A
-- commissioner writes the GroupKeyMap entry before the key set it names
-- (matter.js GroupKeyManagementServer.ts #validateGroupKeyMap leaves the
-- key-set check out because certification tests write the map first), and
-- the constraint turned that write into a FAILURE. go-fabric's own store
-- rebuilds the table the same way (store/schema.go Upgrade); this daemon
-- carries the schema as goose migrations, so the rebuild lives here.
-- SQLite cannot drop a constraint in place: the rows move to a table of the
-- new definition, which then takes the old name. Nothing references
-- matter_group_key_map, so no foreign_keys pragma change is needed.
CREATE TABLE matter_group_key_map_new (
    fabric_index        INTEGER NOT NULL,
    group_id            INTEGER NOT NULL CHECK(group_id BETWEEN 0 AND 65535),
    group_key_set_id    INTEGER NOT NULL,
    PRIMARY KEY(fabric_index, group_id),
    FOREIGN KEY(fabric_index) REFERENCES matter_fabrics(fabric_index) ON DELETE CASCADE
);
INSERT INTO matter_group_key_map_new (fabric_index, group_id, group_key_set_id)
    SELECT fabric_index, group_id, group_key_set_id FROM matter_group_key_map;
DROP TABLE matter_group_key_map;
ALTER TABLE matter_group_key_map_new RENAME TO matter_group_key_map;

-- +goose StatementEnd

-- Loss on Down: a GroupKeyMap entry whose key set was never written cannot
-- satisfy the restored foreign key, so Down deletes those entries; the
-- commissioner writes them again with its next KeySetWrite / GroupKeyMap
-- write. Entries whose key set exists survive.
-- +goose Down
-- +goose StatementBegin
CREATE TABLE matter_group_key_map_old (
    fabric_index        INTEGER NOT NULL,
    group_id            INTEGER NOT NULL CHECK(group_id BETWEEN 0 AND 65535),
    group_key_set_id    INTEGER NOT NULL,
    PRIMARY KEY(fabric_index, group_id),
    FOREIGN KEY(fabric_index) REFERENCES matter_fabrics(fabric_index) ON DELETE CASCADE,
    FOREIGN KEY(fabric_index, group_key_set_id)
        REFERENCES matter_group_keys(fabric_index, group_key_set_id) ON DELETE CASCADE
);
INSERT INTO matter_group_key_map_old (fabric_index, group_id, group_key_set_id)
    SELECT m.fabric_index, m.group_id, m.group_key_set_id FROM matter_group_key_map m
    WHERE EXISTS (SELECT 1 FROM matter_group_keys k
                  WHERE k.fabric_index = m.fabric_index AND k.group_key_set_id = m.group_key_set_id);
DROP TABLE matter_group_key_map;
ALTER TABLE matter_group_key_map_old RENAME TO matter_group_key_map;
-- +goose StatementEnd
