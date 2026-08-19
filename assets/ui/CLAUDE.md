# CLAUDE.md — Config-UI SPA

Loaded when you touch `assets/ui/`. Repo-wide rules: root [`CLAUDE.md`](../../CLAUDE.md).

## Operating concept

The Svelte SPA has one consistent operating concept; match it when you
touch any view. Source the recurring surfaces from the shared
design-system in `assets/ui/src/lib/components/ui/` instead of
hand-rolling them:

- **Loading / empty / error** → the shared `LoadingState` /
  `EmptyState` / `ErrorState` components (never a bare `<p>`). The error
  surface always renders a localized `Error: …` with an optional retry.
- **Action results** (save / delete / create / run / restore) →
  `toastStore.success` / `toastStore.error`, never an inline header
  banner. A failure must surface — silent aborts are a bug.
- **Destructive actions** → the shared `confirmStore.ask({ …,
  destructive: true })` dialog; no hand-rolled modals, no unconfirmed
  deletes.
- **Primitives** → `Button` / `Input` / `Select` / `Card` / `Badge`
  over raw elements; every colour utility carries a `dark:` variant (or
  uses the theme-aware `--ha-*` CSS tokens, which already invert).
- Strings stay localized via `t(...)` (de + en in `lib/i18n.ts`).

**Full i18n and full theme support are mandatory for every SPA change
— no exceptions for new feature areas.**

- **i18n**: every user-visible string goes through `t(...)` with BOTH
  locales (`DE` + `EN`) filled in `assets/ui/src/lib/i18n.ts` — that
  includes button labels, toasts, confirm dialogs, empty/error states,
  badges, tooltips, `aria-label`s, document titles, and placeholder
  text. No hard-coded literals in markup or scripts. Config-schema
  fields additionally follow the `config.field.*`/`config.help.*` rule
  (see Critical Rules); everything else is reviewed, not guard-enforced
  — treat a missing locale entry like a failing test.
- **Themes**: every view must render correctly in **all four**
  combinations — skin `loom` and `ha` (`data-skin`) × light and dark
  mode. Use the theme-aware CSS tokens (which invert per mode and
  restyle per skin) or Tailwind `dark:` variants; never a raw colour
  that only works in one combination. New views add Playwright visual
  baselines for at least light + dark (see Testing Guidelines); skin
  parity is part of review.

UI patterns (session-based MASTER editing, undo/redo, dirty tracking,
preset selection) mirror `homematicip-local-frontend`; the operating
concept above is locked in by the Playwright e2e + visual suite
(see [`tests/CLAUDE.md`](../../tests/CLAUDE.md)).

---


## SPA browser-e2e + visual regression (`assets/ui/tests/e2e/`)

Playwright drives the real SPA in a headless Chromium and locks in the
homogeneous operating concept (navigation + document titles + skip-link,
the shared loading/empty/error states, toast feedback, the confirm
dialog) plus visual baselines of representative views in **both light
and dark mode**. The suite is hermetic: it serves the SPA via the Vite
dev server and **mocks every `/api/v1/*` call** (`tests/e2e/helpers/
mock-api.ts`), so no daemon or CCU is needed and screenshots are
deterministic. Run with `cd assets/ui && npm run e2e`; refresh baselines
with `npm run e2e:update`. CI (`.github/workflows/spa-e2e.yml`) runs it
inside the official `mcr.microsoft.com/playwright` container so
rendering matches — screenshot baselines are committed **per platform**
(`*-chromium-linux.png` for CI; macOS `-darwin` baselines coexist for
local runs). The component-level Svelte tests are the separate `vitest`
suite (`*.test.ts` under `assets/ui/src/`); keep both green.


## Every config field needs a label AND a help text in en + de

The SPA section editor renders one field per `cfg:`-tagged config leaf (the
list `config.ClassifyFields` feeds `GET /api/v1/config/schema`). Each field
**must** have BOTH an explicit label (`config.field.<path>`) and an inline-help
description (`config.help.<path>`) in **both** locales of
`assets/ui/src/lib/i18n.ts` (the `EN` and `DE` catalogues). Without the label
key the editor shows a machine-humanised, untranslated string; without the help
key the hint row is dropped silently — both read to operators as "field without
a description". When you add or rename a `cfg:` field, add all four entries.
This is enforced by `TestConfigFieldsHaveLabelsAndHelp` (in `tests/contract/`),
which fails the build listing every missing `EN`/`DE` × `field`/`help` entry —
so `make test` is the safety net, not manual review.

