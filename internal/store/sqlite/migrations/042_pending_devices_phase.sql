-- +goose Up
-- +goose StatementBegin

-- Onboarding is two holds on the same device, not two features, so one
-- row carries it through both:
--
--   'pending'    — not accepted yet. Held out of the model entirely: no
--                  ise_id, no channels, nothing to configure. This is
--                  what `delay_new_device_creation` gates.
--   'unreleased' — accepted on the CCU and materialised here, so it has
--                  its ise_id and channels and CAN be configured, but it
--                  is withheld from the ecosystems (MQTT / Home
--                  Assistant, Matter, outbound webhooks) until the
--                  operator finishes the wizard.
--
-- The row is deleted on release. An absent row therefore means "fully
-- onboarded", which is what every device on an existing installation is:
-- the table is empty after this migration, so nothing already visible in
-- Home Assistant or on a Matter controller disappears on upgrade. Only a
-- device that enters through the wizard is ever held.
ALTER TABLE pending_devices ADD COLUMN phase TEXT NOT NULL DEFAULT 'pending';

-- +goose StatementEnd

-- Down drops the distinction: every held device falls back to 'pending',
-- so a device that was already accepted and configured but not yet
-- released is held out of the model again on the next boot. It reappears
-- on the inbox surface rather than being lost.
-- +goose Down
-- +goose StatementBegin
ALTER TABLE pending_devices DROP COLUMN phase;
-- +goose StatementEnd
