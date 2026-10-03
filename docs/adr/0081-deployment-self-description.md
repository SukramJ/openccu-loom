# ADR 0081 — The daemon describes its deployment and its login paths

- **Status**: accepted (2026-10-03)
- **Related**: [ADR 0021 — mDNS self-advertisement](./0021-mdns-self-advertisement.md),
  [ADR 0058 — mDNS CCU serials](./0058-mdns-ccu-serials.md),
  [ADR 0044 — single-port onboarding and HA Ingress auth](./0044-single-port-onboarding-and-ha-ingress-auth.md),
  [ADR 0074 — per-central features](./0074-per-central-features.md),
  [ADR 0076 — client pairing](./0076-client-pairing.md),
  [ADR 0078 — lite webserver ingress](./0078-lite-webserver-ingress.md),
  [ADR 0079 — box-shell single sign-on](./0079-lite-shell-session-sso.md),
  [ADR 0080 — box API tokens through the lite gate](./0080-lite-gate-api-tokens.md)

## Context

A client that sets up a connection to the daemon has to decide which
questions to ask a person: a host and a port or a box address, a daemon
token or a box token, pairing or a password. Which of these are possible
depends on where the daemon runs — as the add-on on an openccu-lite box,
as the add-on on a classic CCU, as the Home Assistant add-on, or standalone
— and on which login paths its configuration has switched on.

The daemon knows all of this and tells nobody:

- Where it runs is answered by four separate probes with overlapping
  meanings (`build.IsAddon()`, `isSupervised()`,
  `detectSupervisedRestart()`, `isLiteAddonHost()`), none of which is
  published. `addon_build` on `GET /info` is true on a classic CCU and on a
  lite box alike. `OPENCCU_LOOM_SUPERVISOR=1` is set by the Home Assistant
  add-on image, by the CCU add-on's start script and by operators who want
  the restart action, so it identifies none of them.
- Which login paths are open is published for two of them (`auth.oidc.v1`,
  `auth.ccu.v1`) and for none of the others.
- The mDNS record carries neither, and its `tls` key is the literal `0`
  whatever the listener does.

A client therefore shows every field it has. The Home Assistant integration
offers the box switch and the box token on every setup form, including for
a daemon on a classic CCU where no box exists.

Two facts bound the solution:

- **The system type is a property of a central, not of the daemon.** One
  daemon may hold a lite central and a classic one (the lite manifest
  anticipates a remote CCU as a further central). A daemon-wide
  "backend" label would be wrong for such a fleet. What a central offers is
  already published per central (ADR 0074) and is not this ADR's subject.
- **On a lite box, `/info` is not readable before login.** The box's gate
  runs on every URL under `/addons/` and answers a request without a
  credential with 401 (occulited `deploy/lighttpd/occulited.conf`,
  `occulite-gate.lua`); the daemon's own port is closed by the box's
  firewall by default. mDNS is the only channel on which the daemon can
  describe itself to a client that has not logged in yet.

## Decision

- **One deployment value, from one resolver.** The composition root has a
  single resolver for `deployment.Kind`: `lite-addon`, `ccu-addon`,
  `ha-addon` or `standalone`. It is a pure function of the environment
  and the host, checked once at start, and everything that asked one of
  the old probes "where am I" asks it instead.
- **The packaging declares it; the daemon does not guess.**
  `OPENCCU_LOOM_DEPLOYMENT` is set to `ha-addon` by the Home Assistant
  add-on image and to `ccu-addon` by the CCU add-on's start script. Unset
  means `standalone`; any other value is a startup error, `lite-addon`
  included, which is never declared. The classic and
  the lite add-on share one package and one start script, so `ccu-addon`
  is refined to `lite-addon` when the host is a lite box holding the
  add-on's token file — the existing `isLiteAddonHost()` predicate, which
  reads both facts from the host. An add-on installation that predates the
  variable is still recognised by its install path and resolves the same
  way. A declared `ha-addon` or `standalone` is taken as
  it is; the host facts never override it.
- **`OPENCCU_LOOM_SUPERVISOR` keeps its one honest meaning**: something
  restarts the process when it exits. It no longer implies a deployment.
- **`GET /info` publishes a `deployment` object**: `kind`, and
  `ingress_path` when the system's own web server fronts the daemon
  (`/addons/loom/` on a lite box). `addon_build` stays, unchanged, for
  existing clients.
- **Every login path has a capability token**, each read from the same
  condition that wires the resolver it names: `auth.basic.v1`,
  `auth.bearer.v1`, `auth.pairing.v1`, `auth.occulite_token.v1`,
  `auth.occulite_sso.v1`, `auth.ha_ingress.v1`, beside the existing
  `auth.oidc.v1` and `auth.ccu.v1`.
- **The mDNS record carries the short form of the same model**:
  `txtvers=1`, `deploy=<kind>`, `ingress=<path>` when there is one,
  `auth=<comma-separated login paths>`, and a `tls` that reports the
  listener. A login path is named by the middle segment of its capability
  token (`auth.occulite_token.v1` is `occulite_token`), so the record and
  `/info` share one vocabulary. The record is a hint for choosing the first form; `/info` is
  the authority, and a client that can read `/info` trusts it over the
  record. Both are rendered from the one resolved value, so they cannot
  disagree.
- **The HA Ingress passthrough defaults on only for `ha-addon`.** Its
  tri-state default was the supervised stamp, which is also true on a CCU
  and on a lite box; there the passthrough was armed although no Supervisor
  exists. Outside the Home Assistant add-on the passthrough stays inert
  even when `north.rest.auth.ha_ingress.enabled` is set to true, as it
  already did for a daemon that was not supervised.

## Consequences

- A client can build its setup from the daemon's own statement: the box
  fields appear when `deploy=lite-addon` / `deployment.kind: lite-addon`
  and the box-token token are present, and not otherwise.
- The `auth` list on mDNS tells anyone on the local network which login
  paths a daemon accepts. `/info` says the same without a credential
  wherever the daemon's port is reachable, so the record discloses nothing
  new; on a lite box it is the only way to say it at all.
- The mDNS record still names the daemon's own port, which a lite box keeps
  closed. The daemon cannot observe the box's firewall, so it does not
  claim a reachable port: `ingress=` tells the client to go through the
  box's web server instead.
- Behaviour change: on a CCU add-on and on a lite add-on the HA Ingress
  passthrough is off by default. It never matched there in practice (it
  needs a peer in the Supervisor's subnet).
- Contract change, backwards compatible: new `/info` property, new
  capability tokens, new TXT keys. Minor bump of
  `api_version`.
- The MCP surface exposes `/info` as it is; the new fields travel with it
  and need no new verb.

## Rejected

- **A daemon-wide `backend=` label.** Wrong for a mixed fleet, and it
  would answer a different question: a standalone daemon talking to a
  remote lite box has a lite central and no box login path.
- **Deriving the Home Assistant add-on from the Supervisor's environment.**
  Nothing in this repository establishes what the Supervisor injects; a
  value the packaging sets is one the daemon can read.
- **Leaving the login paths to be derived from the deployment kind.** That
  moves a convention into every client and breaks the first time an
  operator switches a path off.
