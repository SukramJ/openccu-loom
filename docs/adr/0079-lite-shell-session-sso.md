# ADR 0079 — Box-shell single sign-on over the lite ingress

- **Status**: accepted (2026-10-01)
- **Related**: [ADR 0078 — lite webserver ingress](./0078-lite-webserver-ingress.md),
  [ADR 0044 — single port + HA-Ingress auth](./0044-single-port-onboarding-and-ha-ingress-auth.md),
  [ADR 0077 — add-on token onboarding](./0077-lite-addon-token-onboarding.md),
  [ADR 0043 — CCU authentication provider](./0043-ccu-authentication-provider.md),
  [openccu-lite `PORTING-PROMPT.md` (the normative session-header contract)](https://github.com/hobbyquaker/openccu-lite/blob/main/docs/PORTING-PROMPT.md)

## Context

Since ADR 0078 an openccu-lite box serves the Config UI through its own
web server. The box's gate authenticates the operator once, for the
shell and every add-on page alike — and hands the add-on the proof: on
every request it passes under `/addons/`, lighttpd carries the validated
credential in the `X-Occulite-Session` header (static files, XHRs, the
proxied backend, WebSocket upgrades). The gate's contract makes the
header trustworthy **behind the gate**: a client-sent header of that
name is stripped from every request and every socket before anything
else, so behind the gate the value is never client-controlled. The
add-on manifest already declares that it wants the header
(`ui.session_header: true` in `packaging/ccu-addon/ccu/openccu-lite.json`),
which also stops the shell from appending the legacy `?sid=` alias.

The daemon, however, ignores the header today. The operator who just
authenticated against the box lands on the daemon's own login form and
signs in a second time — the one seam left in an otherwise seamless
ingress. ADR 0078 deliberately deferred this: mapping the gate's session
onto a daemon identity touches the auth core and deserves its own
decision. This is that decision.

Two properties distinguish this case from the HA-Ingress passthrough
(ADR 0044). There, the daemon cannot verify the outer session and must
trust a perimeter (supervised build, trusted subnet, marker header).
Here the box itself answers the question: `GET /api/auth/v1/state` with
`Authorization: Bearer <session id>` returns `authenticated`, `user`
and `role` for exactly that session — the daemon can verify every claim
live against the system that issued it. And the header's trust boundary
is sharply documented by the contract itself: a CCU has no gate and
passes a client's header straight through, and the daemon's own port is
reachable without the gate — so the header may only ever be treated as
a **claim**, never as proof.

## Decision

- **Accept the box-shell session as a daemon identity, by verification,
  never by trust.** A new resolver in the authentication chain reads
  `X-Occulite-Session` and confirms it against the box before any
  request is answered under it. The header alone authenticates nothing:
  a value that is not a 26-character base32 session id (in particular
  the ten-character legacy alias, which the box API refuses) makes the
  resolver defer, and so does any verification outcome short of
  `authenticated: true` with a role the mapping knows.
- **Verify only against the onboarded local box.** The resolver asks the
  openccu-lite central that the add-on onboarding bound (ADR 0077),
  over the established occulited client (loopback or TLS-pinned), and
  no other system. Without such a central the resolver is inert. This
  replaces ADR 0044's perimeter layers: no subnet check and no marker
  header are needed, because possession of a session id that the box
  confirms *is* the session — a spoofed header without one verifies to
  nothing.
- **Opt-in config, enabled by the lite add-on packaging.** A new block
  `north.rest.auth.occulite_sso` with `enabled` (daemon default
  `false`, mirroring `ha_ingress`); the openccu-lite add-on ships its
  configuration with the switch on, so the seamless flow is the
  packaged default exactly where the gate exists. Both catalogue
  entries (`config.field.*`, `config.help.*`, en + de) ship with the
  field.
- **Fixed, fail-closed role mapping.** Box `admin` maps to the daemon's
  admin role, box `user` to operator; any other or missing role defers.
  The identity's subject is the box user prefixed with its origin
  (`occulite:<user>`), so audit rows name the real actor and never
  collide with local accounts.
- **Verification is cached per session id for 60 seconds** — the
  contract's own advice ("cache the answer briefly, if at all") — with
  a short negative cache so a dead session cannot hammer the box.
  WebSocket upgrades carry the header and are verified at upgrade; an
  established socket then re-verifies against the box every 60 seconds
  (role compared too, so a demotion closes it), so a session revoked on
  the box ends the live event stream within one verification interval —
  a transient verify error keeps the socket, because availability must
  not hinge on the box's loopback.
- **The chain order stays first-wins, SSO last.** A request carrying a
  daemon session cookie, bearer token or Basic credentials is resolved
  by the existing pipeline first; the SSO resolver only answers when
  every earlier resolver deferred, and defers itself otherwise —
  preserving the resolver contract the auth core guards. An operator
  with a local session keeps it; nothing about tokens, MCP mounts or
  the REST role gates changes. One consequence, shared with the
  HA-Ingress fallback: a request presenting an *invalid* daemon
  credential is not answered 401 at the resolver stage — behind the
  gate the box session then authenticates it, attributed to the box
  user. Revoking a daemon token therefore never revokes what the box
  session grants anyway.
- **The SPA follows the identity's origin.** An identity resolved from
  the box session is marked with its provider; the SPA then skips the
  login form, shows the box user read-only, and hides the logout
  button — logging out of the daemon while the shell session lives
  would only bounce straight back in. The box's own logout remains the
  one that ends the session.

## Alternatives considered

- **Reusing the ReGa session on CCU3/OpenCCU** (the second half of the
  suggestion that prompted this ADR). Rejected: a CCU has no stripping
  gate — the header contract itself warns that a CCU passes a client's
  header straight through — and there is no per-session verification
  API comparable to `auth/v1/state`; ADR 0043 already lets operators
  sign in with their CCU accounts, which is the honest equivalent
  there.
- **Trusting the header behind perimeter checks instead of verifying**
  (the ADR 0044 pattern). Unnecessary here and weaker: the box can be
  asked, so it is asked — every request class (ingress, direct port,
  local process on the loopback) collapses into the same rule.
- **Exchanging the confirmed box session for a daemon session cookie.**
  A second session store to keep consistent, plus a lifetime that can
  outlive its source; the per-request resolver with a 60-second cache
  gives the same UX without a second authority over "who is signed in".
- **Auto-provisioning local accounts for box users.** The identity is
  ephemeral by design; a provisioned account would persist rights after
  the box user is gone.

## Consequences

- New resolver in `internal/auth` with defer semantics pinned by the
  existing first-wins resolver guard; a contract test drives the full
  chain (header alone → 401, header + confirming box → role-mapped
  identity, alias → defer, box down → defer).
- The occulited client gains the `auth/v1/state` verification call;
  `godevccu`'s litefake must learn the endpoint and the gate header
  (its wire contract is versioned, so this is a godevccu release plus a
  version bump here).
- SPA: provider-aware session store (skip login, read-only user, no
  logout) — e2e-covered against litefake.
- `GET /auth/me` additively gains the identity's provider so the SPA
  needs no second probe (minor APIVersion bump).
- The daemon keeps working unchanged wherever the gate does not exist:
  on a CCU, on a bare install, or with the switch off, the resolver
  defers on every request and the login form stays.

## Amendment (2026-11-11) — the box account's level decides the role

The decision mapped the box's role word: `admin` to the daemon's admin
role, `user` to operator. The box derives that word from the account's
**level** — `read`, `operate`, `configure`, `administer` — and answers
`user` for all three below `administer`; its own documentation calls the
word an alias kept for older clients. Reading the word therefore made a
box account that may only read an operator here: it could switch a
device through the daemon that the box itself would not let it switch.

The mapping now reads `level` from `GET /api/auth/v1/state`:

| Box level | Daemon role |
| --- | --- |
| `read` | viewer |
| `operate` | operator |
| `configure` | operator |
| `administer` | admin |

It stays fixed and fail-closed. A level the mapping does not know defers
even when a known role word stands beside it, so a level the box adds
later is never read as the coarser word it derives. Only a box that
answers no level at all is read by its role word, as before. A box API
token answers neither and is mapped by its scopes, unchanged (ADR 0080).

`configure` maps to operator because the daemon has no role between
operator and admin, and raising it to admin would grant what the box
withholds from that level.

**Better than the comparison targets, than before, and the right cut.**
The box's own shell and its other add-ons hold an account to its level;
the daemon was the one place a `read` account gained a right. It is
better than before for the same reason, and it no longer depends on a
field the box has announced as transitional. The cut is unchanged: the
box owns accounts and levels, the daemon owns the translation into its
three roles, in one function (`occuliteSessionRole`), and the adapter
that carries the box's answer to it lives in one package
(`internal/auth/occulitebox`) that the composition root and the contract
test share.
