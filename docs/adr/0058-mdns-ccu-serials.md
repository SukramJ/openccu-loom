# ADR 0058 — mDNS TXT carries the configured CCU serials

- Status: accepted (partially supersedes ADR 0021's TXT decision)
- Date: 2026-07-29

## Context

ADR 0021 deliberately kept CCU identities out of the daemon's
`_openccu-loom._tcp` TXT record: the list is volatile (readiness-gated
bring-up, live adopt/remove), TXT space is limited, and serials on the
multicast wire are pre-auth information. Discovery clients (the HA
integration) therefore learned the per-CCU list only after
authenticating, via `GET /api/v1/system/ccu`, guided by the cheap
`centrals=<count>` hint.

Operating experience turned the trade the other way: discovery-side
per-CCU dedup and selection happen BEFORE the token step, and the
serial (the CCU's printed identity) is broadcast by CCUs themselves in
other protocols on the same LAN — the confidentiality gain was small,
the flow cost real.

## Decision

1. The TXT bundle gains `ccus=<sn1>,<sn2>,…` — the **last 10
   characters** of each configured CCU's serial (the printed short
   form), comma-separated, sorted ascending for a stable record.
2. Serials appear as they resolve: the record starts without (or with a
   partial) `ccus` key and is **re-announced at runtime**
   (`Advertiser.UpdateTXT` → zeroconf `SetText`) whenever a central's
   serial resolves or the central set changes (live adopt/remove). The
   `centrals=<count>` hint is refreshed on the same trigger — it was
   silently stale after live adopt before.
3. Unresolved serials are omitted, never padded — a consumer must treat
   `ccus` as best-effort and `GET /api/v1/system/ccu` stays the
   authoritative post-auth source.

## Consequences

- LAN peers can enumerate CCU serials without authentication. Accepted
  by explicit project decision; the serial is an identifier, not a
  credential.
- TXT growth is bounded: 10 bytes per CCU plus separators inside a
  single TXT string (255-byte limit) — dozens of CCUs fit; the daemon
  truncates the list at the limit rather than emitting an invalid
  record.
- The HA integration can dedup/select per CCU at discovery time; its
  flow change lives in that repository.

## Amendment (2026-11-20) — the serial names the CCU, not the daemon

A second daemon serving the same CCU — a test build started on a laptop
against the production CCU — announced the same `ccus` value as the
production daemon. The HA integration had read "a configured serial at a
new address" as "my daemon moved" and rewrote its production config entry
to the test daemon (#1131, SukramJ/homematicip_local#1386). The serial
answers "is this CCU set up?"; it cannot answer "is this my daemon?".

4. Every installation has an **instance id**: 32 lower-case hex digits
   from a cryptographic random source, created on the first start and
   kept in the database (`instance_identity`), so it survives restarts,
   updates and a backup restored on new hardware. The TXT bundle carries
   it as `id=<instance id>`; `GET /api/v1/info` serves it as
   `instance_id`. Without a database there is no id, and the key is left
   out — an id that changed on every start would name nothing.
5. The id is **public and proves nothing**. Anyone on the LAN can read
   and repeat it, so a client uses it to tell daemons apart, never as a
   reason to send credentials somewhere. Following a daemon to a new
   address is a decision the operator confirms.
6. A daemon whose REST listener is bound to loopback only does not
   announce itself: nobody on the network can reach it, and the
   announcement would only send clients to an address that refuses them.

Better than before: a client can distinguish "moved" from "another
daemon" at discovery time, and the record no longer advertises
unreachable endpoints. Against the comparison targets this amendment
makes no claim; their discovery records were not examined for it. The
split is right: the daemon states facts about itself (id, served CCUs);
whether to follow is the client's decision, made with the operator.
