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

## The administrator's half

Pairing is on by default; `north.rest.auth.pairing.enabled: false`
switches the unauthenticated `/api/v1/pairing` routes off (clients then
get `503 pairing_off`). The switch is read at start-up, so changing it
needs a restart. The admin side stays available either way and simply
has nothing to show.

- **In the web UI:** **Settings → API Tokens** shows a **Pairing
  requests** card with every pending request — its name, app, role and
  address. Type the six digits the client displays into **Code**, then
  **Approve** — typing the code is the approval. **Reject** turns the request
  down.
- **Over REST (admin only):** `GET /api/v1/pairing-requests` lists the
  pending requests; `POST /api/v1/pairing-requests/{id}/approve` with
  the typed code approves one, `POST /api/v1/pairing-requests/{id}/reject`
  rejects it. Both answer `204`. A request is listed only once its
  client has revealed its half of the code (its first poll); approving
  one before that answers `409`.
- **Live updates:** the WebSocket broadcast `pairing.requests_changed`
  on topic `system.pairing` fires whenever a request arrives, is
  decided, expires or is withdrawn. It carries only the pending count;
  fetch the list for the details. Pending requests also appear as an
  operator warning.

A wrong code rejects the request (`409`), and so does **Reject**; both
mute the client's (address, app) pair for ten minutes. A request marked
as a look-alike (`look_alike`) shares its address, or its app and
instance, with another pending request — compare the code with care.

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
