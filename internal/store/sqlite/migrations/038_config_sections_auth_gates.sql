-- Repair `north.rest` section rows written before the HTTP Basic / Bearer
-- switches became load-bearing tri-state gates.
--
-- The auth config used to carry three plain bools with no omitempty:
-- basic_enabled, bearer_enabled and session_enabled. None of them was ever
-- read — Basic resolved whenever users existed, Bearer whenever tokens
-- existed, and the session middleware was installed unconditionally — but the
-- whole sub-tree is serialised into the config_sections row on the first-boot
-- seed and on every save, so every stored payload carried all three as literal
-- `false`.
--
-- basic_enabled/bearer_enabled are now *bool gates: nil means "scheme
-- enabled", an explicit false REJECTS the scheme. A stored `false` therefore
-- silently kills HTTP Basic and Bearer on the next boot — the CLI, the
-- API-token WebSocket upgrade and every REST automation get 401 while /health
-- stays green and the SPA login keeps working, because session cookies have no
-- gate. Nothing logs it. config_sections has a schema_version column, but its
-- constant was never bumped, so those rows are decoded verbatim forever.
--
-- session_enabled is the discriminator, not merely a third key to drop: it was
-- REMOVED in the same change that made the other two load-bearing, so no
-- daemon that understands the tri-state semantics has ever written it, and no
-- write path can reintroduce it (a section PUT persists a re-marshal of the
-- current struct, which has no such field). A row carrying it predates the
-- semantic change and its `false` values express no operator intent; a row
-- without it was written by a daemon where an explicit false is a deliberate
-- "scheme off" and must survive untouched.
--
-- Removing the keys rather than setting them to true restores nil, which is
-- both the documented default and what those releases actually did.
--
-- Idempotent: the guard no longer matches once the keys are gone. The nested
-- CASE (rather than one WHERE with several AND terms) keeps the JSON functions
-- from ever seeing a payload that is not valid JSON, whatever order the query
-- planner picks for a conjunction.

-- +goose Up
UPDATE config_sections
   SET value_json = CASE
         WHEN json_valid(value_json) THEN
           CASE
             WHEN json_type(value_json, '$.auth.session_enabled') IS NOT NULL
               THEN json_remove(value_json,
                                '$.auth.basic_enabled',
                                '$.auth.bearer_enabled',
                                '$.auth.session_enabled')
             ELSE value_json
           END
         ELSE value_json
       END
 WHERE section = 'north.rest'
   AND value_json LIKE '%"session_enabled"%';

-- Down cannot restore the removed keys, and must not try: their values carried
-- no meaning at the version that wrote them, so re-adding
-- "basic_enabled": false would recreate the silent 401 this migration exists
-- to repair. A no-op is the honest inverse.
-- +goose Down
SELECT 1;
