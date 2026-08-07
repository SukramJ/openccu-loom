-- +goose Up
-- +goose StatementBegin

-- Add CASE Authenticated Tags (CATs) column to matter_resumption.
-- CATs are carried in the initiator's NOC subject and allow the
-- responder to grant fabric-scoped privilege to a set of case-session
-- peers beyond the single NodeID. Per Matter Core Spec §4.13.2
-- and matter.js packages/protocol/src/session/case/ the CATs are
-- derived from the resumption context and must survive across
-- session restarts so resumed sessions can re-apply the same ACL.
--
-- Stored as JSON (e.g. [1099511627777, 2199023255554]) for portability;
-- NULL and '[]' are both treated as "no CATs" by the load path.
ALTER TABLE matter_resumption
    ADD COLUMN case_authenticated_tags BLOB NOT NULL DEFAULT '[]';

-- +goose StatementEnd

-- Down is destructive to the CATs specifically: the table rebuild below
-- preserves every resumption secret (fabric_index, peer_node_id,
-- resumption_id, shared_secret) but discards every case_authenticated_tags
-- value already negotiated. Affected sessions lose their fabric-scoped extra
-- privilege until it is renegotiated.
-- +goose Down
-- +goose StatementBegin

-- SQLite does not support DROP COLUMN before 3.35.0. The authoritative
-- down-migration drops and re-creates the table without the column.
CREATE TABLE matter_resumption_prev (
    fabric_index    INTEGER NOT NULL,
    peer_node_id    BLOB    NOT NULL,
    resumption_id   BLOB    NOT NULL,
    shared_secret   BLOB    NOT NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(fabric_index, peer_node_id),
    FOREIGN KEY(fabric_index) REFERENCES matter_fabrics(fabric_index) ON DELETE CASCADE
);

INSERT INTO matter_resumption_prev
    (fabric_index, peer_node_id, resumption_id, shared_secret, created_at, last_used_at)
SELECT  fabric_index, peer_node_id, resumption_id, shared_secret, created_at, last_used_at
FROM    matter_resumption;

DROP INDEX IF EXISTS matter_resumption_id;
DROP TABLE matter_resumption;
ALTER TABLE matter_resumption_prev RENAME TO matter_resumption;
CREATE UNIQUE INDEX matter_resumption_id ON matter_resumption(resumption_id);

-- +goose StatementEnd
