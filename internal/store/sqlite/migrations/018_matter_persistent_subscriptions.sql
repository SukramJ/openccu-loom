-- +goose Up
-- +goose StatementBegin

-- matter_persistent_subscriptions stores active subscriptions that
-- survive daemon restart.  On boot the bridge re-arms every row as an
-- in-memory subscription so controllers that had active subscriptions
-- before the restart receive ongoing reports without re-subscribing.
-- This satisfies Matter 1.4 §10.6.9's requirement that publishers
-- survive restarts for ICD subscribers (KeepSubscriptions=true path).
--
-- paths_json holds a JSON array of ConcreteAttributePath objects
-- (serialised with the same field names as the Go struct so the
-- store layer can decode without a custom mapper).
-- intervals_json holds {"min":N,"max":N} for the negotiated cadence.
CREATE TABLE matter_persistent_subscriptions (
    id                  INTEGER  PRIMARY KEY AUTOINCREMENT,
    fabric_index        INTEGER  NOT NULL,
    node_id             BLOB     NOT NULL,
    paths_json          TEXT     NOT NULL,
    intervals_json      TEXT     NOT NULL,
    created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Index on fabric_index so LoadPersistentSubscriptions can efficiently
-- filter by fabric on fabric-removal teardown.
CREATE INDEX matter_persistent_subscriptions_fabric
    ON matter_persistent_subscriptions(fabric_index);

-- +goose StatementEnd

-- Down is destructive: every persisted subscription is deleted. A controller
-- with an active subscription before the restart stops receiving reports
-- until it resubscribes, defeating the Matter 1.4 §10.6.9 restart-survival
-- guarantee this table exists to satisfy.
-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS matter_persistent_subscriptions_fabric;
DROP TABLE IF EXISTS matter_persistent_subscriptions;

-- +goose StatementEnd
