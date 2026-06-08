# Documentation backlog (DOC-TODO)

Tracks the gaps left after the MkDocs reorganisation. The published site
now covers three audiences — **End user**, **Administrator**,
**Developer/Integrator** — with a page per topic (see `mkdocs.yml` `nav`).
This list captures what is still missing, thin, or needs verification.

Conventions:

- All documentation is **English**. German `*.de.md` translations are a
  separate, opt-in track (the i18n machinery is already wired — see
  `docs/hooks/i18n_alternate_filter.py` and the `i18n` plugin in
  `mkdocs.yml`).
- Every concrete claim must be **verified against the code**, not against
  other docs/concepts/analyses.
- `mkdocs` is not installed on the authoring machine; CI builds the site
  (`.github/workflows/`). Keep relative links valid so
  `TestMarkdownLinksValid` / `mkdocs build --strict` stay green.

---

## Code follow-ups surfaced while writing docs

These are **code** gaps the documentation honestly exposed. They are not
doc tasks but must be tracked so the docs and the code converge.

- [x] **OIDC ID-token signatures are now verified.** Both callbacks
      (`internal/north/ui/oidc_handlers.go`, `internal/north/rest/handlers/oidc.go`)
      call `Client.VerifyIDToken`, which checks the RS256 signature against
      the provider's JWKS and validates `issuer` / `audience` / `exp`.
      `docs/SECURITY.md` and `docs/admin/auth.md` are updated accordingly.
- [ ] Confirm whether `GET /api/v1/metrics` is auth-gated; the router
      mounts it conditionally. Document the final answer in
      `docs/admin/observability.md`.

---

## End user

- [ ] **Screenshots.** `docs/user/web-ui.md` and `docs/user/matter.md`
      contain `<!-- TODO screenshot -->` placeholders. Capture real SPA
      screenshots (device list, parameter edit, un-ignore flow, Matter
      setup view with QR) and embed them.
- [ ] **Matter pairing walkthrough with images** for Apple Home, Google
      Home, and Alexa — step-by-step photos/screenshots, not just prose.
- [ ] **Device-type coverage page** — a user-facing table mapping common
      Homematic device classes (switch, dimmer, blind, thermostat, lock,
      sensors) to the north-bound surfaces (MQTT entity / Matter device
      type) they produce. Source of truth: `pkg/interfaces/matter.go`
      (`MatterDeviceTypeName`) + the `internal/model/custom/*` profiles.
- [ ] **First-device tutorial** — an end-to-end "bridge your first switch
      and toggle it" narrative tying getting-started → web-ui → concepts.
- [ ] **Glossary** of Homematic terms (paramset, MASTER vs VALUES,
      channel, ReGa, sysvar, program) for newcomers.

## Administrator

- [ ] **Prometheus metric catalogue.** `docs/admin/observability.md`
      describes the endpoint/format but not the individual metric names.
      Enumerate the real metrics from `internal/metrics/**` and add a
      reference table (name, type, labels, meaning).
- [ ] **Reverse-proxy + TLS recipes.** The daemon serves plain HTTP and
      has no built-in rate limiting. Add worked configs for nginx, Caddy,
      and Traefik (TLS termination, auth pass-through, rate limiting,
      WebSocket upgrade for `/api/v1/events`). Link from `docs/SECURITY.md`.
- [ ] **Backup automation examples.** `docs/admin/backup.md` covers the
      `openccu-loom backup` CLI + REST endpoints; add a cron example, a
      Docker/compose volume-snapshot example, and a restore-drill
      checklist. Verify the `--include-secrets` / `--out` / `--force`
      flags against `cmd/openccu-loom`.
- [ ] **Systemd unit + Docker Compose reference** for a production
      deployment (restart policy, volumes for `data_dir`, `secret.key`
      handling, healthcheck against `/health`).
- [ ] **Upgrade / migration guide** — how DB migrations (goose) run on
      start, what to back up before upgrading, rollback posture.
- [ ] Clarify the `?fresh`/forced-fresh-read behaviour referenced in the
      old caching doc — the value route is PUT-only, so confirm how an
      operator forces a fresh read (WS `ccu.cache_clear`? a REST GET
      modifier?) and document the exact mechanism in `docs/caching.md`.
- [ ] **MQTT setup how-to** (broker config, Home Assistant Discovery vs
      raw plane, TLS to the broker) — `docs/mqtt-topic-schema.md` documents
      the schema but there is no "get MQTT working" admin walkthrough.

## Developer / Integrator

- [ ] **REST/WS cookbook.** `docs/integrations/rest-ws.md` is an overview;
      add worked request/response examples per common task (read a value,
      write a value, subscribe + resume after disconnect, paramset
      read/write, trigger a program). Verify each against
      `assets/openapi.yaml` / `assets/wsapi.json`.
- [ ] **Publish-or-not decision for contributor setup docs.** These remain
      excluded from the site: `docs/contributor/goland-debugging.md`,
      `docs/contributor/matter-smoke.md`,
      `docs/contributor/matter-mdns-test-setup.md`. Decide whether to fold
      key parts into `docs/developer/setup.md` / `docs/developer/testing.md`
      or publish them as-is.
- [ ] **Publish ADRs natively (optional).** `docs/developer/adr-index.md`
      currently links each ADR to its GitHub blob (the `adr/` tree is
      excluded from the build). If we want them browsable on the site,
      un-exclude `adr/` and add a nav section — but first confirm no ADR
      cross-links point at excluded files (would break `--strict`).
- [ ] **Architecture deep-dives** — `docs/developer/architecture.md` is a
      map. Consider per-subsystem pages (event bus, coordinators, client
      reliability stack, store/caches) sourced from the code, for
      contributors who need more than the overview.
- [ ] **Matter contributor path** — link the parity workflow
      (`docs/matter-parity-contract.md`) to a concrete "add a cluster"
      walkthrough grounded in `internal/north/matter/**`.

## Cross-cutting

- [ ] **German translations (`*.de.md`).** The i18n hook + plugin are
      ready; no German pages exist yet. Prioritise the end-user and
      administrator pages first. Until a page has a `.de.md` sibling, the
      language switcher stays hidden for it (by the hook) — that is
      expected.
- [ ] **CI strict build.** Confirm the GitHub Pages workflow runs
      `mkdocs build --strict` (fails on broken links / pages-not-in-nav)
      and that the contract tests `TestMarkdownLinksValid` +
      `TestDocPurity*` run on PRs touching `docs/`.
- [ ] **Diagrams.** The mermaid diagrams in `concepts.md` /
      `architecture.md` render via `pymdownx.superfences` — verify they
      render on the published site and add more where a picture beats
      prose (callback flow, boot sequence, auth flow).
- [ ] **Search/SEO polish** — page descriptions, consistent H1s, and a
      short "see also" footer block per page.
- [ ] **Link audit on every docs PR** — re-run
      `go test ./tests/contract/ -run 'TestDocPurity|TestMarkdownLinksValid'`
      before merge.
