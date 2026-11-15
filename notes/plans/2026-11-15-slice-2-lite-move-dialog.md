# Slice 2 — guided lite-move dialog (plan + owner decisions)

Fable plan of 2026-11-15, read-only; parent plan
`notes/plans/2026-11-15-guided-lite-migration.md` §Slice 2.

## Owner decisions (2026-11-15)

- **Box link**: the SPA builds it — `location.protocol + "//" + location.hostname + "/"`,
  new tab. Holds because the onboarder only records centrals whose host is this
  machine (`cmd/openccu-loom/lite_addon_onboard.go:240-263`). No DTO change.
- **Entry**: primary a button on the warning row in Diagnostics, which mounts the
  dialog itself (`settings.ccus` is hidden in the lite profile, `hostOwned()`,
  `internal/north/ui/surface/surface.go:266-270`); secondary
  `#/settings?tab=ccus&lite_move=<central>`.
- **Reasons**: offer the dialog for `no_token`, `ambiguous`, `failed`; `no_token`
  adds a note that reinstalling the add-on is the alternative. Pairing only when
  the probe reports `lite.pairing_available`.

## Flow → existing API (no endpoint, DTO or OpenAPI change)

1. `api.listCentralsV2()` → stored row.
2. `api.probeCentral({host: row.host})`; stop unless `system_type === "openccu-lite"`,
   `ready`, `lite.pairing_available`.
3. `createPairing` (`lib/onboarding/pairing.svelte.ts:50`) `start({host, access:"full"})`;
   show code + box link; poll.
4. On approval `api.updateCentralV2(name, body)`; body explicit because the overlay
   restores omitted keys (`admin_centrals.go:434-475`) and `dropOtherSystemFields`
   clears only account + RPC ports (`:500-536`): `name, host, enabled, system_type:
   "openccu-lite", pairing_id, json_rpc_port: 0, tls: false,
   tls_insecure_skip_verify: false, primary_interface` (kept if still offered else
   `""`), `interfaces: probe.lite.interfaces.map(name => ({name}))`. Pure builder in
   `lib/onboarding/lite-move.ts`.
5. Result: readiness from `centralStore`; what goes = stored interfaces minus probe
   interfaces (CUxD) + static sentence on programs and system variables.

MCP: not extended — the move needs a human confirmation in the box's UI.
Surface registry: no entry (dialogs are not surfaces); comment at `surface.go:266`.
