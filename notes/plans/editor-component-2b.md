# #1067 step 2b — the editor as a web component (loom side)

Working plan for the branch `feature/editor-component`. Decisions:
[ADR 0089](../../docs/adr/0089-editor-component-transport.md); measurements in
the #1067 comments of 2026-11-12 and 2026-11-17.

Owner decisions (2026-11-17): one PR for all of 2b; the daemon reports the
bundle through its own manifest route (`GET /api/v1/ui/editor-component` →
`{module_url, hash, routes[]}`, module under `/app/component/editor-<hash>.js`,
no session needed). The `--ha-*` tokens get `:host` forms in go-fabric-ui
(SukramJ/go-fabric-ui#72); the component build waits for that release.

## State (2026-11-17)

Built on `feature/editor-component`; ADR 0089 carries a "Settled by the build"
section. What exists:

- `assets/ui/src/lib/host/` — the host seam (`host()`, `installHost`); the page
  host is the default, so the SPA needs no installation step.
- `assets/ui/src/component/` — `install-host.ts` (imported first; in-memory
  storage, surface set = configure + history, route-list enforcement),
  `editor-element.svelte.ts` (`<loom-device-editor>`), `EditorRoot.svelte`
  (mounts `routes/DeviceDetail.svelte` itself), `editor-routes.json`.
- `vite.component.config.ts` → `spa_dist/component/{editor.js,routes.json}`;
  `npm run build` builds both.
- Daemon: `internal/north/ui/editor_component.go`, `GET
  /api/v1/ui/editor-component` (API 16.5.0), DTO `hmapi.EditorComponentManifest`.
- New surface `device.actions` (header lifecycle actions).
- Tests: `tests/e2e/editor-component.spec.ts` (same save scenario on SPA route
  and component; contract tests bite-proven: portal, `@property`, dark frame,
  route list, own request, navigation), `tests/e2e/editor_component_test.go`
  (Go, built binary), unit tests.
- Found on the way: hidden `device.overview` still rendered (fixed with
  `activeTopTab`), `DeviceDetail` relied on the sidebar for `infoStore`,
  `OverflowMenu` outside-click used `e.target`.

Open:

- Acceptance "against the simulated CCU in a test host page": the browser suite
  is mocked; no CI job runs a browser against a daemon + godevccu. Owner
  decision.
- Theme tokens wait for go-fabric-ui#72 (expected-failure test in the spec).
- Relay central naming and audit actor (ADR 0089 "Still open") — for 2c / the
  integration.
- Route prefix `/devices` also covers the lifecycle routes the component hides;
  narrowing to method + prefix would be a contract change.

## What the component mounts

`routes/DeviceDetail.svelte` itself. The configure part is too entangled with
the page's state (channel rename, rooms and functions, taxonomy) to extract
cleanly, so the component host narrows the page through the surface mechanism
instead: it shows `device.configure*` and `device.history` and nothing else.
One code path, one test that fails for both.

## Parts

1. **Shadow-root readiness** — done: `lib/dom.ts` `deepActiveElement()` replaces
   every `document.activeElement` read (5 editor dialogs + Inbox, SysvarList).
   The `dark:` variant needs no change: the element puts `dark` and `data-skin`
   on an inner container in the shadow root, so `.dark *` matches. To be
   measured in the browser test.
2. **Host seam** (`lib/host/`): transport (request: method, path, headers,
   body → status, body; events: raw frame subscription), navigation (hash
   route), key-value storage, the root node for id lookups, the portal target,
   download capability. The SPA installs the page host (fetch + CSRF,
   WebSocket, `location.hash`, `localStorage`, `document`) — behaviour of the
   SPA unchanged. One host per bundle instance (the stores are module
   singletons) — one editor element per page.
3. **Element + second Vite target**: wrapper class around `mount()`, shadow
   root, adopted stylesheet, `@property` rules registered on the document once,
   `BitsConfig defaultPortalTo` = node in the shadow root, properties `locale`,
   `dark`, `skin`, `transport`, `address`, `central`. Output to
   `spa_dist/component/`.
4. **Daemon**: serve the module + manifest, `routes[]` from one source that the
   component's transport enforces at runtime (a call outside → error), OpenAPI,
   `pkg/hmapi` DTO, tests.
5. **Tests**: Playwright test host page (not the SPA shell) mounting the
   element with a transport over the mocked API; the same spec runs against
   the SPA route. Open: the acceptance asks for the simulated CCU — no browser
   suite runs against a daemon today (spa-e2e mocks `/api/v1`, `tests/e2e` has
   no browser). Decide before part 5.

Hand-over issue to `homematicip_local` is step 2c.
