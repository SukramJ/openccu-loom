# ADR 0089 — The daemon's warnings appear on the openccu-lite box as add-on notices

- Status: accepted
- Date: 2026-11-11
- Related: [ADR 0077 — the add-on token onboards the local box](./0077-lite-addon-token-onboarding.md),
  [ADR 0079 — box-shell single sign-on](./0079-lite-shell-session-sso.md),
  [ADR 0088 — the Config UI is the bridge's console](./0088-config-ui-is-the-bridges-console.md)

## Context

On an openccu-lite box the daemon runs as an add-on. The box's own Status
page is where its operator looks first: it lists the system's warnings,
drives the status LED from them, and lets an administrator silence each
one. The daemon keeps a warning list of its own (`GET /api/v1/warnings`:
unhealthy components, error-grade incidents, service-message backlogs,
pending pairing requests), which is visible only inside the daemon's
Config UI. A broker that went away, or a pairing request that waits for a
decision, is therefore invisible to someone looking at the box.

The box offers add-ons a way to say such things themselves. An add-on
posts **notices** to `/api/system/v1/addon-notices` with its control token
(`/run/occulite/addon-tokens/<id>`, minted at every start of the box's
service); each notice has an id, a severity, a title and an optional text
in German and English, and stands beside the system's warnings until the
add-on clears it. An add-on holds at most twenty.

The daemon's warning list is computed when it is read. Nothing tells a
consumer that it changed.

## Decision

1. **A mirror keeps the box's notices equal to the daemon's warnings.**
   One component owns the box's notice set for this add-on: it computes
   the notices the active warnings call for, reads what the box holds, and
   writes the difference — a `PUT` for a new or changed notice, a `DELETE`
   for one whose warning cleared. It never writes a notice that is already
   as it should be, so a silence set on the box stands (the box keeps a
   silence across a replacing `PUT`, and a cleared notice's silence is
   spent, which matches the daemon's own rule that a silence ends when its
   condition clears).

2. **Event-driven, with a slow resynchronisation.** The mirror reconciles
   when a warning source changes, not on a clock:
   - a health component changes status (a new change hook on the health
     tracker);
   - an incident is recorded or the service-message list of a central
     changes (the centrals' event buses, subscribed through the registry's
     `OnRegister` observer so later centrals are covered);
   - a pairing request arrives or is decided (the pairing manager's
     change callback).

   Changes are coalesced: one reconciliation runs at a time, and any
   number of nudges that arrive meanwhile ask for exactly one more. A
   resynchronisation every five minutes covers what no event announces:
   an incident leaving the 24-hour window, a component whose last sample
   went stale, and a box whose service restarted (a new control token) or
   whose notices were removed.

3. **Which warnings.** Health, incident and pairing warnings are mirrored.
   A service-message warning is mirrored for every central **except the
   box the daemon runs on**: the box lists its own service messages on the
   same page, and the daemon's count would say the same thing twice.

4. **The words are the daemon's.** The notice title is the warning's
   sentence in German and English, rendered by the daemon from its own
   catalogue (`internal/i18n`), not by the Config UI. The link is the
   add-on's page on the box.

5. **Ids.** A notice id is derived from the warning id: lower-cased, every
   character outside `a-z0-9._-` replaced by `.`. An id longer than the
   box's 64 characters, and one that would collide with another, ends in
   a short hash of the warning id.

6. **Twenty at most.** Errors come before warnings, then by id; what does
   not fit is summarised in one notice that names the count and links to
   the daemon's own list.

7. **A switch, on in the add-on.** `north.ui.box_notices`, tri-state like
   the box-shell single sign-on: unset means on beside an openccu-lite
   box and off everywhere else; an explicit value overrides. Outside the
   add-on there is no box to tell, and the mirror is inert. The switch
   sits in the UI block because it decides where the daemon's operator
   sees its warnings, beside the switch that says who owns its config
   surface.

8. **Failure is quiet and bounded.** The box being away, or refusing the
   token, is logged once per state change and retried at the next
   reconciliation. It never becomes a warning of the daemon's own: a
   warning about failing to post warnings would be posted to the same box.

## Is this the best architecture?

**Better than the comparison targets.** The CCU's WebUI and the
aiohomematic family have no notion of a second program reporting into the
system's own status view; an add-on there is on its own page or nowhere.
The box's mechanism exists for exactly this, and the daemon is the first
consumer to feed it from a computed warning list instead of hand-written
calls.

**Better than before.** The operator of a box no longer has to open a
second UI to learn that the bridge behind it is failing, and the box's
LED follows. Nothing is duplicated: the daemon's list stays the one place
its warnings are computed, and the box's page stays the one place they
are silenced on the box.

**The right cut.** The box owns presentation, silencing and the LED. The
daemon owns what a warning is and how it is worded. The mirror is a
north-bound adapter of the warning aggregator, beside REST and MCP, and
knows the box only through the occulited client; the aggregator does not
learn that a box exists. godevccu's fake box learns the notice routes, so
the mirror is tested through the composition root against it.

**Weighed against a periodic comparison.** Comparing on a clock alone is
less code and heals itself, and was the alternative considered. It shows a
warning up to an interval late and asks the box when nothing changed.
Events show it at once; the slow resynchronisation keeps the healing.
Events cost a change hook in three places. The health tracker's and the
centrals' are pinned through the composition root's entry point, each
measured red with its subscription removed; the pairing manager's is
composed in the REST mount and has no pin of its own yet.

## Consequences

- New: the mirror, the health tracker's change hook, the occulited
  client's notice calls, a config field with labels and help in English
  and German, server-side catalogue entries for the warning sentences.
- godevccu's `litefake` gains the notice routes and the control token; a
  godevccu release and a version bump here.
- The first write to a real box is a live write and is done on a box the
  owner names. Until then the routes are verified against godevccu's fake
  and occulited's handlers (`internal/httpapi/addonnotices.go` at
  `04ee7b6`), not against a box.
- A daemon that stops leaves its notices on the box until it starts again
  and reconciles; the box offers a time-to-live per notice, which this
  decision does not use.

## Not decided here

- Per-user silences of the daemon are not carried to the box; the box
  silences per warning, for its administrators.
- Add-on views in the box's Control app (a separate box mechanism) are
  not used.
