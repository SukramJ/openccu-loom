-- +goose Up
-- +goose StatementBegin

-- matter_resumption persists CASE-resumption identifiers per Matter
-- Core Spec §4.13.2.4. ResumptionID is the 16-byte token the bridge
-- emits in Sigma2 so a returning peer can re-establish a session via
-- Sigma1 with the resumption-id field populated, skipping the full
-- handshake.
--
-- Sessions themselves are not persisted (Matter convention is
-- volatile sessions); only the resumption pre-shared secret + the
-- node identity needed to re-anchor the resumed session live here.
CREATE TABLE matter_resumption (
    fabric_index    INTEGER NOT NULL,
    peer_node_id    BLOB    NOT NULL,
    resumption_id   BLOB    NOT NULL,
    shared_secret   BLOB    NOT NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(fabric_index, peer_node_id),
    FOREIGN KEY(fabric_index) REFERENCES matter_fabrics(fabric_index) ON DELETE CASCADE
);

-- Resumption ID is globally unique (Matter §4.13.2.4: 16-byte random
-- with negligible collision probability). Index lets the responder
-- look up by ID alone when the initiator's NodeID is unknown until
-- decode.
CREATE UNIQUE INDEX matter_resumption_id ON matter_resumption(resumption_id);

-- +goose StatementEnd

-- Down is destructive: every CASE-resumption secret is deleted. No session
-- state is lost (sessions are volatile by Matter convention), but every
-- paired controller falls back to a full CASE handshake on its next
-- connection instead of the abbreviated resume path.
-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS matter_resumption_id;
DROP TABLE IF EXISTS matter_resumption;

-- +goose StatementEnd
