-- +goose Up
-- +goose StatementBegin

-- matter_diagnostics persists the GeneralDiagnostics counters that
-- need to survive daemon restarts: RebootCount + accumulated
-- TotalOperationalHours from prior process lifetimes. The cluster
-- (`internal/north/matter/cluster/core/general_diagnostics.go`) seeds
-- these via SetPersistedCounters during boot, then the daemon updates
-- the row on shutdown so the next boot sees a fresh RebootCount and
-- the running TotalOperationalHours total.
--
-- Single-row table (id=1 invariant): there is exactly one bridge per
-- daemon, the diagnostics counters are global, no fabric- or
-- endpoint-scoping is needed.

CREATE TABLE matter_diagnostics (
    id                       INTEGER PRIMARY KEY CHECK (id = 1),
    reboot_count             INTEGER NOT NULL DEFAULT 0,
    base_operational_hours   INTEGER NOT NULL DEFAULT 0,
    updated_at               INTEGER NOT NULL
);

-- Seed the singleton row so subsequent UPSERTs hit an existing record.
INSERT INTO matter_diagnostics (id, reboot_count, base_operational_hours, updated_at)
    VALUES (1, 0, 0, CAST(strftime('%s','now') AS INTEGER));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS matter_diagnostics;
-- +goose StatementEnd
