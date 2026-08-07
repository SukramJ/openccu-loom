-- +goose Up
-- matter_settings is a key-value store for per-daemon Matter strings
-- that must survive restarts (writable cluster attributes such as
-- BasicInformation.NodeLabel / Location per Matter §11.1.6.6 "N"
-- quality). Kept separate from matter_metadata, whose value column is
-- INTEGER for counters.
CREATE TABLE matter_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Down is destructive: the persisted BasicInformation.NodeLabel/Location
-- strings are deleted. Any naming or location edit a commissioner wrote back
-- reverts to the firmware default on the next boot.
-- +goose Down
DROP TABLE IF EXISTS matter_settings;
