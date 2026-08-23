-- Repair `north.mqtt` section rows that still carry the retired
-- `payload_format` key.
--
-- The field was declared, validated (only "" | "bare" | "json" accepted) and
-- rendered by the SPA section editor, but no publisher in internal/north/mqtt
-- ever read it — state topics have always carried the JSON envelope
-- {"value":..,"available":..,"modified_at":..} unconditionally, from before
-- the field existed. It promised operators a primitive-scalar "bare" mode
-- that was retired with the ADR-0011 payload unification and never came
-- back. Removing the key rather than bumping ConfigSectionSchemaVersion
-- (see TestConfigSectionPayloadShapeIsPinned) avoids discarding every other
-- persisted north.mqtt setting for every operator to drop one field nothing
-- ever consumed.
--
-- Idempotent: the guard no longer matches once the key is gone.

-- +goose Up
UPDATE config_sections
   SET value_json = json_remove(value_json, '$.payload_format')
 WHERE section = 'north.mqtt'
   AND json_valid(value_json)
   AND json_type(value_json, '$.payload_format') IS NOT NULL;

-- Down is a no-op, not a restoration: the key never carried operator intent
-- (it was validated but read by nothing), so re-adding it on a downgrade
-- would resurrect a field this migration exists to retire, not repair a loss.
-- +goose Down
SELECT 1;
