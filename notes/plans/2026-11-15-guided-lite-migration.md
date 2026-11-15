# Guided move of a central from OpenCCU to openccu-lite

Slice plan, written 2026-11-15 after releases 0.103.0 and 0.104.0. It is the
hand-over for the implementing session; every fact carries where it was read.
"Read" means read in the source, not observed on a box.

## Where things stand

- 0.103.0 (#1105): a stored central can change its system type on save
  (`dropOtherSystemFields`, `internal/north/rest/handlers/admin_centrals.go`;
  `config.CentralFieldsOf`, `internal/config/config.go`).
- 0.104.0 (#1106, #1107): on an openccu-lite box with the add-on token file, the
  one local CCU central is moved to openccu-lite at start
  (`cmd/openccu-loom/lite_addon_onboard.go`, `convert`); where the daemon cannot,
  the `/health` component `config.lite_addon` names the central. ADR 0077 records
  it.

## Slice 0 — a defect in 0.104.0, first

**Finding.** The move saves an enabled central disabled and then enabled
(`lite_addon_onboard.go`, `convert`). The disabled save goes through
`liveCentralAdmin.Put` → `centralOrchestrator.removeCentral`
(`cmd/openccu-loom/central_adopt.go:969-990`, `:634-748`), and `removeCentral`
ends in `purgeCentralState` (`:864-929`) plus the Matter clean-up (`:733-747`).
So the move deletes, for that central:

- the VALUES and MASTER cache rows of its interfaces;
- the recording overrides (store and in-memory overlay);
- the visibility un-ignore patterns;
- the channel flags (hidden / locked overrides);
- the Matter exposures, and it retires the central's Matter partition
  (`setMatterPartitionRetirer`, `:350-358`; what retiring does to a
  commissioned fabric was **not read**).

ADR 0077's sentence "everything keyed by the central's name stays with it" is
therefore false for these stores. The same purge already runs when an operator
disables a central in the editor; that is existing behaviour and not part of
this slice, but it is worth one line to the owner.

**What to build.** A restart that is not a removal.

- `centralOrchestrator.restartCentral(ctx, cc)`: the teardown of
  `removeCentral` without `purgeCentralState`, without the Matter exposure
  delete and without the partition retire, followed by `adoptCentral(cc)`,
  under the same lifecycle lock.
- `liveCentralAdmin.Put`: an edit to an enabled, live central whose
  `centralConfigNeedsRestart` is true calls `restartCentral` instead of logging
  `central.edit.restart_required` (`:994-1000`). This is also what makes a
  manual type switch take effect without a daemon restart.
- `convert`: one `Put` of the new row; the disabled save goes.
- SPA: the central editor's "restart required" hint for central edits goes
  (`assets/ui/src/lib/components/settings/CentralsAdmin.svelte`, the comparison
  around the `system_type` / `username` / `password_plain` block near line 548).
- ADR 0077: correct the sentence, state what is kept.
- Release 0.104.1.

**Open judgement for the Fable read (before the slice merges).**

1. Does a restart retract the central's MQTT discovery and publish it again?
   `removeCentral` retracts the hub plane (`hubMQTT.RetractCentral`, `:713-716`)
   and evicts the model, which publishes a `DeviceRemovedEvent` per device
   (`:624-629`). For a type change the hub plane's programs and system variables
   legitimately go; whether Home Assistant keeps an entity's registry entry
   across a retract-and-republish with the same `unique_id` must be read in
   `../core` (`homeassistant/helpers/entity_registry.py`, deleted entities),
   not assumed.
2. Which of the kept stores are keyed by interface id, and are those ids the
   same on openccu-lite (`cachereset.StoreInterfaceID`, ADR 0060)? CUxD rows
   become orphans; decide whether the restart drops the rows of interfaces the
   new configuration no longer has.

**Tests.** The wiring rule applies: the pin constructs through the real
orchestrator and asserts the effect — after an edit that needs a restart, the
central is live on the new configuration **and** a seeded channel flag,
un-ignore pattern, recording override and Matter exposure are still there
(`tests/contract/wiring_pins/`; reference
`internal/central/hub_notifier_wiring_test.go`). Bite: put the purge back.
The e2e harness cannot seed a stored central before start
(`tests/e2e/harness`); extending `harness.Options` with seed rows is the way to
an e2e of the move through the built binary, and belongs to this slice.

## Slice 1 — "move pending" as a state, not a health line

- The onboarder already knows the state: openccu-lite host, a local CCU
  central, and why it was not moved (`recordStranded`). Give it a small
  read-only view (`pendingLiteMoves() []{central, reason}`) and feed the
  warnings aggregator through a new source beside `PairingSource`
  (`internal/warnings/warnings.go:79-118`, `:197-205`): warning id
  `lite-move:<central>`, `MessageKey: "warnings.lite_move_pending"`,
  `Central` set.
- The `/health` component `config.lite_addon` then goes, or it would produce a
  second, generic warning (`warnings.health.unhealthy`, `:124-138`). Decide in
  the plan review; the recommendation is to drop the component.
- The box notice mirror follows the aggregator
  (`cmd/openccu-loom/box_notice_wiring.go`), so the warning reaches the
  openccu-lite start page once the sentence `box_notice.lite_move_pending`
  exists in both server catalogues (`internal/i18n/catalogs/en.json`,
  `de.json`). The mirror authenticates with the add-on's **control** token
  (`/run/occulite/addon-tokens/openccu-loom`), not the API token; that the box
  writes it for an add-on it adopted from OpenCCU is read from occulited's
  `RefreshAddonTokens` (`internal/system/addonctl.go:195-222`, all rc.d ids)
  and **not observed**.
- Guard: the fan-out and subscriber guards if a new event type appears; the
  warning itself gets an aggregator unit test and one test through the
  composition root (the mirror receives it).

## Slice 2 — the guided dialog in the SPA

Everything it needs exists server-side: `POST /centrals/probe`,
`POST /centrals/pairing`, `GET /centrals/pairing/{id}`, and `PUT
/centrals/{name}` with `pairing_id` (`internal/north/rest/router.go:1434-1437`;
`CentralOnboarding`, `internal/north/rest/handlers/centrals_onboarding.go:24-31`).
No new endpoint and no DTO change is planned; if one turns out to be needed,
that is a stop condition (`assets/openapi.yaml`, `pkg/hmapi`).

- A dialog `CentralLiteMoveDialog.svelte`, opened from the warning (an action on
  the warning's row, and a query parameter on the centrals settings view):
  1. states what was detected — "this system now runs openccu-lite; central
     `<name>` is still a CCU central";
  2. one button starts the pairing against the central's own host — no host,
     type or interface input;
  3. shows "confirm in openccu-lite", with the link, and polls the pairing;
  4. on approval saves the central as openccu-lite with the `pairing_id` and
     the interfaces the probe returned; slice 0 makes it live without a
     restart;
  5. reports the result: devices loaded, and what goes (CUxD, system
     variables, programs).
- Rules that apply: `assets/ui/CLAUDE.md` (shared primitives, toast results,
  en + de for every string, both themes), the surface registry
  (`internal/north/ui/surface/`) for the entry point, vitest for the flow,
  Playwright baselines light + dark.
- MCP: record why not — the move needs a human confirmation in the box's UI.

## Slice 3 — documentation

A user page for the move (`docs/user/`, nav entry in `mkdocs.yml`): what
happens after the system update, the one confirmation, what changes (CUxD,
system variables, programs, backup and reboot scopes), and the fallback.

## For the owner, outside this repo

Points collected for an openccu-lite issue (not filed):

1. Start page: say when an add-on adopted from OpenCCU runs as root although
   the manifest in its own directory declares it can run confined. The file is
   there (`packaging/ccu-addon/ccu/update_script:81`); occulited reads it for
   `requires.rega` only (`internal/system/regadeps.go:66-77`), deliberately —
   reading it for a hint widens nothing either.
2. Offer the re-install from there and say what it does: manifest stored,
   add-on token issued, own user.
3. Show an add-on's open pairing request on the start page.
4. Point the `?sid=` hint at that step in this case.
5. `/usr/local` mode 0777 (openccu-lite/openccu-lite#22).

## Order

Slice 0 alone and first, released as 0.104.1. Then 1, 2, 3 as one feature
(0.105.0), each its own pull request.
