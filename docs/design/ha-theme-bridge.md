# HA-native theme bridge

!!! info "Who this page is for"
    Contributors touching the Config UI theming layer
    (`assets/ui/src/app.css`, `assets/ui/src/lib/theme/`,
    `assets/ui/src/lib/stores/preferences.svelte.ts`) and reviewers
    checking a themed component change for skin correctness.

**Status:** Implemented. **Related:**
[Using the Web UI](../user/web-ui.md),
[ADR 0044 — single-port onboarding and HA Ingress auth passthrough](../adr/0044-single-port-onboarding-and-ha-ingress-auth.md).

## Summary

The Svelte SPA can render in two visual skins: the default **Loom**
teal/slate look, and an **HA** skin that matches Home Assistant's
design language. Standalone the operator picks the skin explicitly in
Settings → Appearance. Running inside Home Assistant's Ingress iframe
the SPA switches to the HA skin automatically and mirrors the
operator's *live* HA theme — including custom themes — via a
same-origin bridge, with a static HA-default fallback when the bridge
cannot reach the parent document. In every context the app shell
(sidebar, header, navigation) is structurally identical; only the
color/elevation tokens change.

## The two-axis model

Two independent attributes on `<html>` drive presentation:

- **Axis 1 — skin**: `data-skin="loom"` or `data-skin="ha"`.
- **Axis 2 — light/dark**: the existing `.dark` class, unchanged
  mechanism.

Every themed utility in the SPA reads from a single **consumption
layer** — the `--ha-*` custom properties defined in
`assets/ui/src/app.css` (`--ha-primary-color`,
`--ha-card-background-color`, `--ha-divider-color`,
`--ha-primary-text-color`, `--ha-secondary-text-color`, the semantic
status colors, and the radius/elevation tokens). Components never
branch on the skin directly; they consume `--ha-*` and let the active
`data-skin` / `.dark` combination resolve the value. This keeps the
primitive layer skin-agnostic and makes adding a third skin in the
future a CSS-only change.

## Three value sources

The same `--ha-*` token names resolve to different concrete values
depending on skin and context:

1. **`data-skin="loom"`** — the pre-existing `:root` and `html.dark`
   blocks in `app.css`. These are untouched by this work; the
   standalone default look is pixel-for-pixel unchanged.
2. **`data-skin="ha"`, standalone** — a static HA-default token set
   (`html[data-skin="ha"]` / `html[data-skin="ha"].dark` blocks in
   `app.css`), literal fallback values matched to the latest
   `home-assistant/frontend` design tokens (e.g.
   `--ha-primary-color: var(--primary-color, #009ac7)`, flat
   shadow-less cards, Roboto as the body font).
3. **`data-skin="ha"`, embedded** — the same declarations, but the HA
   theme bridge (below) writes HA's *real* CSS variables
   (`--primary-color`, `--card-background-color`, …) onto the SPA's
   own `:root` before the tokens resolve, so `var(--primary-color,
   #009ac7)` picks up the live value and the static literal is only
   ever a fallback of last resort.

## The HA theme bridge

`assets/ui/src/lib/theme/ha-bridge.ts` implements the runtime side:

- `isEmbedded()` — `true` when `window.self !== window.top`, i.e. the
  SPA is running inside HA's Ingress iframe.
- `resolveSkin(stored)` — forces `"ha"` when embedded, regardless of
  the operator's stored preference; otherwise returns the stored
  choice.
- `startHaBridge()` — the mirroring loop. It is a no-op outside an
  iframe. Inside an iframe it wraps every parent-document access in
  `try`/`catch`: a cross-origin parent (Ingress is same-origin in
  practice, but the code does not assume it) throws on
  `window.parent.document` access, and the bridge falls back silently
  to the static HA-default tokens from `app.css`.

  When the parent is reachable, the bridge:

  - Reads the parent's computed style
    (`getComputedStyle(window.parent.document.documentElement)`) and
    copies each variable in the `HA_THEME_VARS` list (HA's primary/
    accent/background/text/divider/status colors, card radius and
    shadow, header and sidebar colors) onto the SPA's own root via
    `style.setProperty`, skipping empty values.
  - Determines light vs. dark by relative luminance of HA's
    `--primary-background-color` (WCAG relative-luminance formula over
    the parsed RGB triple): luminance below 0.5 adds the `.dark` class,
    otherwise it is removed. This **overrides** the SPA's own
    light/dark preference while embedded — the SPA tracks HA's theme,
    not the operator's Loom-side toggle.
  - Installs a `MutationObserver` on the parent document's root
    element (watching `style` and `class` attribute changes) so a live
    theme edit or a manual dark-mode toggle in HA re-syncs the SPA
    within one repaint. It also re-runs the copy on window `focus` and
    `visibilitychange`, catching theme changes made while the iframe
    tab was not visible.
  - Returns a cleanup function that disconnects the observer and
    removes the listeners; `App.svelte` calls `startHaBridge()` on
    mount and calls the returned cleanup on teardown.

Because the bridge only ever *reads* the parent's computed style and
*writes* to its own document, it works for any HA theme — built-in or
custom — without the daemon needing to know which theme is active.

## Preferences, bootstrap, and the pre-paint script

- `assets/ui/src/lib/stores/preferences.svelte.ts` persists a `skin:
  "loom" | "ha"` preference (default `"loom"`) alongside the existing
  `theme` preference. `applyTheme()` sets `document.documentElement
  .dataset.skin` from `resolveSkin(prefs.skin)`; when embedded it
  deliberately skips its own `.dark` toggle so `startHaBridge` owns
  light/dark without the two fighting each other. `setSkin(skin)`
  updates the preference, persists it, and re-applies the theme.
- `App.svelte` calls `startHaBridge()` once on mount (alongside the
  existing `applyTheme()` / system-theme wiring) and keeps its cleanup
  for teardown.
- The inline pre-paint script in `assets/ui/index.html` mirrors the
  `data-skin` decision before first paint — reading the persisted
  preference from `localStorage` synchronously, and forcing `"ha"`
  when `window.self !== window.top` — so the app shell never flashes
  Loom-teal before the Svelte store and the bridge run. It is
  deliberately tiny, dependency-free, and wrapped in `try`/`catch`.

## Structure is identical in every context

The sidebar, header, and navigation are the same component tree
whether the SPA is standalone or embedded — there is no
embedded-specific layout. The **only** embedded-specific delta is the
document `<title>`: `App.svelte`'s `<svelte:head>` renders no `<title>`
element when `isEmbedded()` is true, so Home Assistant's own Ingress
panel title is left in place instead of being overwritten on every
route change.

## Palette selector

Settings → Appearance gains a "Design" control (`settings.appearance
.design` and related i18n keys, English + German) offering
"OpenCCU-Loom" and "Home Assistant" via `setSkin(...)`. When
`isEmbedded()` is true the control is rendered disabled with an
inline hint that Home Assistant drives the theme automatically — the
operator's stored choice is not consulted while embedded, so exposing
an editable control there would be misleading.

## Complete theme coverage via palette remap

The scope boundary above described the state right after the
two-axis skin landed: only the primitives and highest-traffic views
had been hand-migrated to the `--ha-*` consumption tokens, and the
long tail of plain Tailwind `slate-*`/`red-*`/`amber-*`/`green-*`/
`emerald-*`/`brand-*` utilities elsewhere in the SPA stayed
slate-neutral under the HA skin. That gap is now closed **without a
per-file sweep**.

Tailwind v4 compiles a class like `bg-slate-500` to
`background-color: var(--color-slate-500)` rather than inlining the
literal color — the palette is itself a set of CSS custom properties
(`--color-slate-50` … `--color-brand-950`, defined once in the
`@theme` block of `app.css`). `html[data-skin="ha"]` in `app.css`
overrides that entire `--color-*` scale (neutral/slate, error/red,
warning/amber, success/green + emerald, and the brand/violet ramps)
with values derived from the same HA tokens the `--ha-*` layer
consumes. The remap is a single selector block, has no `.dark`
variant of its own, and does not touch `--color-white` /
`--color-black` (kept literal for on-primary text). Because every
plain palette utility resolves through these variables, the whole
SPA — including views that were never touched by the original
migration — now follows the active skin automatically. `data-skin=
"loom"` is untouched: the remap block only exists under
`html[data-skin="ha"]`, so the standalone default look is unaffected.

One class of utility does **not** follow the remap automatically:
Tailwind inlines opacity-modified palette utilities (`bg-slate-900/50`
compiles to a literal `#1c1c1c80`-style hex, not a `var()` reference),
so those bypassed the override. Each of the SPA's opacity-modified
palette utilities was rewritten to reference the (now remapped)
`--color-*` variable through `color-mix()` instead, e.g.
`bg-slate-900/50` → `bg-[color-mix(in_srgb,var(--color-slate-900)_50%,transparent)]`.
This preserves the original opacity while making the underlying color
skin-aware.

**Boundary:** the palette remap is intentionally **static per skin**,
not bridged. `ha-bridge.ts` continues to mirror only the `--ha-*`
semantic tokens from HA's live (including custom) theme for
first-class fidelity on the primitives and primary views; the
`--color-*` palette scale under `data-skin="ha"` always uses the
refreshed HA-default literals (or their `var(--primary-color, …)`-style
fallback chain for the handful that also read an HA variable), even
when embedded inside a customized HA theme. This keeps the remap cheap
and predictable — it never needs a `MutationObserver` re-run — at the
cost of the deep-tail utilities not tracking a live custom HA theme as
precisely as the `--ha-*`-consuming primitives do.

## Scope boundary

This work retthemes the shared UI primitives
(`assets/ui/src/lib/components/ui/`) and the highest-traffic views —
the surfaces an HA-embedded operator sees most: navigation shell,
cards, buttons, inputs, badges, and focus rings, all migrated from
hard-coded Tailwind slate/brand utilities to the `--ha-*` consumption
tokens. Beyond that hand-migrated set, the palette-remap block
described above now recolors every other plain Tailwind palette
utility in the SPA automatically, so there is no remaining
slate-neutral long tail under the HA skin — see "Complete theme
coverage via palette remap".
