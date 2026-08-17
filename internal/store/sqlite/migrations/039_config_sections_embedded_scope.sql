-- Repair `north.ui` section rows written before EmbeddedScope existed.
--
-- Through 0.55.x, `embedded: true` selected the reduced Config UI
-- unconditionally, everywhere the daemon served it — including on the
-- daemon's own directly-exposed port. 0.56.0 (commit f8fc7be8, 2026-08-09)
-- added EmbeddedScope to let that declaration be scoped to requests that
-- arrive through Home Assistant, and made the empty/unset value resolve to
-- the NEW, narrower `inside_ha` default rather than the old daemon-wide
-- behaviour (config.NorthUI.EmbeddedScopeOrDefault). A row saved before
-- EmbeddedScope existed carries no `embedded_scope` key — indistinguishable
-- on the wire from a post-0.56.0 row whose operator deliberately kept the
-- new default — so an HA add-on operator who set `embedded: true` on 0.55.x
-- and never revisited the UI settings again got a silently different,
-- narrower UI on every upgrade past 0.56.0, with no notice beyond a
-- CHANGELOG entry.
--
-- There is no removed sibling field to key on the way
-- 038_config_sections_auth_gates.sql keys on session_enabled: EmbeddedScope
-- was a pure addition. The row's own updated_at is the next best evidence —
-- Put() bumps it on every save, including the first-boot seed — so a row
-- whose updated_at predates the release that introduced EmbeddedScope was
-- written by a daemon that could not have known the field existed, while a
-- row updated since had every opportunity to state a scope explicitly. This
-- undercounts a rarer case (a post-0.56.0 save that touched some other
-- NorthUI field, e.g. through /ui/surfaces, without ever revisiting
-- Embedded/EmbeddedScope) but correctly repairs the scenario this migration
-- exists for: "no config change of their own" since before the field shipped.
--
-- Idempotent: the guard no longer matches once embedded_scope is present.
-- The nested CASE keeps the JSON functions from ever seeing a payload that
-- is not valid JSON, whatever order the query planner picks for a
-- conjunction.

-- +goose Up
UPDATE config_sections
   SET value_json = CASE
         WHEN json_valid(value_json) THEN
           CASE
             WHEN json_extract(value_json, '$.embedded') = 1
               AND json_type(value_json, '$.embedded_scope') IS NULL
               THEN json_set(value_json, '$.embedded_scope', 'always')
             ELSE value_json
           END
         ELSE value_json
       END
 WHERE section = 'north.ui'
   AND value_json LIKE '%"embedded":true%'
   AND updated_at < '2026-08-10T00:00:00Z';

-- Down cannot tell an embedded_scope this migration wrote from one the
-- operator has since chosen deliberately, so removing the key again could
-- discard a real choice. A no-op is the honest inverse, matching
-- 038_config_sections_auth_gates.sql.
-- +goose Down
SELECT 1;
