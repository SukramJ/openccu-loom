# Implementation plan — tabular, model-driven device configuration

**Status:** in execution — cuts 1–3 are merged, cut 4 is next. See
[Where this stands](#where-this-stands--read-this-first) for the current
state, the machine-local prerequisites, and where this plan turned out to
be wrong. Owner decisions taken on 2026-09-07 are recorded under
[Decisions](#decisions) and in the execution log. Each cut below is its own
branch and PR and needs the owner's go before it starts.
**Audience:** a fresh agent with no access to the concept conversation.
Everything needed is inline; every "measured" claim carries the
`file:line` it was read from at `main @ d3e741db` (0.75.0). Re-verify a
line number before relying on it — the files are large and move.
**Companion:** the concept the owner approved is an artifact
(`Konzept Gerätekonfiguration`, 2026-09-07); this file is the executable
half of it.

---

## Where this stands — read this first

Status as of 2026-09-07, written so the work can be picked up on another
machine. Everything below is either merged or an open PR; nothing needed
lives only on the machine this was written on.

| Cut | State |
|---|---|
| 1 — inbox accept renames channels | **merged**, [#721](https://github.com/SukramJ/openccu-loom/pull/721) |
| 2 — door-lock operation-mode labels | **merged**, code half [#722](https://github.com/SukramJ/openccu-loom/pull/722) + [#723](https://github.com/SukramJ/openccu-loom/pull/723), data half [openccu-data#33](https://github.com/SukramJ/openccu-data/pull/33) → go-openccu-data v0.1.4, module bump [#726](https://github.com/SukramJ/openccu-loom/pull/726) *(open)* |
| 3 — link editor null link | **merged**, [#725](https://github.com/SukramJ/openccu-loom/pull/725) |
| 4 — channel table | **next**, not started |
| 5 — parameter table | not started |
| 6 — device table | not started |

The plan itself is [#724](https://github.com/SukramJ/openccu-loom/pull/724).

### Picking it up elsewhere

1. `git pull` on `main`. If [#726](https://github.com/SukramJ/openccu-loom/pull/726) is
   still open, that branch carries the module bump to v0.1.4 and the
   guards that go with it; land it before cut 4 so the door-lock labels
   and the reshaped borrowing guard are in.
2. `make setup` (Go tooling + the pre-commit hook), then `make ui-install`
   and `cd assets/ui && npx playwright install chromium` — the SPA e2e
   suite needs the browser, and the Playwright baselines are per platform
   (`*-darwin.png` locally, `*-linux.png` only in the pinned container).
3. The sibling checkouts this plan cites — `../openccu-data`,
   `../go-openccu-data`, `../OpenCCU`, `../OpenCCU-Base`, `../aiohomematic`
   and friends — are read alongside the repo, not vendored. Cut 2 needed
   all of them; cuts 4–6 need none.
4. `./.ccu_cred` (gitignored, read-only account) is what the CCU probes in
   cut 2 used. **Cuts 4–6 need no CCU access at all** — every remaining
   cut is hermetic.

### Owner decisions taken during execution

Recorded because they are not derivable from the code, and a later agent
would otherwise re-open them:

| Question | Decision |
|---|---|
| Model stamp for rooms / functions on accept | Stamp the model too, in the same cut |
| ISE-id batching for channel renames | Do it in the same cut, not as a follow-up |
| How to restrict the index fallback | The measured rule (refuse the unqualified index for a channel-typed parameter), not the plan's stage-1-only rule |
| The borrowed help text | Belongs upstream in openccu-data, not in the daemon |
| Wording for the door-lock tokens | Curate from the CCU's own vocabulary, after reading the CCU first |
| The non-deterministic tie-break | Its own PR, ahead of the label work |

### Still open, deliberately

- **The help text is still wrong on the door lock.** It reads "Durch den
  Flüsterbetrieb fahren die Heizkörperthermostate langsamer" — a radiator
  thermostat's text — because the parameter-help table is keyed by
  parameter name alone (167 entries for `de`, none channel-type-qualified).
  The daemon cannot attribute a help text to a channel type at all. Owner's
  decision: fix belongs upstream. Captured in
  `notes/parity/fixtures/hmip-dlp-channel-operation-mode.json` under
  `daemon_help_text_defect`.
- The Python client fan-out for cut 4's `ChannelSummary` fields
  (`../openccu-loom-client/wire/`) — a separate PR in that repo, noted in
  cut 4 and out of scope here.

### What cut 4 needs before it starts

It is the first cut with a backend half, and the first that touches the
API contract, so it carries obligations the three bug-fix cuts did not:

- `assets/openapi.yaml` first, then the handler, then
  `make export-schemas && make ui-types` in the same commit
- `APIVersion` in `internal/north/rest/handlers/info.go` bumped alongside;
  verify with `bash script/check_api_version_bump.sh origin/main`
- the MCP surface (`internal/north/mcp/tools_hub.go`) carries its own
  `channelSummary` — extend it or record why not
- Playwright baselines for the device page will move; refresh `-darwin`
  locally and `-linux` only inside the pinned container

---

## How this plan was wrong, and how that was found

Two of cut 2's instructions did not survive contact with the data, and
cut 3's root cause — which the plan left open — turned out to be
reproducible only in one of the two ways it proposed. Everything here is
load-bearing for anyone reading the cuts below; the measurements behind
each claim are in the PRs the state table links.

**Cut 1 grew two items during execution, both owner-approved:**

- The reorder moves the room / function writes after the ingest, and those
  only reach the CCU — so the model is stamped there too. Without it a
  freshly accepted device rendered without its room and function until the
  five-minute device-details restamp.
- The rename hook resolved every address through its own full
  `Device.listAllDetail`, so the fix turned one listing into 1 + n. Batched
  into a single resolve via a new `CcuBackend.GetIseIDsByAddresses` and an
  optional batch rename hook. `backends.Operations` was **not** extended —
  the wiring already holds the concrete `*CcuBackend`, so the stop condition
  step 5 names never applied.

**Cut 2: two corrections to what is written below.**

1. **Step 2's rule is wrong as stated.** "The index retry may only resolve
   through a channel-type-qualified key" was measured against the embedded
   v0.1.3 extract before being implemented: 526 of 587 index keys are
   *unqualified*, and 105 parameters have the unqualified index retry as
   their only label source (siren tone selection, DST month and weekday, PIR
   sensitivity, climate display modes …). That rule would have replaced one
   wrong enum with a hundred untranslated ones. What shipped instead: the
   unqualified index key is refused only for a parameter the table keys by
   channel type *somewhere* — the table's own statement that the parameter's
   enum varies per channel type. Measured blast radius: exactly 7 parameters,
   identical in de and en. The value-only stage 4 is refused for an index
   unconditionally, because an index is a position and that stage answers
   about values.
2. **The Files table names the wrong repository.** `go-openccu-data/data/` is
   generated and must never be hand-edited (its own `CLAUDE.md` says so); the
   curated overlay lives in `openccu-data/openccu_data/data/translation_custom/`.
   The release chain is openccu-data release → `repository_dispatch`
   regenerates go-openccu-data → module tag → `go.mod` bump here.

**Cut 2 also found two things the plan did not predict:**

- The reported "Flüsterbetrieb" was never a value label — it is the **help
  text**. The daemon shows a radiator-thermostat text on a door lock because
  `Translations.ParameterHelp` is keyed by parameter name alone (167 entries
  for `de`, none channel-type-qualified). Owner's decision: fix belongs
  upstream in openccu-data. Recorded in the fixture under
  `daemon_help_text_defect`.
- The value-only reverse index was **non-deterministic**: ties between
  equally short labels were resolved by Go's map iteration, so "off" gave
  "Aus" 29× and "aus" 11× across 40 loads. Fixed in #722; without it no test
  can pin a label at all.

Also: the CCU carries **no** wording for the door lock's four tokens —
verified read-only against `stringtable_de.txt`, the WebUI language files,
`../OpenCCU`, `../OpenCCU-Base`, `openccu-data` and the Python reference
family. The curated wording is a formulation derived from the CCU's terms
for the neighbouring settings, and is labelled as such in the upstream
commit.

**Cut 3's root cause, which the plan listed as not reproduced.**
`ChannelPanel.save()` reads its props again after `await
api.putLinkParamset(...)`, to reload what it just wrote. `LinkConfigPanel`
derived those props from the `Link` object in `DeviceLinks`' nullable
`editing` holder, so leaving the editor while the PUT was in flight nulled
the holder underneath the running save. The editor takes primitives now,
snapshotted when it opens.

Two things that cost time there and are worth knowing before repeating the
exercise:

- **A hand-written approximation of the pre-fix source did not bite.**
  Rebuilding the old shape as `const senderAddress =
  $derived(link.sender_address)` kept the test green: a `$derived` answers
  from cache once its effect is destroyed, while a direct property read in
  the template recomputes. The regression has to be restored from git, not
  re-typed.
- **The browser cannot express the race.** A Playwright test that closes
  the editor during a delayed PUT is green before *and* after the fix,
  because by the time the response lands the teardown has fully settled.
  That version was written, measured as unable to bite, and replaced with
  one that pins what a browser can prove.

---

## Decisions

| Question | Decision |
|---|---|
| Device list: table in addition to cards, or instead? | **Replace the cards.** The table becomes the only device-list layout. |
| Write preview before MASTER/LINK writes? | **On by default, switchable** in preferences. Never for VALUES. |
| Order of work? | **Plan everything first, then approval per cut.** Bugs (cuts 1–3) before the tables (cuts 4–6). |
| Channel header format | `Türschlossantrieb (HmIP-DLP, Kanal 12)` — type label, model, channel number; the address in monospace beside it. |
| Channel editor placement | Below the channel table, full width. Not an expandable row. |
| The CCU's "Tüschlossantrieb" typo | Corrected upstream in the curated overlay, not in the daemon. (Overlay source is `openccu-data`, not `go-openccu-data` — see the Execution log.) |

---

## Rules that apply to every cut

These come from the root `CLAUDE.md` and `assets/ui/CLAUDE.md`; they are
repeated here because a fresh agent will not have read the concept.

- **Bug fixing is test-first.** Failing reproducer, then fix, then the
  bite proof: remove or revert the production change, observe the test
  red with its message, restore, green. A guard that only calls the new
  helper directly does not count — guard the caller
  (`notes/contributor/engineering-rules.md`).
- **Every user-visible SPA string** goes through `t(...)` with BOTH
  locales in `assets/ui/src/lib/i18n.ts` (the `EN` and `DE` catalogues).
  Add a case to `assets/ui/src/lib/i18n.catalog-coverage.test.ts` for each
  new key group — `t()` falls back silently, so a component test cannot
  catch a missing entry.
- **Four theme combinations** (`data-skin` loom / ha × light / dark). Use
  the `--ha-*` tokens or `dark:` variants; never a one-theme colour.
- **Playwright baselines are per platform.** Refresh `*-darwin.png`
  locally with `npm run e2e:update`; refresh `*-linux.png` only inside the
  pinned `mcr.microsoft.com/playwright` image (read the tag from
  `.github/workflows/spa-e2e.yml`). A local full run of
  `doc-screenshots.spec.ts` overwrites `docs/user/img/` with macOS
  renders — do not commit those.
- **Svelte edits need `npm run check`** (svelte-check); `npm run typecheck`
  is `tsc` only and does not read `.svelte` files.
- **Gate before every push**, from the repo root:

  ```sh
  make test && make lint
  cd assets/ui && npm run check && npm run typecheck && npm test && npm run e2e
  ```

- **Commits:** conventional, scoped, `git commit -s`, no `Co-Authored-By`.
  Branch names `fix/<desc>` or `feat/<desc>`. Never open a PR unless the
  owner asks; pushing is not PR permission.
- **Sub-agents:** no sub-agent runs `make test` or a repo-wide lint; no
  two writing agents in one package. The composition root, DTOs,
  `assets/openapi.yaml` semantics and auth stay in the main conversation.
- **Live CCU (172.18.4.29) is read-only** unless the owner names the
  target device for a write. Nothing in this plan needs a live write.
- **`CHANGELOG.md`** gets an entry under *Unreleased* for every cut
  (user-visible change); no version bump in these cuts.

---

## The map — measured facts this plan rests on

| Fact | Where |
|---|---|
| Device page: three top tabs, four configure sub-tabs; the channel strip renders chips with name/type label and a `(data_points_count)` suffix, the channel number only in the tooltip | `assets/ui/src/routes/DeviceDetail.svelte:1100-1134` |
| Channels are already resorted numerically after the REST handler's string sort; week-profile channels are filtered out of the strip | `DeviceDetail.svelte:304-311` |
| Channel-number deep link `#/devices/<addr>/channels/<n>` | `DeviceDetail.svelte:674` |
| Per-channel rename, rooms/functions, team picker, flag toggles, then `ChannelPanel paramset="MASTER"` | `DeviceDetail.svelte:1160-1257` |
| Parameter editor renders cards in a two-column grid; DST / time-pair / LINK-category grouping already exists | `assets/ui/src/lib/components/channel/ParameterGrid.svelte:206-259` |
| Widget heuristics (radio ≤ 4 options, slider for finite range, multiplier projection) | `assets/ui/src/lib/components/parameter/ParameterField.svelte:168-260` |
| The UI schema carries `control` per parameter | `pkg/hmapi/rest_contract.go:632`, filled at `internal/central/adapter/uischema_adapter.go:461` |
| The SPA reads `control` only for the Bedienen tiles | `assets/ui/src/lib/control/resolver.ts:39` |
| Save path: `putParamset` / `putLinkParamset` with only dirty names, then `load()`, then wake-up hint or success toast; the failure toast is `channel.save_failed` | `assets/ui/src/lib/components/channel/ChannelPanel.svelte:573-612` |
| Channel DTO of `GET .../channels` (`ChannelSummary`) | `internal/north/rest/handlers/devices.go:186-215`; OpenAPI at `assets/openapi.yaml:8979` |
| MCP has its own `channelSummary` | `internal/north/mcp/tools_hub.go:500-509` |
| `LinkSourceRoles` / `LinkTargetRoles` live on the model channel | `internal/model/device/channel.go:1515-1545` |
| Device list already has a DataTable mode (`prefs.deviceView === "list"`) beside the card grid | `assets/ui/src/routes/DeviceList.svelte:73-79, 300-308, 509-560` |
| `DataTable` supports sort, one search box, `persistKey`, a `cell` snippet, `rowClass`; no row click, no selection, no per-column filter, no expansion | `assets/ui/src/lib/components/ui/DataTable.svelte`, `data-table.ts` |
| `DeviceCard` is used only by `DeviceList.svelte` (and its own test) | grep over `assets/ui/src` |
| Preferences store fields, localStorage-backed | `assets/ui/src/lib/stores/preferences.svelte.ts:15-31` |
| Inbox accept: `finishAccept` runs `applyInitialConfig` (rename, rooms, functions) **before** `AcceptPendingDevice` materialises a deferred device | `internal/central/adapter/device_admin.go:275-284` |
| `RenameDeviceWithChannels` returns before the channel loop when the device is not in `ModelRegistry` | `internal/central/central.go:1083-1090` |
| The rename hook resolves every address through a full `Device.listAllDetail` | `internal/central/adapter/ccu_wiring.go:904-922`, `internal/client/backends/ccu_extended.go:909-939` |
| Value-list labels: four-stage lookup, stage 4 is a value-only "shortest label for this value across all parameters" fallback | `internal/central/adapter/valuelabels.go:38-54`, `internal/ccudata/translations.go:365-403` |
| go-openccu-data v0.1.3 extract has no door-lock value labels; the curated overlay knows only the parameter label `channel_operation_mode_door_lock`; `channel_types_de` carries `door_lock_transceiver => Tüschlossantrieb` | `$(go list -m -f '{{.Dir}}' github.com/SukramJ/go-openccu-data)/data/translation_extract.json.gz`, `.../translation_custom/parameters_de.json:81` |
| godevccu's HmIP-DLP description has no `CHANNEL_OPERATION_MODE` at all | `../godevccu/internal/embed/data/device_descriptions/HmIP-DLP.json` |
| No HmIP-DLP on the open CCU 172.18.4.39; the one that has it (172.18.4.29) answers XML-RPC with 401 — JSON-RPC with the read-only account works, `Interface.getParamsetDescription` takes `paramsetKey` and answers a list | probed 2026-09-07 |
| The DLP's real `CHANNEL_OPERATION_MODE` value lists (channels 2, 3, 12), the daemon's de/en labels for them, and the raw channel-12 description | `notes/parity/fixtures/hmip-dlp-channel-operation-mode.json` |
| Existing e2e specs that drive the device page | `assets/ui/tests/e2e/device-detail.spec.ts`, `channel-editor.spec.ts`, `channel-export-import.spec.ts`, `links.spec.ts`, `virtual-remote.spec.ts`, `doc-screenshots.spec.ts` |
| Visual baselines that exist for these views | `assets/ui/tests/e2e/visual.spec.ts-snapshots/device-list-{light,dark}-chromium-{darwin,linux}.png` |

---

## Cut 1 — channels are renamed along on inbox accept

> **Done** — [#721](https://github.com/SukramJ/openccu-loom/pull/721).

**Branch:** `fix/inbox-accept-renames-channels`
**Symptom (owner):** after pairing, "Kanäle mitbenennen" was ticked, only
the device (channel 0) got the name.
**Root cause (measured):** `finishAccept` renames before the deferred
device is materialised, so `RenameDeviceWithChannels` finds no model
device and skips the channel loop. The non-deferred path (device already
in the registry) is not affected by this ordering.

### Files

| Action | Path |
|---|---|
| change | `internal/central/adapter/device_admin.go` — `finishAccept` |
| new | `internal/central/adapter/device_admin_accept_rename_test.go` |
| read only | `internal/central/central.go:1076-1110`, `internal/central/adapter/pending_devices.go:89-117`, `internal/central/adapter/callback_handlers_delay_test.go:111-168` (template for building a unit with a parked device) |

### Steps

1. **Reproducer first.** Build a `central.Unit` with a real device-ingest
   function that populates `ModelRegistry` (grep the existing tests for
   `RenameDeviceWithChannels` and `SetDeviceIngestFn` and reuse whichever
   helper already materialises a device with ≥ 3 channels from
   `hmproto.DeviceDescription`s). Park the device through
   `NewCallbackHandlers(...).SetDelayNewDeviceCreation(true)` +
   `NewDevices`, exactly as `TestDeferredDeviceIsAnnouncedOnTheInboxAndMaterialisedOnAccept`
   does. Install a recording `RenameDeviceFn`. Call
   `DeviceAdminDomain.AcceptInboxDevice(ctx, address, interfaces.AcceptInboxOptions{Name: "Haustür", IncludeChannels: true})`
   on a domain whose central has no CCU-side inbox accepter (the
   `hub.ErrNoInboxAccepter` branch at `device_admin.go:218-224`) — that is
   the deferred-only path. Assert the recorder saw the device address
   **and every channel address** with `"Haustür:<n>"`. Today it sees the
   device only. Name the test after the behaviour, e.g.
   `TestAcceptInboxDeviceRenamesEveryChannelOfADeferredDevice`.
2. **Second case, regression guard for the other path:** the same with the
   device already materialised (no delay). Expect the same recorder
   contents. This should already pass; keep it so the reorder cannot
   regress it.
3. **Fix.** In `finishAccept`, materialise first, then configure:

   ```go
   _, acceptErr := AcceptPendingDevice(ctx, u, address)
   configErr := applyInitialConfig(ctx, u, address, opts)
   ```

   Keep the "every step is attempted" property: run
   `applyInitialConfig` even when `acceptErr != nil` (the CCU has
   accepted the device either way), then join: an accept error is
   returned as is; a config error alone keeps the
   `ErrAcceptConfigIncomplete` wrapping. Rewrite the doc comment on
   `finishAccept` and drop the sentence in `applyInitialConfig`'s comment
   that says the device "may not have materialised" — after this cut it
   has. Leave the rooms/functions remotes as they are (they address the
   CCU directly, which is still the right call).
4. **Bite proof.** Swap the two lines back, run the test, paste its red
   message into the PR description, restore.
5. **Side finding, optional in this cut:** every channel rename triggers
   a full `Device.listAllDetail` (`ccu_extended.go:913`). A 13-channel
   device costs 14 full lists. If it fits in an hour: resolve the
   device's whole ise-ID map once in the hook by walking the one
   `listAllDetail` answer for `<address>` and every `<address>:<n>`, and
   thread the map through `RenameDeviceWithChannels` via a new optional
   hook `SetRenameDeviceBatchFn`. **Stop condition:** this changes the
   `backends.Operations` surface (CUxD stub included) — describe the
   shape in the PR and ask before extending the interface; otherwise
   leave it as a follow-up note in the changelog entry.
6. `CHANGELOG.md` → Unreleased → Fixed.

### Acceptance

```sh
go test ./internal/central/adapter/ -run 'AcceptInboxDevice|Rename' -count=1 -v
```

then the full gate.

---

## Cut 2 — HmIP-DLP `CHANNEL_OPERATION_MODE` shows "RGB"

> **Done** — code [#723](https://github.com/SukramJ/openccu-loom/pull/723)
> (with [#722](https://github.com/SukramJ/openccu-loom/pull/722) ahead of it),
> data [openccu-data#33](https://github.com/SukramJ/openccu-data/pull/33),
> module bump [#726](https://github.com/SukramJ/openccu-loom/pull/726).

> **Read [How this plan was wrong](#how-this-plan-was-wrong-and-how-that-was-found)
> first — step 2's rule and the repository named in the Files table were
> both corrected during execution.**

**Branch:** `fix/door-lock-operation-mode-labels`
**Symptom (owner):** the lock's operation-mode enum shows "RGB" where
"Flüsterbetrieb" belongs.
**Root cause (measured 2026-09-07, capture in
`notes/parity/fixtures/hmip-dlp-channel-operation-mode.json`):** the
DLP carries `CHANNEL_OPERATION_MODE` on three channels; on channel 12
(`DOOR_LOCK_TRANSCEIVER`) its `VALUE_LIST` is
`IGNORE_DOOR_OPEN SKIP_HOLD_TIME_OPENING SKIP_RELOCK_DELAY_CLOSING SKIP_HOLD_TIME_OPENING_RELOCK_DELAY_CLOSING`.
No translation table knows these tokens, so `ValueListLabel`
(`valuelabels.go:44-51`) falls back to the **index** as a string. Index
`0` and `1` then hit stage 2 (`channel_operation_mode=0` → "Inaktiv",
`=1` → "Aktiv" — labels extracted for a different device), index `2` and
`3` hit the stage-4 value-only index ("Ein", "RGB" — the shortest labels
any parameter has for the values `2` and `3`). Measured with
`ValueListLabels(tr, "de", "DOOR_LOCK_TRANSCEIVER", "CHANNEL_OPERATION_MODE", …)`
against the embedded v0.1.3 translations: `[Inaktiv Aktiv Ein RGB]`,
en `[Inactive Active On RGB]`. Channel 2 (`ACCELERATION_TRANSCEIVER`,
`OFF TILT_DETECTION ANY_MOTION TILT_AND_MOTION_DETECTION`) and channel 3
(`DOOR_STATE_TRANSCEIVER`, `OFF ON ON_AUTO_CALIBRATION`) show the same
defect with the same wrong labels. So it is not one missing overlay
entry: **the index fallback itself invents labels** whenever a token is
unknown.

### Precondition — wording the owner supplies

The CCU's own string tables (`/config/stringtable_de.txt`,
`/webui/js/lang/{de,en}/translate.lang.*.js`) carry **no** entries for
these tokens (probed 2026-09-07), so the CCU WebUI cannot be the source
of "Flüsterbetrieb". The owner names the label for each of the four
channel-12 tokens (de and en), and for the channel-2 and channel-3 tokens
if they want them curated in the same cut. Until then the correct
rendering is the humanised raw token, never a borrowed label.

### Files

| Action | Path |
|---|---|
| new | `internal/central/adapter/valuelabels_index_fallback_test.go` — table test on the captured value lists (all three channels) |
| change | `internal/central/adapter/valuelabels.go:44-51` — the index retry resolves only through a channel-type-qualified key |
| read only | `internal/ccudata/translations.go:365-403` — the four stages; unchanged, the token lookup keeps all of them |
| change | `../go-openccu-data/data/translation_custom/parameter_values_de.json`, `parameter_values_en.json` — `door_lock_transceiver\|channel_operation_mode=<token>` entries |
| change | `../go-openccu-data/data/translation_custom/channel_types_de.json` — `door_lock_transceiver: Türschlossantrieb` |
| change | `go.mod` / `go.sum` — bump go-openccu-data after its release |
| read only | `internal/central/adapter/valuelabels.go`, the overlay loader in `internal/ccudata/` (find how `translation_custom` merges and whether `channel_types` is part of the merge — if not, that is a second small change there) |

### Steps

1. **Reproducer.** Load the fixture, feed each channel's `VALUE_LIST`
   and channel type into
   `ValueListLabels(ccudata.LoadTranslationsEmbedded(), locale, channelType, "CHANNEL_OPERATION_MODE", values)`
   and assert: with no curated entry, every label is `humanizeRaw(token)`
   — never "RGB", "Ein", "Aktiv", "Inaktiv". Today the assertion fails on
   all four indices of channel 12. Then a second table with the curated
   wording (once the owner supplies it) asserting the curated labels.
2. **Fix, code half — the index fallback stops guessing.** In
   `ValueListLabel` (`valuelabels.go:48-51`) the index retry may only
   resolve through a **channel-type-qualified** key (stage 1 with the
   index) — that is the one legitimate use of an index key, the CCU
   string tables that key some enums by position for a specific channel
   type. It must not reach the unqualified stage 2 nor the value-only
   stage 4, because neither knows which enum the index belongs to. Keep
   the token lookup exactly as it is (all four stages). Document the
   reason in the comment: an index is meaningful only inside the enum it
   was taken from.
3. **Fix, data half (upstream).** Add the curated entries to
   go-openccu-data's overlay, both locales, keyed with the channel type
   (`door_lock_transceiver|channel_operation_mode=<token lowercased>`)
   so no other channel type can borrow them; the same for channels 2 and
   3 if the owner supplies wording. Correct the channel-type typo
   (`door_lock_transceiver` → `Türschlossantrieb`) in the same overlay.
   Run that repo's own tests, tag a release, bump `go.mod` here.
   **Stop condition:** the owner cuts the upstream release; prepare the
   upstream commit and stop.
4. **Negative control.** A case that constructs `Translations` without
   the curated overlay must still yield the humanised raw token. A case
   that asks for index `3` under a *different* channel type must not
   return the door lock's curated label. Together they prove the lookup
   no longer borrows.
5. Bite proof: revert the code half → red; revert the data half → red;
   restore both → green. Both must bite independently.
6. `CHANGELOG.md` → Unreleased → Fixed (two lines: the label, the typo).

### Acceptance

```sh
go test ./internal/ccudata/ ./internal/central/adapter/ -run 'ValueList|DoorLock|Translations' -count=1
```

then the full gate.

---

## Cut 3 — link editor: "Cannot read properties of null (reading 'sender_address')"

> **Done** — [#725](https://github.com/SukramJ/openccu-loom/pull/725). The
> root cause the plan left open is reproduced below in "How this plan was
> wrong"; the browser could not express the race, only vitest could.

**Branch:** `fix/link-editor-null-link`
**Symptom (owner):** changing a direct link fails with
`channel.save_failed` and that message.
**What is measured:** the toast is raised only in `ChannelPanel.save()`'s
catch (`ChannelPanel.svelte:602-605`), so the exception is thrown inside
that `try`. Nothing in that block reads `sender_address`. The only
nullable `Link` holders in the SPA are `editing` and `renaming` in
`assets/ui/src/lib/components/links/DeviceLinks.svelte:32-35` and the
`link` prop of `LinkConfigPanel.svelte`, whose `$derived` expressions
read `link.sender_address` unguarded (`LinkConfigPanel.svelte:26-33`).
The message names `null`, not `undefined`, which matches those holders
and rules out a failed `.find()`.
**Root cause:** **not reproduced yet.** The plan is reproduce → fix; if
the reproducer stays green, stop and ask the owner for a browser stack
trace from the real instance (DevTools console, the red entry under the
toast).

### Files

| Action | Path |
|---|---|
| new | `assets/ui/src/lib/components/links/LinkConfigPanel.null-link.test.ts` |
| new or extend | `assets/ui/tests/e2e/links.spec.ts` — a save with a delayed `PUT **/link-ps/**` |
| change | `assets/ui/src/lib/components/links/LinkConfigPanel.svelte`, `DeviceLinks.svelte` |
| read only | `assets/ui/src/lib/components/channel/ChannelPanel.svelte:180-215, 573-612`, `assets/ui/tests/e2e/helpers/mock-api.ts` |

### Steps

1. **Reproducer A (vitest).** Mount a small host component with
   `let editing = $state<Link \| null>(link)` and
   `{#if editing}<LinkConfigPanel link={editing} …/>{/if}`; mock
   `api.putLinkParamset` to a promise you resolve by hand; make one field
   dirty, click Save, set `editing = null` while the PUT is pending,
   resolve the PUT, flush. Assert no `toastStore.error` with
   `sender_address` in its detail, and no unhandled rejection.
2. **Reproducer B (Playwright, hermetic).** In `links.spec.ts`, route
   `**/api/v1/devices/*/link-ps/**` with a 1.5 s delay, open a link's
   *Konfigurieren*, change a value, click Save, click *Zurück zur Liste*
   during the delay. Assert the toast that appears is a success (or the
   wake-up hint), never `channel.save_failed`.
3. **Fix.** `LinkConfigPanel` takes primitives — `senderAddress`,
   `receiverAddress`, `name`, and the four party labels — instead of the
   `Link` object, so no reactive read can dereference a nulled parent
   state. `DeviceLinks` keeps `editingKey: string \| null`
   (`"<sender>-><receiver>"`) plus an `editingSnapshot` of those
   primitives taken when the editor opens; a list reload never replaces
   or nulls what the open editor renders. `renaming` gets the same
   treatment for symmetry (its save path is short, but it reads the
   holder after an `await`).
4. Bite proof: restore the object prop → reproducer A red; restore
   primitives → green.
5. `CHANGELOG.md` → Unreleased → Fixed.

### Acceptance

```sh
cd assets/ui && npx vitest run src/lib/components/links && npx playwright test tests/e2e/links.spec.ts
```

then the full gate.

---

## Cut 4 — channel table and channel header

> **Next.** Not started. First cut with a backend half and an API-contract
> obligation — see [Where this stands](#where-this-stands--read-this-first).

**Branch:** `feat/channel-table`
**Goal:** the chip strip becomes a sortable table; the selected channel's
editor follows below it, headed by `Türschlossantrieb (HmIP-DLP, Kanal 12)`.

### Backend half (main conversation owns it)

`ChannelSummary` gains the link roles so the SPA can show *Sender* /
*Empfänger* without a second request:

```go
// LinkSourceRoles / LinkTargetRoles are the raw CCU LINK_SOURCE_ROLES /
// LINK_TARGET_ROLES tokens. Empty when the channel cannot take part in a
// direct link on that side.
LinkSourceRoles []string `json:"link_source_roles,omitempty"`
LinkTargetRoles []string `json:"link_target_roles,omitempty"`
```

| Action | Path |
|---|---|
| change | `internal/north/rest/handlers/devices.go` — struct at `:186`, and the builder that fills `ChannelSummary` (grep `TypeLabel:` in that file) |
| change | `internal/north/mcp/tools_hub.go:500-509` — same two fields (the MCP surface rule) |
| change | `assets/openapi.yaml:8979` — `ChannelSummary` properties; `info.version` minor bump |
| change | `internal/north/rest/handlers/info.go:19` — `APIVersion` `11.1.0` → `11.2.0` |
| generate | `make export-schemas && make ui-types` (both, in the same commit as the bump; the generated SPA types are a CI guard of their own) |
| test | `internal/north/rest/handlers/devices_test.go` — the fields appear for a channel with roles and are absent otherwise |

Verify locally with `bash script/check_api_version_bump.sh origin/main`
after committing the bump. The Python client fan-out
(`../openccu-loom-client/wire/`) is a separate PR in that repo; note it in
the PR description, do not do it here.

### Frontend half

| Action | Path |
|---|---|
| change | `assets/ui/src/lib/components/ui/DataTable.svelte` + `data-table.ts` — add `onRowClick?: (row: Row) => void`, `selectedKey?: string \| null` (adds `aria-selected` and a selected row class), `numeric?: boolean` on a column (sets `tabular-nums` and right alignment). Keep every existing caller green. |
| new | `assets/ui/src/lib/components/channel/ChannelTable.svelte` — the table; props `channels`, `selected`, `linkCounts: Map<string, number>`, `onSelect(ch)`, `compact?: boolean` (cut 6 reuses it read-only) |
| new | `assets/ui/src/lib/channel/channel-roles.ts` — `roleOf(ch): "sender" \| "receiver" \| "both" \| "none"` from the two arrays, `isVirtualChannel`, `channelHeader(ch, model, t)` |
| new | `assets/ui/src/lib/channel/channel-roles.test.ts` |
| change | `assets/ui/src/routes/DeviceDetail.svelte:1095-1134` — replace the strip with `ChannelTable`; keep `selectedChannel`, the deep link at `:674`, and the week-profile redirect (the row for a `WEEK_PROFILE` channel calls `configSub = "schedule"`); move the header block (`:1160-1182`) under the table and format it via `channelHeader` |
| change | `assets/ui/src/routes/DeviceDetail.svelte` — load `api.listLinks(detail.address, locale)` lazily the first time the channels sub-tab opens; count per channel address on this device; on failure the column shows `—`, never an error state (the links tab owns errors) |
| change | `assets/ui/src/lib/i18n.ts` — keys below, both catalogues |
| change | `assets/ui/src/routes/DeviceDetail.test.ts` — the tests that click chips by text (`channel rename`, `room/function assignment`, `schedule-tab`) now click table rows; add: numeric order with out-of-order input, header format, role derivation, week-profile row routes to schedule |
| change | `assets/ui/tests/e2e/device-detail.spec.ts`, `channel-editor.spec.ts`, `channel-export-import.spec.ts`, `virtual-remote.spec.ts` — wherever a chip is located by its text, locate the row (`getByRole("row", { name: … })`) |
| baselines | any `*-snapshots` that show the device page (grep the spec files for `toHaveScreenshot`); refresh darwin locally, linux in the container |

Columns, in this order, all from the DTO unless noted:

| Column | Source | Notes |
|---|---|---|
| Nr. | `number` | numeric, default sort ascending, always the first column |
| Name | `name` | empty stays empty; the pencil for inline rename stays in the header below the table, not in the cell |
| Beschreibung | `type_label` | falls back to `type` |
| Typ | `type` | monospace |
| Rolle | `link_source_roles` / `link_target_roles` | Sender / Empfänger / beides / — |
| DPs | `data_points_count` | numeric |
| Verkn. | links count (lazy) | numeric; click → `configSub = "links"` |
| Status | `hidden`, `locked`, `number ≥ 50`, week-profile type, `group_no` | `Badge variant="muted"` chips, localised |

Header under the table:

```text
{type_label || type} ({model_label || model}, {t("device.channel_n", { n })})   0002EFGH1234:12
```

i18n keys (add to both `EN` and `DE`): `device.channels.col.number`,
`.name`, `.description`, `.type`, `.role`, `.datapoints`, `.links`,
`.status`; `device.channel.role.sender`, `.receiver`, `.both`, `.none`;
`device.channel.header` (`"{label} ({model}, {channel})"` — the channel
part is `device.channel_n`, which exists); `device.channel.chip.hidden`,
`.locked`, `.virtual` (reuse `device.virtual` if identical), `.week_profile`,
`.group`. Extend `i18n.catalog-coverage.test.ts` with one case listing
them.

Surfaces: none new — the table lives inside `device.configure.channels`
(`internal/north/ui/surface/surface.go:267`). MCP: covered by the backend
half.

### Acceptance

```sh
cd assets/ui && npm run check && npx vitest run src/routes/DeviceDetail src/lib/channel && npx playwright test tests/e2e/device-detail.spec.ts tests/e2e/channel-editor.spec.ts
go test ./internal/north/rest/handlers/ -run Channel -count=1 && make test
```

then the full gate, then `bash script/check_api_version_bump.sh origin/main`.

---

## Cut 5 — parameter editor as rows, control-driven widgets, write preview, read-back

**Branch:** `feat/parameter-table`
**Goal:** cards become rows; the widget follows `control` before `type`;
a preview shows what will be written; after the write, what the device
kept is compared with what was sent.

### Files

| Action | Path |
|---|---|
| change | `assets/ui/src/lib/components/channel/ParameterGrid.svelte` — sections (`<section>` + `<h4>`) each containing rows; the two-column grid goes; DST and time pairs render as rows inside their sections |
| change | `assets/ui/src/lib/components/parameter/ParameterField.svelte` — row layout `grid-cols-[minmax(14rem,1fr)_minmax(12rem,2fr)_auto]`: label with the raw name beneath (`font-mono text-xs`), widget, range/default column (`min … max unit · Standard: x`), dirty marker before the label; help collapses under the row (keep the existing ≤ 80-chars inline rule) |
| change | `ParameterTimePair.svelte`, `ParameterLevelField.svelte`, `DstSubgroup.svelte` — same row grid, so every row shares one baseline |
| change | `assets/ui/src/lib/control/resolver.ts` — export `widgetFor(param: UISchemaParameter): WidgetKind` (see table); `ParameterField` calls it first and falls back to today's heuristics when it returns `"auto"` |
| new | `assets/ui/src/lib/control/widget-for.test.ts` — table test, one row per `control` family |
| new | `assets/ui/src/lib/channel/write-preview.ts` — pure: `buildPreview(schema, values, serverValues, dirtyNames): PreviewEntry[]` (label, name, from, to as *display* values through the same multiplier/enum-label logic the field uses) and `readBackDiff(sent, reloaded): ReadBackEntry[]` |
| new | `assets/ui/src/lib/channel/write-preview.test.ts` |
| new | `assets/ui/src/lib/components/channel/WritePreviewDialog.svelte` — follows `ConfirmDialog.svelte`'s overlay/card pattern (the shared `confirmStore` only takes string bodies, so this is its own component, opened by a `$state` flag in `ChannelPanel`); shows the table, the request line (`PUT /api/v1/devices/<ch>/paramsets/MASTER` or `…/link-ps/<peer>`) and the JSON body in a `<pre>`; buttons *Abbrechen* / *Schreiben* |
| change | `assets/ui/src/lib/components/channel/ChannelPanel.svelte:573-612` — `save()` becomes `requestSave()`: if `prefs.writePreview && paramset !== "VALUES"` open the dialog, else write; the dialog's confirm calls the existing write path unchanged. After `load()`, compute `readBackDiff`; when non-empty, `toastStore.push("info", t("channel.readback.title"), …)` and mark those rows with a chip `Gerät meldet {value}` until the next edit of that row |
| change | `assets/ui/src/lib/stores/preferences.svelte.ts` — `writePreview: boolean` (default `true`) and `paramDensity: "compact" \| "comfortable"` (default `"compact"`), with `setWritePreview`, `setParamDensity`, loader defaults |
| change | `assets/ui/src/routes/Settings.svelte` — two controls in the display/preferences section, next to the expert-mode switch |
| change | `assets/ui/src/lib/i18n.ts` — keys below |
| change | `assets/ui/tests/e2e/channel-editor.spec.ts:147` — the FLOAT save now clicks through the preview; add one case with the preference off (set localStorage `openccu-loom.prefs.v1` in the fixture) |
| change | existing `ParameterGrid.*.test.ts`, `ParameterField.*.test.ts` — selectors, not behaviour |
| baselines | the device page snapshots again (darwin locally, linux in the container) |

Widget by `control` (first match wins; `"auto"` means today's heuristics):

| `control` prefix | Widget |
|---|---|
| `SWITCH.STATE`, `*.STATE` with type BOOL | switch |
| `DIMMER.LEVEL`, `BLIND.LEVEL`, `*.LEVEL` | slider + number, percent when `display_as_percent` or `multiplier === 100` |
| `BUTTON.SHORT`, `BUTTON.LONG`, any type ACTION | trigger button (existing `onAction`) |
| `NONE`, empty | `"auto"` |
| anything unknown | `"auto"` |

The table is a starting point: read `internal/model/generic/switch_to_sensor.go:29` and grep `Control` in `internal/model/custom/` for the forms the daemon actually emits, and extend the table from that — never from memory.

i18n keys: `channel.preview.title`, `.request`, `.body`, `.col.parameter`,
`.col.from`, `.col.to`, `.write`, `.nothing_to_write`;
`channel.readback.title`, `.body` (`"{count} Werte weichen vom
Gesendeten ab"`), `.chip` (`"Gerät meldet {value}"`);
`settings.prefs.write_preview`, `.write_preview_help`, `.param_density`,
`.density.compact`, `.density.comfortable`; `parameter.default` (`"Standard"`).
Both catalogues, plus a coverage-test case.

Read-back rationale for the PR description: rfd clamps out-of-range
values to MAX and answers `ok`; hmipserver stores rejected values. Both
were measured by the homematic-manager project against CCU firmware
3.89.8 (its `docs/config-pending.md`), not by this project — cite it as
an external measurement.

### Acceptance

```sh
cd assets/ui && npm run check && npx vitest run src/lib/channel src/lib/control src/lib/components/channel src/lib/components/parameter && npx playwright test tests/e2e/channel-editor.spec.ts
```

then the full gate.

---

## Cut 6 — device list: table only, column filters, expandable channels

**Branch:** `feat/device-table`
**Goal:** the card grid goes; the DataTable becomes the single layout
with a filter row under the header and a channel table per expanded
device row.

### Files

| Action | Path |
|---|---|
| change | `assets/ui/src/lib/components/ui/DataTable.svelte` + `data-table.ts` — `columnFilters?: boolean` (one `Input` per filterable column under the header, matched through `get()` with `makeTextMatcher`; `filter?: "text" \| "select" \| false` per column, `filterOptions?` for selects); `expand?: Snippet<[Row]>` + `expandable?: (row) => boolean` with an expanded set keyed by `rowKey` and a leading chevron cell; persist filters under `persistKey` beside sort/search |
| new | `assets/ui/src/lib/components/ui/DataTable.filters.test.ts`, `DataTable.expand.test.ts` |
| change | `assets/ui/src/routes/DeviceList.svelte` — remove `DeviceCard`, the grid branch, `listClass`, the sort-button toolbar (`:509-533`); keep the toolbar filters (availability, update-only, area, central, group-by-interface) and the bulk selection; the `expand` snippet renders `ChannelTable compact` from cut 4 with channels fetched lazily per device (find the channels call in `client.ts`; cache per address in component state) and row click → `#/devices/<addr>/channels/<n>` |
| delete | `assets/ui/src/lib/components/DeviceCard.svelte`, `DeviceCard.test.ts` (only `DeviceList` used it — re-grep before deleting) |
| change | `assets/ui/src/lib/stores/preferences.svelte.ts` — remove `deviceView` and `setDeviceView`; the loader ignores a stored value |
| change | `assets/ui/src/lib/stores/deviceListFilters.svelte.ts` — drop `sortColumn` / `sortAsc` (DataTable persists sort under its `persistKey`) |
| change | `assets/ui/src/lib/i18n.ts` — remove `devicelist.view_grid` / `view_list` (grep for other users first); add `devicelist.filter.<col>` placeholders and `devicelist.expand` / `collapse` aria labels |
| change | `assets/ui/src/routes/DeviceList.test.ts` — "renders device cards" cases become row cases; add filter-row and expansion cases |
| baselines | `visual.spec.ts-snapshots/device-list-*` (darwin locally, linux in the container); `doc-screenshots.spec.ts` renders `docs/user/img/` — container only |
| docs | `docs/user/` pages that show the device list (grep `device-list` / `devices.png` in `docs/`) get the new screenshot from the container run, same commit |

Surfaces: `nav.devices` unchanged.

### Acceptance

```sh
cd assets/ui && npm run check && npx vitest run src/routes/DeviceList src/lib/components/ui && npx playwright test tests/e2e/visual.spec.ts
```

then the full gate.

---

## What is deliberately out of scope

- Multi-apply of a MASTER paramset to several channels, and a staged
  change set with one *Anwenden* — both homematic-manager features worth
  having, both their own plan (multi-apply needs the identical-description
  eligibility rule or it bricks channels, per the external measurement
  above).
- The Python client fan-out for the `ChannelSummary` fields.
- Any change to `internal/model/custom/` profiles.

## Owner inputs still needed

1. Cut 2: the label wording (de and en) for the four channel-12 tokens
   `IGNORE_DOOR_OPEN`, `SKIP_HOLD_TIME_OPENING`, `SKIP_RELOCK_DELAY_CLOSING`,
   `SKIP_HOLD_TIME_OPENING_RELOCK_DELAY_CLOSING`; optionally for channel 2
   and 3 as well. The captures themselves are done (fixture in
   `notes/parity/fixtures/`).
2. Cut 3, only if neither reproducer goes red: a DevTools stack trace of
   the failing save from the real instance.
3. Cut 2: the go-openccu-data release after the overlay commit.
