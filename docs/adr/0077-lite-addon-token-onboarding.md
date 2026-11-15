# ADR 0077 — The add-on token onboards the local openccu-lite box

- **Status**: accepted (2026-09-29)
- **Related**: [ADR 0071 — South profiles](./0071-south-profiles.md),
  [ADR 0072 — the event stream](./0072-openccu-lite-event-stream.md),
  [ADR 0076 — Client pairing](./0076-client-pairing.md),
  `cmd/openccu-loom/lite_addon_onboard.go`

## Context

When OpenCCU-Loom runs as an add-on on an openccu-lite box, the box
already holds a credential for it: an add-on whose manifest declares
`runtime.api_scopes` gets an API token minted **at every occulited
start** (and after installs and policy changes), written to
`/run/occulite/addon-tokens/<id>.api` — mode 0600, owned by the add-on's
user, the secret plus a trailing newline, held in memory only on the box
side. occulited never grants an add-on token `*`, `auth:admin`, `power`,
`backup` or `radio:keys`; a declaration naming one is logged and left
out.

Until now the co-located daemon ignored that file: the first boot walked
the operator through pairing with the box it runs on — a code typed to
approve access to a system whose administrator just installed the add-on.
And a token copied once would be the wrong design anyway: it goes stale
whenever occulited restarts underneath a running daemon.

## Decision

Two pieces, deliberately separable:

- **`api_token_file`** is a regular central config field (config, store
  column, REST row, SPA pass-through): a path whose file holds the
  token. The occulited transport reads it **per request**
  (`occulited.FileToken`), so rotation needs no reconnect logic — the
  next request simply carries the fresh credential. The field is a path,
  not a secret; it is mutually exclusive with a stored token and useful
  beyond the add-on (container secret mounts).
- **Auto-onboarding**: at boot, the composition root starts a bounded
  background attempt when three facts hold — the host's `/VERSION`
  carries `VARIANT=lite`, the minted token file is readable, and **no
  central is configured at all**. It then waits for the box to answer,
  reads the hostname and the interface list from the box (never
  assumed), and creates the central through the same decorated admin
  service a REST create takes, so persist-then-adopt is the production
  path. The central is named after the box's hostname, taken once —
  central names are sticky by design — reduced to the callback router's
  allowlist, falling back to `local`.

The empty-list condition is the whole lifecycle policy: an operator who
deletes the auto-onboarded central has, at that moment, a history of
having configured centrals only if they created others — and if the list
is empty again, re-onboarding on the next boot is the desired first-boot
behaviour, not a fight. The manifest declares
`api_scopes: ["rpc:admin", "meta:write", "system:write", "logs:read"]`;
`backup` and `power` remain reachable only through a paired or manual
token, and the feature table reports them as `missing_scope` exactly as
for any narrow token.

### The central a move from OpenCCU brings along (2026-11-15)

A box that moves from OpenCCU to openccu-lite keeps the add-on's data,
and with it a CCU central pointing at the box itself. It can never come
up — the readiness marker it waits for is ReGa's, and there is no ReGa —
and the empty-list rule above keeps the onboarding away from it. So the
attempt has a second case: when the token file is readable and **exactly
one** stored central is a CCU central whose host is this machine (a
loopback address, or an address one of its interfaces carries), that
central is moved to openccu-lite in place.

- It keeps its **name** and what is stored under it: channel flags,
  visibility un-ignore patterns, recording overrides, Matter exposures and
  its Matter partition. The account, the RPC ports and the interface list
  are the CCU's and go; the interfaces are read from the box, as at a first
  boot, and the token comes from `api_token_file`.
- The move is one save through the admin service, which restarts a running
  central on such an edit. What goes is what belongs to an interface the
  box does not have (CUxD): its devices are removed as by a removal, so
  their channel flags, recording overrides, cache rows and Matter
  exposures go with them.
- Nothing is moved when a central already uses the add-on token (the box
  is configured; a leftover CCU central is the operator's to delete) or
  when more than one CCU central points at the box (which one is the box
  is not the daemon's to guess).
- Where the daemon leaves such a central alone — that ambiguity, a box
  that minted no token because the add-on's manifest was not applied, or a
  move that did not complete (the box did not answer within the window, or
  the save failed) — the central lands on the operator warning list as
  `lite-move:<central>`, with one sentence per reason that names the way
  out, instead of an endless "waiting for CCU". The warning list is
  mirrored onto the box's Status page (ADR 0090), so the notice stands
  where the operator looks after the system update. It clears by itself
  once the central is no longer stored as a CCU central. It replaced the
  `/health` component `config.lite_addon` of 0.104.0, which would have
  reached the same list a second time as a generic "unhealthy" line.

**Better than before, and the right cut.** Before, the operator had to
find out that the central's type was the problem and re-enter it by hand
after every such move; the CCU's own WebUI and the aiohomematic family
have no counterpart, since neither runs on the box across that move. The
decision belongs in the daemon's composition root, next to the first-boot
case: it is the only place that knows both the host (`VARIANT=lite`, the
token file) and the stored centrals, and it writes through the admin
service so no second persistence path exists.

## Alternatives considered

- **Copy the token into the store once.** Stale after the next occulited
  restart; the failure would look like a revoked token. Rejected.
- **Re-read only on 401.** Works, but adds retry state for something a
  per-request read of a tmpfs file gets for free. Rejected.
- **A config switch for the onboarding.** More surface for a behaviour
  whose only trigger state (no centrals at all, on the box itself, token
  file present) already expresses the intent. Not now; the empty-list
  rule can grow a switch later without breaking anything.
- **Skip the wizard entirely on the box.** The SPA's first-run flow
  stays: the auto-onboarded central simply appears in it, and everything
  else (auth, MQTT, Matter) is still the operator's.

## Consequences

- A fresh add-on install on an openccu-lite box shows the local system's
  devices without any pairing step; pairing remains for `backup`/`power`
  and for every remote client.
- The e2e guard `TestLiteAddonAutoOnboardingFirstBoot` boots the real
  binary against a fake box and fails when the composition-root call,
  the persist-then-adopt path or the bring-up is missing.
- The manifest's `api_scopes` are pinned by
  `TestCCUAddonLiteManifestDeclaresRuntimePolicy`: a dropped declaration
  removes the token file at the next update.
- REST `APIVersion` 12.4.0 carries the new `api_token_file` row field.
