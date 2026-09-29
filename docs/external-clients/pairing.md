# Token pairing for external clients

How a client obtains an API token without anybody copying a secret
(ADR 0076 — the same protocol this daemon speaks as a client against an
openccu-lite box, so both pairings feel identical). openccu-loom-client
ships it as `start_pairing()`; this page is the wire contract for any
other implementation.

## The flow

1. **Ask** — `POST /api/v1/pairing`, unauthenticated, local networks
   only:

   ```json
   {
     "app": "my-bridge", "app_version": "1.0", "instance": "hostname",
     "name": "My Bridge on hostname", "role": "operator",
     "purpose": "device control", "commit": "<hex sha256(client_nonce)>"
   }
   ```

   `client_nonce` is ≥16 random bytes you keep secret for now. `role` is
   `viewer` or `operator` — `admin` is never pairable. The `202` answer:

   ```json
   {
     "id": "…", "poll": "<secret>", "nonce": "<hex>",
     "expires_in": 300, "interval": 2, "fingerprint": "<hex|empty>"
   }
   ```

2. **Check the fingerprint** — when the answer names one, it must equal
   the SHA-256 of the certificate DER *your connection* saw. A mismatch
   means something terminates TLS between you and the daemon: abort and
   `DELETE` the request — the code would authenticate the interceptor.
   An empty fingerprint (plain HTTP, or a TLS-terminating reverse
   proxy) simply drops the binding.

3. **Derive and display the code** — six digits of
   `SHA-256(nonce ‖ client_nonce ‖ fingerprint)` (first four bytes,
   big-endian, mod 1 000 000, zero-padded). Show them to the human
   driving the setup.

4. **Poll** — `GET /api/v1/pairing/{id}?client_nonce=<hex>&wait=25`
   with `Authorization: Pairing <poll>`. The first poll reveals your
   nonce (it must match the commitment); `wait` long-polls up to 30 s.
   Without `wait`, respect `interval` — polling faster answers `429`
   with code `pairing_slow_down` (keep polling, slower). The daemon's
   administrator meanwhile sees the request on the tokens panel and
   approves by **typing your code**.

5. **Take the token** — the deciding poll answers
   `{"state":"approved","token":"…","subject":"…","role":"…"}` exactly
   once; the request is gone afterwards (a re-poll is `404`). Other
   terminal states: `rejected` (also the result of a wrong typed code —
   your (address, app) pair is then muted for ten minutes) and
   `expired` (five minutes without a decision).

## Error codes to branch on

| HTTP | `code` | Meaning |
| --- | --- | --- |
| 503 | `pairing_off` | switched off on this daemon — do not retry; ask for a manual token |
| 403 | `pairing_not_local` | the daemon judged your peer address non-local |
| 429 | `pairing_slow_down` | poll at the announced interval; not an abort |
| 429 | `rate_limited` | pending cap, per-hour cap, or a rejection's mute — give up for now |

## Limits

Five minutes per request, five pending at most, ten per hour per
address, one per (address, app). The peer address is the connection's,
deliberately not `X-Forwarded-For` — behind a reverse proxy, gate the
`/api/v1/pairing` path there exactly as you gate the login.
