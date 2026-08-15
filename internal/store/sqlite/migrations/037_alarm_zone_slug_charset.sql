-- Repair alarm-zone slugs the 034 backfill left outside the identifier
-- charset every consumer expects.
--
-- That backfill derived the slug in SQL: LOWER() plus four literal
-- replacements. SQLite's LOWER() folds ASCII only and the replacement list
-- covers space, hyphen, dot and slash, so a zone named `Küche` kept `küche`
-- and `EG (Innen)` kept `eg_(innen)`. Every Go path spells the same key with
-- routingkey.HubSlug — transliterate, then collapse each non-alphanumeric run
-- to `-` — which yields `kuche` and `eg-innen`.
--
-- The stored spelling wins wherever one exists, and it becomes the MQTT topic
-- segment, the discovery object_id and the unique_id. Home Assistant's
-- object_id grammar is [a-zA-Z0-9_-]+, so those zones produce entities that
-- never appear, while the identically named zone created after the upgrade
-- works — the German-named installations this project mostly serves are the
-- ones that hit it.
--
-- Blanking the slug is the repair: the security domain derives one with
-- HubSlug whenever the stored value is empty, which is the same value a
-- freshly created zone gets.
--
-- The predicate is deliberately narrow — only slugs carrying a character
-- outside the grammar are reset. A slug like `erdgeschoss_flur` is a working
-- identifier in a consumer's registry even though HubSlug would have written
-- `erdgeschoss-flur`; rewriting it would orphan a live entity to fix nothing,
-- which is exactly what the frozen-slug rule in 034 exists to prevent.

-- +goose Up
UPDATE alarm_zones SET slug = '' WHERE slug GLOB '*[^a-z0-9_-]*';

-- Down is a no-op: the pre-migration spelling was derived, not authored, and
-- re-deriving it in SQL would reintroduce the very value this removes.
-- +goose Down
SELECT 1;
