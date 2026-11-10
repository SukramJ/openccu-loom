# Plan — go-fabric v0.21.0, go-fabric-ui v0.9.0, the dependency round, mDNS findings at start, the Topology tab (0.99.0)

Date 2026-11-10. Repo: openccu-loom (`/home/markus/Dokumente/GitHub/openccu-loom`). Branch `gofabric-0.21.0` off `main` (d6d2f95c). CLAUDE.md rules; conventional commits with the repo's `Signed-off-by` (DCO, not an attribution line; no other trailer); English; one PR; do not push, PR or tag; close nothing. Copy this plan to `notes/plans/gofabric-0.21.0-fabricui-0.9.0.md` in the first commit. Read `notes/plans/gofabric-0.19.1-fabricui-0.8.0.md` and `git show 04bd243b --stat` first: the last bump's file list is this one's.

## Why (owner decisions 2026-11-10)
- go-fabric 0.19.2 → an erase/fabric removal flush bounded at 500 ms (go-fabric#473); 0.20.0 and 0.21.0 are controller-side (#478, #488, #483) and leave Loom's device node unchanged; `node`'s diagnostics types are aliases of the new `diag` package (no source change). go-fabric-ui 0.9.0: the controller Diagnostics tab (not mounted here), `SessionInfo` gains four optional fields (absent on the node); the npm package `@sukramj/fabric-ui` 0.9.0 carries the same pages.
- Dependencies due: Go — `github.com/getkin/kin-openapi` 0.153.0, `github.com/pressly/goose/v3` 3.30.0, `golang.org/x/mod` 0.43.0, `x/sync` 0.25.0, `x/term` 0.48.0, `x/text` 0.44.0, `modernc.org/sqlite` 1.62.0 (plus whatever `go get -u ./...` of direct minor/patch brings; no major); npm in `assets/ui` — `@lucide/svelte` 1.71.0, `@playwright/test` 1.65.0, `@tailwindcss/vite` 4.3.5, `tailwindcss` 4.3.5, `bits-ui` 2.20.6, `svelte-check` 4.7.8, `vite` 8.3.7, `vitest` 5.1.0, `@sukramj/fabric-ui` 0.9.0. **Not** `typescript` 7 (major; owner says no). The Dependabot alert on `deepmerge` has no fixed version: leave it, say so.
- #1056, two items the owner picked: (a) **DiagnoseMDNS at start**: with `north.matter.mdns_interfaces` set, log each interface finding once after the node started, as go-ha2fabric 0.6.0 does (`cmd/ha2fabric/nodes.go:288-302`: `Node.DiagnoseMDNS()`, codes `interface_not_found`, `interface_filtered`, `interface_no_addresses`, at warn with `code`, `severity`, `message`); event name `matter.bridge.mdns.interface`; a unit test on a fake finding list if the node start is testable, else on the logging helper. (b) **Topology tab**: the Matter area gets the `NodeTopology` page of `@sukramj/fabric-ui` (`ui/src/lib/pages/index.ts:13` of go-fabric-ui) as a tab `topology` in `assets/ui/src/routes/matter/Matter.svelte` (`pages` :35-44, routes in `$lib/stores/fabric-node.svelte` `matterRoutes`), en/de label "Topology"/"Topologie", route `#/matter/topology`; the REST API already serves `/node/topology` (`assets/openapi.yaml:6577`). A visual baseline `matter-topology-{light,dark}` is added to `tests/e2e/visual.spec.ts` beside `MatterPair` (:195-216, :396-417); regenerate the linux baselines inside `mcr.microsoft.com/playwright:v1.65.0-noble` (the version `@playwright/test` moves to) as root, then `chown -R 1000:1000`; darwin baselines stay (refreshed on a Mac later; say so).
- Loom's own node start (`cmd/openccu-loom/daemon_matter.go:383-400`, `matterMDNS` :77).

## Sections (one commit each)
1. **Bump go-fabric/go-fabric-ui** (+ schema pin refresh if the last bump did one; the `assets/openapi.yaml` go-fabric-ui note → 0.9.0, re-merge only if the node paths changed — `SessionInfo`'s four new optional fields are under /node, so the copy's schema moves; REST API 15.8.0 → 15.9.0 if the repo bumps it for an additive schema change, check the last bump).
2. **Go dependency round** (`go get` the listed minor/patch versions, `go mod tidy`, notices if a version is named there).
3. **npm round** in `assets/ui` (`npm install` the listed versions, lockfile; `npm run check`, `npm test`).
4. **mDNS findings at start** (#1056 a).
5. **Topology tab** (#1056 b) with the visual baselines.
6. **CHANGELOG `[0.99.0]`**, version files (`internal/build/version.go`, both `packaging/ha-addon/*/config.yaml`), docs the Matter area page lists tabs in (grep "Diagnostics" in `docs/`), a comment on #1056 is the session's (do not post).

## Guards
`export PATH=$PATH:$HOME/go/bin`; `make ci` (or the Makefile's pipeline targets: build, vet, test, lint), the SPA `npm run check`, `npm test`, and the e2e visual run for the Matter specs (`npx playwright test -g Matter` in the container) — background, summary log; grep a failing log.

## Report back (short, fixed shape)
Branch, commits, guards with HEAD, dependency versions actually taken, differences, left out, questions.
