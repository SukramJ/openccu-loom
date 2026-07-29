-- Rename the alarm "area" domain to "zone": the independently armable
-- partition is a zone; "area" is freed up for the room-grouping concept
-- above CCU rooms. Data-preserving — existing rows migrate in place,
-- including the area_id keys inside alarm_codes.binding_json documents.

-- +goose Up
ALTER TABLE alarm_areas RENAME TO alarm_zones;

ALTER TABLE alarm_sensors RENAME COLUMN area_id TO zone_id;
DROP INDEX alarm_sensors_by_area;
CREATE INDEX alarm_sensors_by_zone ON alarm_sensors(zone_id);

ALTER TABLE alarm_outputs RENAME COLUMN area_id TO zone_id;
DROP INDEX alarm_outputs_by_area;
CREATE INDEX alarm_outputs_by_zone ON alarm_outputs(zone_id);

ALTER TABLE alarm_state RENAME COLUMN area_id TO zone_id;

ALTER TABLE alarm_incidents RENAME COLUMN area_id TO zone_id;
DROP INDEX alarm_incidents_by_area;
CREATE INDEX alarm_incidents_by_zone ON alarm_incidents(zone_id, closed_at_ms);

ALTER TABLE alarm_journal RENAME COLUMN area_id TO zone_id;
DROP INDEX alarm_journal_by_area_ts;
CREATE INDEX alarm_journal_by_zone_ts ON alarm_journal(zone_id, ts_ms);

ALTER TABLE alarm_codes RENAME COLUMN areas_json TO zones_json;
UPDATE alarm_codes SET binding_json = replace(binding_json, '"area_id"', '"zone_id"');

-- +goose Down
UPDATE alarm_codes SET binding_json = replace(binding_json, '"zone_id"', '"area_id"');
ALTER TABLE alarm_codes RENAME COLUMN zones_json TO areas_json;

DROP INDEX alarm_journal_by_zone_ts;
ALTER TABLE alarm_journal RENAME COLUMN zone_id TO area_id;
CREATE INDEX alarm_journal_by_area_ts ON alarm_journal(area_id, ts_ms);

DROP INDEX alarm_incidents_by_zone;
ALTER TABLE alarm_incidents RENAME COLUMN zone_id TO area_id;
CREATE INDEX alarm_incidents_by_area ON alarm_incidents(area_id, closed_at_ms);

ALTER TABLE alarm_state RENAME COLUMN zone_id TO area_id;

DROP INDEX alarm_outputs_by_zone;
ALTER TABLE alarm_outputs RENAME COLUMN zone_id TO area_id;
CREATE INDEX alarm_outputs_by_area ON alarm_outputs(area_id);

DROP INDEX alarm_sensors_by_zone;
ALTER TABLE alarm_sensors RENAME COLUMN zone_id TO area_id;
CREATE INDEX alarm_sensors_by_area ON alarm_sensors(area_id);

ALTER TABLE alarm_zones RENAME TO alarm_areas;
