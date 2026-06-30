# Implementation plan — B8: Auto-tile dashboard (fleet overview)

## 1. Summary & status

**Status**: prioritised, not started. **Mostly frontend.**

> **Critical correction (verified 2026-06-30).** The AutoTile *concept*
> in [`docs/ui/auto-tile-concept.md`](../ui/auto-tile-concept.md) is
> **already implemented** — its "Concept approved … Not yet implemented"
> status header is stale. All four rollout phases shipped: the Go
> quantity catalogue, the `DataPointSummary` extension, `composer.ts`,
> `AutoTile.svelte` + the readout primitives, dispatcher integration, and
> `SensorActorTile` retirement. **Do not re-implement the auto-tile
> engine.**
>
> What does **not** exist is a fleet-wide **overview/dashboard route**:
> today `AutoTile` only renders *inside the device-detail channel panel*.
> B8 is therefore: build a whole-home overview route that reuses the
> existing tile dispatcher + `AutoTile` to lay out tiles across **all**
> devices, grouped and filterable. As a first step, update the concept
> doc's stale status.

## 2. Current state (verified)

### Auto-tile engine — already shipped (do not rebuild)

- **Go quantity catalogue**: `pkg/hmui/quantity.go` (+ `quantity_test.go`)
  — `Hint{ Icon, Semantic, StateColorRule }`, `HintFor(parameter, unit,
  paramType, valueList)` with unit / parameter-substring / enum-shape
  resolution layers.
- **Wire extension**: `pkg/hmapi/rest_contract.go` `DataPointSummary` has
  `Min`, `Max`, `Default` (~lines 414–416). The devices handler computes
  and emits the hint: `internal/north/rest/handlers/devices.go` (~line
  886) `hmui.HintFor(...)` → `UIHint *hmui.Hint` `json:"ui_hint"`.
- **Composer (Phase 2)**: `assets/ui/src/lib/sensor-actor/composer.ts`
  (~243 LOC) — pure TS, produces the layout description.
- **AutoTile (Phase 3)**: `assets/ui/src/lib/sensor-actor/AutoTile.svelte`
  (~287 LOC) + readouts under
  `assets/ui/src/lib/sensor-actor/readouts/` (`BooleanReadout`,
  `NumericReadout`, `EnumReadout`, `StringReadout`, `EventReadout`) +
  `classify.ts`, `primary.ts`, `state-color.ts`, `primitives/`
  (`TogglePill`, `ActionButton`, `NumericActionFeature`).
- **Dispatcher integration + Phase 4**: `assets/ui/src/lib/cdp/dispatch.ts`
  (CDP-kind → tile registry: Light/Climate/Cover/Lock/Siren/Switch/
  TextDisplay/Valve) and `assets/ui/src/lib/cdp/CdpTilesPanel.svelte`
  (~line 329) render one `AutoTile` per orphan channel as the fallback.
  `SensorActorTile.svelte` is **gone** (retired, as the concept's wizard
  decision Q5 specified).

### Where tiles render today

`CdpTilesPanel` is consumed by the **device-detail** view
(`assets/ui/src/routes/DeviceDetail.svelte`). There is **no fleet-wide
overview**: a grep for `overview` / `dashboard` / `home` route in
`assets/ui/src/App.svelte` and `assets/ui/src/routes/` returns nothing.
The closest existing surface is `assets/ui/src/routes/Favorites.svelte`
(a manually-curated quick-control grid) and `DeviceList.svelte` (a list,
with a working per-central filter at ~line 217 + `centralFilter`).

## 3. Design decisions

- **Reuse, don't reinvent.** The overview route renders each device's
  channels through the **existing** dispatcher pipeline (`dispatch.ts` →
  CDP tile → `AutoTile` fallback) exactly as `CdpTilesPanel` does. No new
  tile/widget code — the engine already covers every device.
- **Scope unit = channel tile.** The overview is a responsive grid of the
  same tiles `CdpTilesPanel` produces, but spanning the whole fleet rather
  than one device. Honour the composer's `gridSpan` (2 cells at ≥9
  readouts, per concept wizard Q4) so dense sensors (e.g. HmIP-SFD) lay
  out correctly.
- **Grouping.** Group tiles by **room**, with **function** and **central**
  as alternative groupings (multi-CCU: a per-central group header / filter
  is required — see `DeviceList.svelte` for the established filter
  pattern). Rooms/functions are per-central today (see
  `RoomsFunctionsAdmin.svelte`); the overview groups within each central,
  it does not merge rooms across CCUs (that is explicitly out of scope —
  see roadmap D4).
- **Filtering + persistence.** Filter by central / room / function /
  search, persisting selections to `localStorage` (mirror the
  `saveLS`/`loadLS` pattern in `DeviceList.svelte` / `Inbox.svelte`).
- **Data source.** Reuse the device + channel summaries the existing
  list/detail views already fetch (the `ui_hint`, `min`/`max`, `source`
  lifecycle, `value_age_seconds` are already on the wire). No new endpoint.
- **Performance.** A large fleet (hundreds of channels) means many tiles;
  use the same virtualization/pagination posture as `DeviceList` if the
  flat grid is too heavy, or lazy-render per group. Decide based on the
  real fleet size; start simple (render per-group, collapse empty groups).
- **Density.** The composer already emits `comfortable` / `compact`
  density tokens; the overview just honours them via the AutoTile classes.

## 4. Implementation steps

1. **Un-stale the concept doc**: update the Status header of
   `docs/ui/auto-tile-concept.md` to record that Phases 1–4 shipped and
   that the remaining work is the fleet overview route (this plan). (Docs
   are English; markdown links must resolve — `TestMarkdownLinksValid`.)
2. **Create the route** `assets/ui/src/routes/Overview.svelte`:
   - Fetch devices/channels via the existing API client methods
     `DeviceList.svelte` uses (reuse, do not add endpoints).
   - Wrap in `LoadingState` / `ErrorState` (retry) / `EmptyState`.
   - Build the grouped, filtered model (central → room/function →
     channels). Extract the grouping/filtering into a pure helper
     (`overview-grouping.ts`) so it is unit-testable without a DOM.
   - Render each channel through the existing dispatcher. Factor the
     "dispatch a channel to its tile" logic out of `CdpTilesPanel.svelte`
     into a small shared component/helper if it is not already reusable,
     so both device-detail and overview share one code path (avoid
     copy-paste of the dispatch chain).
3. **Register the route** in `assets/ui/src/App.svelte` (hash-router).
4. **Add the nav entry** in
   `assets/ui/src/lib/components/ui/Sidebar.svelte` (likely the top entry
   — this becomes the home/overview surface).
5. **Add i18n keys** (EN + DE) in `assets/ui/src/lib/i18n.ts` for the
   route title, group/filter labels, and empty/error copy.
6. **Optional follow-up stages** (separate, smaller tasks; the underlying
   widgets already exist per `docs/ui/sensor-actor-tile-concept.md`,
   `docs/ui/control-widget-concept.md`, `docs/ui/control-inventory.md`):
   per-room sub-dashboards, a "favourites first" pinned row reusing
   `Favorites.svelte` state, and the freshness-pulse polish (concept §5.5
   — `source` is already on the wire).

## 5. Config / API / i18n changes

- **Config**: none expected. If you add a persisted server-side
  preference (e.g. default grouping), that new `cfg:`-tagged field needs
  `config.field.<path>` **and** `config.help.<path>` in **both** EN and
  DE of `assets/ui/src/lib/i18n.ts` or `TestConfigFieldsHaveLabelsAndHelp`
  fails. Prefer `localStorage` for view prefs → no config, no test impact.
- **API contract**: none. `ui_hint`, `min`/`max`/`default`, `source`,
  `value_age_seconds` are already on `DataPointSummary` in
  `assets/openapi.yaml`. No `make export-schemas`, no `APIVersion` bump —
  **unless** you introduce a new endpoint (avoid it; reuse the device
  list/detail data).
- **i18n**: route + grouping/filter strings in EN + DE.

## 6. Tests

- **Vitest** (`overview-grouping.test.ts`): cover grouping by
  central/room/function, the multi-central case, filter + search, and the
  empty-group collapse. Pure-function tests, no DOM.
- **Playwright e2e + visual regression** (`assets/ui/tests/e2e/`): mock a
  multi-device, multi-central payload in
  `tests/e2e/helpers/mock-api.ts`; assert the grid renders grouped tiles,
  the central/room filter narrows the set, and a dense-sensor tile takes a
  2-cell span. Capture **light and dark** baselines for the new route.
- Reuse the existing AutoTile/dispatch tests
  (`assets/ui/src/lib/cdp/widgets/registry.test.ts`,
  `routing.test.ts`) — do not duplicate engine coverage; the overview
  test asserts *layout/grouping*, not tile internals.
- Name files after the unit; never `*_coverageN`.

## 7. Project-rule checklist

- SPA operating concept: `LoadingState` / `EmptyState` / `ErrorState`
  (never bare `<p>`); action results via `toastStore`; `Button` / `Card`
  / `Badge` / `Select` primitives; every colour utility carries a `dark:`
  variant or uses `--ha-*` tokens (both light + dark must look right —
  visual baselines enforce it).
- All strings localized via `t(...)` in EN + DE.
- **Multi-CCU safe**: never assume a single central — group/filter by
  `central` and label tiles with their central when more than one exists
  (mirror `DeviceList.svelte`).
- Reuse the shared dispatch path; do not fork `CdpTilesPanel`'s logic.
- If the concept doc is edited, keep markdown links valid and prose within
  the looser markdown rules (no code-comment purity constraints apply).

## 8. Acceptance criteria

- A new top-level **Overview** route renders tiles for the whole fleet,
  grouped by room (with function/central alternatives) and filterable,
  reusing the existing CDP tiles + `AutoTile` (no new tile components).
- A brand-new/unknown device renders as a coherent AutoTile in the
  overview with no per-device Svelte code (the engine already guarantees
  this; the overview just surfaces it fleet-wide).
- Multi-central setups show per-central grouping/labelling; rooms are not
  merged across centrals.
- `cd assets/ui && npm run test && npm run e2e` green with light + dark
  baselines; the concept doc status is corrected.

## 9. Effort

**M (frontend).** One route + a pure grouping helper + nav + i18n +
tests, plus a small refactor to share the dispatch path with
`CdpTilesPanel`. The heavy lifting (engine) is already done.

## 10. References

- [`docs/ui/auto-tile-concept.md`](../ui/auto-tile-concept.md) — the
  approved concept (status header to be corrected; §8 Rollout describes
  the now-shipped Phases 1–4).
- `docs/ui/sensor-actor-tile-concept.md`,
  `docs/ui/control-widget-concept.md`, `docs/ui/control-inventory.md` —
  follow-up stages (largely realized by existing widgets).
- `CLAUDE.md` → *Architecture Quick Reference → SPA operating concept*;
  *Testing Guidelines → SPA browser-e2e + visual regression*.
- Roadmap entry B8 in [`docs/roadmap.md`](../roadmap.md). Related: D4
  (cross-CCU overview) shares the per-central grouping concern.
