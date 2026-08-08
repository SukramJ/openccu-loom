# UI surface profiles — operator-configurable navigation, per operating mode

- **Status**: Implemented in 0.55.0
- **Date**: 2026-08-08
- **Scope**: `openccu-loom` — `internal/config`, `internal/north/rest`, `assets/ui/`
- **Related**:
  [Embedded UI mode](./embedded-ui-mode.md),
  [ADR 0045 — login and onboarding into SPA](../../docs/adr/0045-login-and-onboarding-into-spa.md),
  [ADR 0051 — northbound authorization model](../../docs/adr/0051-northbound-authorization-model.md),
  [ADR 0054 — remote ingress proxy add-on](../../docs/adr/0054-remote-ingress-proxy-addon.md)

---

## ⭐ Recommendation (TL;DR)

> Generalise the `embedded` switch into **two named surface profiles** — `standalone` and
> `embedded` — that an admin can edit in the SPA under
> **Settings → Navigation & views**.
>
> - A **master toggle** decides which profile is live. Everything else is per-profile
>   configuration, and **the inactive profile is editable too**, so an operator can prepare
>   the Home Assistant layout before switching to it.
> - Each profile stores **sparse overrides** against a shipped default, never a full list.
>   A view added in a later release arrives with the default its PR assigned it, instead of
>   being silently absent because it was missing from a frozen snapshot.
> - Every row shows **its shipped default** and marks itself when it deviates. Reset is
>   available **per row** and **per profile**.
> - A small set of surfaces is a **floor: never hideable, enforced on the server** — the
>   device list, Settings, this editor, the About page, and (in `standalone` only) user and
>   token administration. Without that floor an admin can hide the way back to the switch
>   they just flipped.
> - **In `embedded` mode the profile also carries the write boundary.** A surface hidden
>   there is refused for the Home Assistant passthrough identity as well, and one the admin
>   deliberately shows becomes writable again — so an operator who prefers Loom's paramset
>   editor to the HA panel gets a working editor, not a dead one. Everywhere else hiding is
>   pure navigation: it never restricts an API token, a Loom account, or MQTT.

---

## 1. Why make it configurable

The [embedded UI mode](./embedded-ui-mode.md) needs exactly one thing from the operator: a
declaration that Home Assistant owns this daemon's config surface. Once that switch exists,
two arguments make it worth generalising rather than hard-coding the hidden set:

1. **The shipped split will be wrong for someone.** The embedded ownership matrix is a good
   default, not a law. An operator who runs Loom's Matter bridge deliberately behind HA
   wants `/matter` back; one who never uses HmIP groups wants the entry gone from both
   modes.
2. **Standalone deployments have the same need, without any HA involved.** A wall tablet
   showing only devices and the alarm panel; a household that never armed the alarm system
   and finds the panel confusing; a shared workshop instance where the audit log is noise.
   Today the only answer is "ignore the entry".

The trap to avoid is presenting this as a permission system. It is not: hiding a view
removes the door, not the wall. That distinction has to be stated in the UI itself, once,
where nobody can miss it — §3.2.

## 2. Model

### 2.1 Three tiers of preference — keep them distinct

The SPA already has two preference tiers, and this adds a third. Confusing them is the
easiest way to build the wrong thing:

| Tier | Stored | Scope | Examples |
|---|---|---|---|
| Device-local | `localStorage` (`lib/stores/preferences.svelte.ts`) | this browser | theme, skin, locale, nav collapsed, expert mode, device view |
| Per user | server, preferences key (`lib/stores/startRoute.svelte.ts`) | follows the user across devices | start page |
| **Daemon policy (new)** | server, config section `north.ui` | **every user of this daemon** | which surfaces exist at all |

The new tier is admin-only and daemon-wide. The editor states that in one line, because
"I hid it for me" versus "I hid it for everyone" is the kind of misunderstanding that ends
in a support thread.

### 2.2 What a *surface* is

A surface is an addressable entry point, identified by a stable string:

- `nav.<key>` — a navigation item (`nav.devices`, `nav.alarm`, `nav.matter`)
- `settings.<tab>` — a settings tab (`settings.mqtt`, `settings.users`)
- `device.<tab>` — a device-detail tab or configure sub-tab (`device.configure`,
  and its four children `device-config`, `channels`, `links`, `schedule`)

The IDs are derived from the tables that already exist — `navClusters` in
`assets/ui/src/lib/nav.ts:75-259`, `ALL_TABS` in `assets/ui/src/routes/Settings.svelte:243-261`,
and the top/sub tabs in `DeviceDetail.svelte:53-63`. Deriving rather than duplicating is the
point: a second hand-maintained list drifts on the first new view, which is exactly the
failure mode `nav.ts:4-13` already warns about for the start-route selector.

Not surfaces, deliberately: individual buttons, single config fields, entity-level
filtering. Field-level control already exists (`cfg:"expert"` + expert mode), and
data-point filtering is `settings → visibility` ("Hidden parameters") — a different
mechanism for a different object. This feature only ever hides *navigation*.

### 2.3 Profiles

```yaml
north:
  ui:
    embedded: false          # master toggle — which profile is live
    profiles:
      standalone:
        nav.alarm: hidden    # sparse: only deviations from the shipped default
      embedded:
        nav.matter: visible
```

Two profiles, fixed names, no user-defined profiles. A third mode has no meaning today —
"who owns the config surface" has exactly two answers — and every additional profile
doubles the review surface of the default table.

**Sparse by construction.** An absent key means "shipped default", so a new view in 0.56
appears with the default its PR chose, in both profiles, without touching anyone's stored
config. A full-snapshot format would freeze the surface list at the moment of the first
edit and every later view would be invisible until an admin noticed.

### 2.4 Resolution order

```
visible := shippedDefault(surface, profile)
           |> override(profile)          // operator's stored deviation
           |> floor(surface, profile)    // forced visible, never overridable
           && capabilityGate(surface)    // history.v1, matterEnabled, alarm mounted …
           && roleGate(surface)          // adminOnly
```

The floor sits **inside** the policy, the gates **outside** it. Making a surface visible can
never conjure a view whose feature is switched off: `nav.matter` set to `visible` while the
Matter bridge is disabled stays absent. The editor shows that as its own row state
(§3.3) rather than silently ignoring the operator.

The same resolved value drives the write boundary in `embedded` mode — see §2.8. One
resolution, two consumers; never two tables.

### 2.5 The floor — surfaces that can never be hidden

| Surface | Floor in | Why |
|---|---|---|
| `nav.devices` | both | The device list is what the application *is*. A config UI with no devices is not a reduced UI, it is a broken one. |
| `nav.settings` | both | Hiding Settings removes every path to every switch, including this one. |
| `settings.navviews` | both | Hiding this editor is the one-way door: the profile can then only be repaired through YAML or the REST API. |
| `nav.about` | both | The only in-app statement of version, build and add-on stamp. Every support conversation starts there — and a UI that cannot say what it is costs more than the sidebar row it saves. |
| `settings.users`, `settings.tokens` | `standalone` only | In standalone, Loom's user database is the *only* identity store: hiding it strands the operator when a password or token has to be rotated. In `embedded` HA owns identity, so both are hidden by default instead. |

Two properties make this a floor rather than a suggestion:

- **The server enforces it.** A `PUT` that hides a floor surface is rejected with a
  problem-details 422 naming the surface; the resolver additionally ignores such an override
  if one ever reaches it from a hand-edited YAML. A rule with only a disabled switch behind
  it is decoration — CLAUDE.md's phrase for it, and it applies here.
- **The floor is profile-aware**, so `embedded` keeps its ability to hide identity admin
  without weakening standalone.

### 2.6 Guarded, not forbidden

Three hides are legal but consequential, so they ask once, in the confirm dialog, with the
consequence spelled out — and only when the condition actually holds:

| Hiding | Condition | Warning |
|---|---|---|
| `nav.alarm` | alarm system armed or arming | "The alarm system is currently armed. With the panel hidden there is no way to disarm it from this UI — MQTT, REST and the HA integration keep working." |
| `nav.security` | unacknowledged faults present | "3 Security & Safety faults are unacknowledged and can only be acknowledged here." |
| `settings.ccus` | `standalone` profile | "New CCUs can then only be added by editing the config file or calling the REST API." |

The dialog is the shared `confirmStore.ask` — it is a consequential action, not a
destructive one, so it is not styled `destructive: true`.

### 2.7 Shipped defaults

`standalone` ships **everything visible** — the current behaviour, so upgrading changes
nothing. `embedded` ships the ownership matrix of the [embedded UI mode](./embedded-ui-mode.md) §3:

| Surface group | Hidden by default in `embedded` |
|---|---|
| Navigation | `nav.overview`, `nav.favorites`, `nav.energy`, `nav.diagrams`, `nav.matter` |
| Settings | `settings.ccus`, `settings.oidc`, `settings.ccu_auth`, `settings.users`, `settings.groups`, `settings.tokens`, `settings.matter` |
| Device detail | `device.configure` (with its `device-config` / `links` / `schedule` sub-tabs) |

Everything else stays visible, including `nav.devices`, `nav.alarm`, `nav.security`,
`nav.inbox`, `nav.fleet`, the whole automation cluster, the ops cluster and
`device.history`.

> **One correction to the embedded design.** Its §4.2 lists "`/about` branding" among the
> hidden items. Under this model `nav.about` is floor — it stays reachable in embedded mode
> and merely renders without the marketing header. Hiding the version banner is a rendering
> decision, not a surface decision.

### 2.8 Write enforcement follows the profile, in `embedded` mode only

The embedded design refuses writes on the surfaces Home Assistant owns (ADR 0051). That
refusal is **bound to the profile entry, not to the mode**: a surface hidden in the live
`embedded` profile refuses its writes, and a surface the admin deliberately shows accepts
them again.

The reason is that "HA owns the paramset editor" is a *duplication* statement, not a safety
one. An operator who prefers Loom's editor — export/import, copy-to-channel, the fleet-wide
link list — should get a working one rather than an editor that renders and then fails on
save. Tying both to one switch keeps the two answers from contradicting each other.

Four boundaries keep that from becoming a hole:

1. **It applies to the Home Assistant passthrough identity only** — the identity minted by
   the Ingress auth passthrough (ADR 0044). A Loom account, an API token and the Remote
   add-on's injected per-instance token (ADR 0054) carry the rights they were granted; a
   navigation switch never widens or narrows those. Otherwise an admin could grant a
   machine client rights by un-hiding a sidebar entry.
2. **It applies in `embedded` mode only.** In `standalone` the profile is purely
   navigational. There is no passthrough identity there to scope.
3. **Not every surface carries writes.** Only surfaces with a declared write-route set
   participate: the device configure tab and its sub-tabs, CCU administration, the identity
   tabs, and Matter. Hiding `nav.energy` gates nothing — it has no write path.
4. **Every change is audited.** Because the table now decides an authorization outcome, the
   audit row for a profile change is load-bearing, not cosmetic: "who re-enabled paramset
   writes for the HA identity, and when" has to be answerable.

The consequence to state plainly, in this document and in the UI: **in `embedded` mode a
visibility switch is also an authorization switch.** The editor marks exactly those rows
(§3.3) instead of relying on the operator to remember which ones they are.

## 3. The editor

### 3.1 Where it lives, and what it is called

A new settings tab **`navviews`** in the existing `general` group
(`Settings.svelte:276-282`), admin-only, labelled:

- EN **"Navigation & views"**
- DE **"Navigation & Ansichten"**

Not "Interface" (the word means CCU interfaces everywhere else in this product) and not
"Visibility" (taken by hidden *parameters*). Naming discipline here is worth more than a
shorter label.

### 3.2 Anatomy

```
Settings › Navigation & views                                       ⓘ Admin only

┌──────────────────────────────────────────────────────────────────────────────┐
│  ⓘ  Hiding a view removes it from the navigation for every user of this      │
│     daemon. API tokens, Loom accounts and MQTT are unaffected — restrict     │
│     those through roles and tokens.                             Learn more → │
│     In embedded mode, rows marked ⇄ also decide whether Home Assistant may   │
│     write to that surface.                                                   │
└──────────────────────────────────────────────────────────────────────────────┘

┌─ Operating mode ─────────────────────────────────────────────────────────────┐
│                                                                              │
│   Embedded in Home Assistant                                    [ ●━━  OFF ] │
│                                                                              │
│   Turn this on when Home Assistant owns this daemon's config surface — the   │
│   Homematic(IP) Local integration is configured against *this* daemon.       │
│   Loom then hides what HA already owns (login, CCU credentials, paramset     │
│   and link editors) and refuses the matching writes.                         │
│                                                                              │
│   Live profile: **Standalone** · 26 of 26 views visible · no changes         │
└──────────────────────────────────────────────────────────────────────────────┘

  Editing profile   ┃ Standalone ● live ┃  Embedded            ┃   4 deviations
                    ┗━━━━━━━━━━━━━━━━━━━┛                       ⟲ Reset profile

  🔍 Find a view…          [ All ] [ Visible ] [ Hidden ] [ Changed from default ]

┌─ Views ─────────────────────────────────────┐ ┌─ Preview ────────────────────┐
│                                             │ │  Overview                    │
│  Overview                    6 of 7 visible │ │   ▸ Devices                  │
│  ─────────────────────────────────────────  │ │   ▸ Alarm system             │
│   Overview                          [ ON  ] │ │   ▸ Security & Safety        │
│   Tiles for every device, grouped by room.  │ │   ▸ Inbox                    │
│                                             │ │   ▸ Fleet                    │
│  🔒 Devices                         [ ON  ] │ │                              │
│   The device list and every device detail.  │ │  Automation                  │
│   Cannot be hidden — the UI needs it.       │ │   ▸ Programs                 │
│                                             │ │   ▸ System variables         │
│   Favorites                         [ OFF ]•│ │   ▸ Device groups            │
│   Your starred devices and channels.        │ │   ▸ Direct links             │
│   Changed · default: visible          ⟲     │ │                              │
│                                             │ │  Diagnostics                 │
│   Alarm system                      [ ON  ] │ │   ▸ Service messages         │
│   Arming, zones, sensors and sirens.        │ │   ▸ Diagnostics              │
│                                             │ │   ▸ Signal quality           │
│   Security & Safety                 [ ON  ] │ │   ▸ Change history           │
│   Smoke, water and tamper classes.          │ │                              │
│                                             │ │  System                      │
│  ⚑ Matter                        [ ON  ]    │ │   ▸ Firmware                 │
│   Bridge Homematic devices to Matter.       │ │   ▸ Backups                  │
│   Unavailable — the Matter bridge is off.   │ │   ▸ Settings                 │
│   Enable it in Settings → Matter →          │ │   ▸ About                    │
│                                             │ │                              │
│  … Automation · Diagnostics · System …      │ │  Preview reflects unsaved    │
│  … Settings tabs · Device detail tabs …     │ │  changes.                    │
└─────────────────────────────────────────────┘ └──────────────────────────────┘

┏━ 3 unsaved changes ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━[ Discard ]  [ Save ]━━┓
```

Reading order is deliberate: **what this is not** (the disclaimer) → **which mode** (the
master toggle) → **which profile am I editing** → **the rows**. The preview answers the
question every toggle raises ("what will the sidebar look like?") without a save.

### 3.3 Row states

Every row carries a label, a one-sentence description, and a switch. Five states cover
everything:

| State | Marker | Switch | Sub-line |
|---|---|---|---|
| Default | none | live | the description alone |
| Changed | `•` dot next to the switch | live | "Changed · default: visible" + a `⟲` reset-this-row button |
| Floor | 🔒 before the label | disabled, forced on | "Cannot be hidden — *reason*" |
| Unavailable | ⚑ dimmed row | live (pre-configuration is allowed) | "Unavailable — *the gate*" + a link to the switch that enables it |
| Role-gated | dimmed label | live | "Only visible to admins" |
| Write-gated (`embedded` profile only) | `⇄` badge after the label | live | "Also decides whether Home Assistant may write here" |

Two decisions inside that table are worth naming. **Unavailable rows stay editable**: an
operator setting up a profile for a bridge they are about to enable should not have to
enable it first, and the resolution order (§2.4) guarantees the setting cannot leak a view.
**Floor rows are shown, not omitted** — a row that silently disappears teaches nothing,
while a locked row with a reason teaches the model.

The `⇄` badge is not decoration: in the `embedded` profile those rows change an
authorization outcome (§2.8), so showing one costs a confirm — "Home Assistant will be able
to write to this surface again. The duplicate editor problem comes back with it: the same
paramset is then editable in two panels with different session assumptions." Hiding one
needs no confirm; it only narrows.

Two labels need disambiguation, because the same word means two things:

- `nav.groups` → **"Device groups (HmIP)"**, DE "Gerätegruppen (HmIP)"
- `settings.groups` → **"Rooms & Functions"**, DE "Räume & Gewerke" — the tab
  id says `groups`, but it renders `RoomsFunctionsAdmin`. Verified against the
  code rather than assumed from the id: the first draft of the registry read it
  as user-group administration and classified its write routes accordingly.

### 3.4 Interactions

- **Editing the inactive profile.** The profile switcher is a segmented control; the live
  one carries a `live` badge. Editing the other one is normal and encouraged — prepare the
  HA layout, then flip the master toggle. The preview follows the *edited* profile and says
  so ("Preview: Embedded profile — not currently live").
- **Flipping the master toggle** is a confirm, not a silent write, because it changes what
  every user of the daemon sees on their next navigation. The dialog quantifies it: "14
  views and 7 settings tabs will be hidden. Device editors become read-only for the HA
  passthrough identity." It saves immediately (it is a mode, not part of the row diff).
- **Group headers** carry an `n of m visible` counter and a group-level "hide all / show
  all" that skips floor rows.
- **Reset per profile** (`⟲ Reset profile`) clears every override in the edited profile
  back to the shipped default. It only changes editor state; the dirty bar still has to be
  saved. That keeps it undoable with `Discard`.
- **Reset per row** (`⟲` on changed rows) is the fine-grained twin.
- **Save / Discard** is a sticky bottom bar with the change count — the pattern the config
  sections already use — and reports through `toastStore`, never an inline banner.
- **Search and filters** operate on label + description text; "Changed from default" is the
  review filter an operator wants before saving, and the one a support request should ask
  for.

### 3.5 Mobile

The preview column drops below the list under `md`, collapsed behind a "Preview navigation"
disclosure. The sticky save bar stays. Group headers become sticky section headers while
scrolling, so the counter remains visible.

### 3.6 Copy

Descriptions are one sentence, present tense, describing *what the view does* — not what
hiding it means. In the `embedded` profile each row that HA owns gets a second, muted line:
"Home Assistant provides this." That single line is what turns the default set from
arbitrary into obvious, and it is also the honest answer to "why is this off?".

All strings go through `t(...)` with EN + DE filled — that is a hard rule for SPA work, and
here it doubles as the surface registry's completeness check (§5).

### 3.7 Accessibility

Each row is a labelled switch (`aria-describedby` pointing at the description), floor rows
use `aria-disabled` with the reason in the accessible name, group toggles are buttons with
`aria-pressed`, and the preview column is `aria-live="polite"` so a screen reader hears the
navigation change as it is edited. Keyboard: the filter chips are a radio group, the profile
switcher a tablist.

## 4. What happens to someone standing on a hidden view

Three cases, all of which the SPA already has machinery for:

1. **Open right now.** The surfaces payload arrives over the existing WebSocket
   config-change broadcast; the router redirects to the first visible view and a toast says
   "This view was hidden by an administrator." No silent blank page.
2. **Bookmarked or deep-linked.** Same redirect path the folded routes use
   (`foldedRouteTarget`, `nav.ts:267-284`, applied in `App.svelte:78`, `:104`, `:153`).
3. **Stored as a start page.** `startRoute.resolve()` must validate against the effective
   set — but only once it has resolved, honouring the boot-race rule already documented at
   `nav.ts:307-326`. Falling back to the default landing route is correct here; stranding
   the operator on an empty page is not.

## 5. Persistence, API, transport

- **Persistence**: the `north.ui` config section — `embedded *bool` plus
  `profiles map[string]map[string]string`. DB-tier via `configstore`, so it is editable at
  runtime, with YAML remaining authoritative for operators who prefer files.
- **Read**: `GET /api/v1/ui/surfaces` → the live mode, both profiles' overrides, and for
  every surface: `id`, `group`, `defaults{standalone,embedded}`, `floor`, `available`,
  `unavailableReason`, `roleGated`. One request at boot, next to `GET /api/v1/info`. The
  effective map is included so the navigation needs nothing else.
- **Write**: `PUT /api/v1/ui/surfaces` with `{mode?, profiles?}`, sparse. Floor violations →
  422 problem-details naming the offending surface. Audited like every other config write
  (`internal/audit`), which is what makes "who hid the alarm panel?" answerable.
- **API version**: adding paths bumps `APIVersion` in `info.go` and lands in
  `assets/openapi.yaml` first — spec-driven, per CLAUDE.md.

## 6. Guards

Each rule below names the test that keeps it true, because this document's own ratchet is
worth exactly as much as its enforcement:

| Rule | Guard |
|---|---|
| Every nav item, settings tab and device tab has a surface entry with both defaults | `TestEverySurfaceIsRegistered` (`tests/contract/`) — parses `nav.ts`, `Settings.svelte` and `DeviceDetail.svelte` and fails in both directions, so a new view fails the build until classified and a removed one cannot leave a dead id behind |
| Every surface has EN + DE label *and* description | `TestSurfaceCopyIsComplete` (`tests/contract/`), modelled on `TestConfigFieldsHaveLabelsAndHelp` |
| The floor is the documented set | `TestFloorSurfacesAreTheDocumentedSet` (`tests/contract/`) — changing it is a decision, so it changes there too |
| Floor surfaces cannot be hidden | `TestPutUISurfacesRejectsFloorHide` (handler, 422) **and** `TestResolveRefusesFloorOverride` (resolver), which ignores a hand-edited YAML override the API never saw |
| Sparse storage: no entry that repeats today's default | `TestNormalizeDropsRedundantEntries` (resolver) and `TestPutUISurfacesStoresSparsely` (handler) |
| A downgrade boots on a profile written by a newer release | `TestResolveIgnoresUnknownIDs` |
| The write rules name routes the router actually serves | `TestSurfaceWriteRoutesExist` (`tests/contract/`) — the declared-vs-served shape from CLAUDE.md: a rule for a moved route is a refusal that silently stopped happening |
| A surface hidden because HA owns it actually gates something | `TestHAOwnedWriteSurfacesAreGated` (`tests/contract/`) — the other direction, with an explicit no-write-path list rather than silence |
| Every write-gated surface owns rules, and only those | `TestEveryWriteGatedSurfaceHasRoutes` (resolver) |
| Only the passthrough identity is scoped by the profile | `TestSurfaceWritesScopesOnlyTheIngressIdentity` — the same write with a bearer token, a session and no identity passes untouched |
| Reads are never refused | `TestSurfaceWritesNeverGatesReads` |
| Standalone refuses nothing | `TestSurfaceWritesLeavesStandaloneAlone` |
| A saved profile is in force immediately | `TestSurfacePolicyUpdateTakesEffectImmediately` (middleware) plus the `policy.Set` wiring pin |
| The policy reaches the middleware through the composition root | `tests/contract/wiring_pins/surface_policy_wiring_test.go` — four pins: constructed, handed to the router, mounted, refreshed on save |
| The e2e fixture still describes the registry | `TestE2ESurfaceFixtureMatchesRegistry` (`tests/contract/`) — a stale fixture would lock every visual baseline onto a navigation nobody sees |
| The navigation, the settings tabs and the editor behave | `surfaces.test.ts` + `nav.test.ts` (vitest) and `settings-navviews.spec.ts` (Playwright) |
| Changing a profile is audited, naming the resulting profile | `TestPutUISurfacesIsAudited` (handler) — an entry that only said "north.ui changed" could not answer who handed a write back |

## 7. Risks and open questions

- **The support trap.** An operator hides a view, forgets, and reports the feature missing.
  Mitigation: active overrides are listed in `/about` and included in the diagnostics
  bundle, so the first support question answers itself.
- **Visibility read as permission — and in one mode it *is* one.** The banner has to carry a
  nuance rather than a slogan: everywhere except the write-gated rows of the embedded
  profile, hiding is pure navigation. That nuance is the price of §2.8, and the `⇄` badge is
  what keeps it teachable. If operators still conflate the two, the fallback is to show the
  write-gated rows in a separate section of the embedded profile rather than inline.
- **The embedded profile is now security-relevant configuration.** A profile that once only
  shaped a sidebar can widen what Home Assistant may write. Two consequences follow: the
  audit row for a profile change matters (§2.8), and a restore of an old profile from a
  backup restores an authorization decision with it — worth calling out in the backup
  documentation.
- **Two admins, two browsers.** Concurrent edits to the same profile last-write-wins today.
  A version field on the payload with a 409 is the cheap fix, matching how config sections
  behave.
- **Per-role layouts are out of scope, decided.** "Hide the audit log for non-admins" is a
  role question and belongs in roles. This feature stays daemon-wide: the existing
  `adminOnly` gates keep working on top of it (§2.4), and if per-role surfaces are ever
  wanted they extend the *role* model and read this table rather than forking it. Profiles ×
  roles would multiply the default table and make the permission confusion above almost
  unavoidable.
