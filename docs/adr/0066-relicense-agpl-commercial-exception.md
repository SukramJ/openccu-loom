# ADR 0066 — Relicense to AGPL-3.0-only with a commercial exception

- **Status**: rejected (2026-08-28)
- **Date**: 2026-08-26
- **Supersedes**: nothing. [ADR 0001 — License: MIT](./0001-license-mit.md) stands.
- **Related**: [ADR 0003 — Embed openccu-data metadata artifacts](./0003-embed-occu-extracts.md)

> **Outcome, 2026-08-28: not adopted. The project stays MIT.**
>
> The proposal below is kept in full because its analysis is worth reading
> and because the question will be asked again. What was decided is only the
> answer, and the answer is no.
>
> Two things follow, and both are the reason the decision belongs in this
> repository rather than in a consumer's notes. First, the CLA obligation
> this ADR would have created does not arise; `CONTRIBUTING.md` continues to
> take contributions under the DCO, and the working note
> `notes/reference/contributor-agreements-declined.md` continues to describe
> why. Second — and this is the consequence that
> reaches other repositories — the daemon remains a possible destination for
> code moved out of the MIT siblings. Line 125 of this ADR states the
> constraint that would otherwise have applied: "MIT material may enter
> OpenCCU-Loom, but AGPL material may not go back." Every future proposal to
> move code here would have been a one-way door. It is not one.
>
> `LICENSE` was never changed, so nothing had to be reverted.

## Context

[ADR 0001](./0001-license-mit.md) chose MIT for four structural reasons: no
copyleft in the dependency graph, alignment with the MIT sibling projects
(aiohomematic, aiohomematic-config, openccu-data), Go-ecosystem convention, and
downstream freedom. Three of those still hold. The fourth — downstream freedom —
is the one the maintainer now wants to constrain.

The goal is narrow and should be stated precisely, because it decides the
licence: **monetising the project should require the maintainer's permission.**
Selling it, offering it as a hosted service, or embedding it in a commercial
product are the cases in scope. Running it inside a company for that company's
own buildings is not.

Two facts frame what is possible.

### The copyright chain is clean

`git shortlog -sne --all` on 2026-08-26:

| Author | Commits |
| --- | ---: |
| SukramJ | 562 |
| dependabot[bot] | 32 |

The Dependabot commits are version-string bumps in `go.mod` / `package.json` —
below the threshold of copyrightable authorship. There is a single rights
holder, so relicensing needs nobody else's consent.

### Relicensing only works forward

MIT is an irrevocable grant to everyone who already holds a copy. Everything up
to and including v0.65.0 stays MIT for good — commercial use and forks included.
The copies are demonstrably distributed: `proxy.golang.org` serves an immutable
cache of **all 156 tagged releases, from v0.1.0 (2026-06-08) onward**, plus
commit snapshots that `@v/list` does not report. Verified 2026-08-26:
`@v/list` returns exactly the repository's tag count, `v0.1.0.zip` downloads
(9.2 MB) and carries the MIT `LICENSE`, while a non-existent module path and a
non-existent version both return 404 — so the listing is real and not an
artefact of the query. Google does not purge that cache on request. GitHub
forks, Software Heritage and local clones compound it.

What makes the timing favourable rather than futile: the repository has **0
forks and 4 stars**, and is three months old. The window will never be wider
than it is now.

### The binary is already non-commercial

Worth recording, because it changes what the new licence actually has to
achieve. Per [ADR 0003](./0003-embed-occu-extracts.md), the embedded CCU
metadata archives carry the eQ-3 HomeMatic Software License: free for private,
non-commercial use, commercial redistribution only with written eQ-3 permission.

So the shipped *binary* already cannot be redistributed commercially. Only the
*source* is unrestricted. The exposure the relicence closes is therefore
specific: someone takes the MIT source, sources the CCU metadata themselves via
`cfg.CCUData.*_path`, and builds a commercial product on it.

## Decision

From **v0.66.0**, the OpenCCU-Loom source tree is licensed
**AGPL-3.0-only**, with a commercial exception granted in writing by the
copyright holder.

- `AGPL-3.0-only`, not `-or-later`. "-or-later" would let a future AGPLv4
  apply automatically and would undermine the exception model, because the
  terms the exception is carved out of could change without the maintainer.
- Commercial users who cannot accept AGPL §13 disclosure obtain a separate
  licence. No public price list; a documented contact route is enough.
- Contributions require a **CLA**, not DCO alone (see Consequences).
- v0.65.0 and every prior release remain MIT. No attempt is made to
  retroactively restrict them, because none would succeed.

## What this achieves — and what it does not

AGPL does not forbid commercial use. It makes it expensive: anyone who conveys
the software, *or offers it to users over a network* (§13 — which lands squarely
on the REST API, the WebSocket plane and the SPA), must release the complete
corresponding source of their derived work under AGPL. For most commercial
actors that is unacceptable, so they come and ask. This is the Grafana /
Nextcloud / pre-SSPL MongoDB model.

The gap, stated plainly rather than glossed: **a provider willing to open-source
their entire stack under AGPL may run OpenCCU-Loom commercially without asking.**
That gap cannot be closed while the licence stays OSI-approved — it is the price
of the label, and it was accepted deliberately. Closing it requires a
non-OSI licence (BSL, PolyForm), which was rejected below.

## Alternatives considered

The two constraints — *block monetisation* and *stay OSI-approved* — admit
exactly one option.

| Option | Blocks monetisation | OSI-approved | Verdict |
| --- | --- | --- | --- |
| **MIT** (status quo) | no | yes | Fails the goal. Protection rests entirely on the eQ-3 data layer, which is someone else's right to enforce. |
| **Apache-2.0** | no | yes | Adds a patent grant and attribution, nothing else. Fails the goal. |
| **AGPL-3.0-only + exception** | de facto | yes | **Chosen.** |
| **BSL 1.1** (MariaDB / HashiCorp) | yes, until the Change Date | no | Strong fit on the merits: source stays readable, production use needs a grant, and each version turns permissive after the Change Date, which defuses the abandonment risk that plain non-commercial licences carry. Rejected only because it is not OSI-approved. |
| **PolyForm Noncommercial 1.0.0** | yes, permanently | no | Cleanest drafted non-commercial text, and it defines "noncommercial" properly instead of leaving it to argument. Rejected on the OSI constraint — and it would also block the in-company self-hosting the maintainer wants to keep allowed. |
| **Elastic License 2.0 / SSPL** | targets managed-service providers | no | Aimed at a threat model (hyperscaler resale) this project does not face. |
| **MIT + Commons Clause** | claims to | no | Rejected on quality, not on the constraints: "Sell" is undefined with respect to support and consulting revenue, and the construct has a poor track record. |
| **Proprietary EULA** (source closed) | yes | no | The maximal option, and the model of the plugin that prompted this review — which works only because it ships binary-only. Applied here it means a private repository and binary-only releases: losing issues, transparency, Dependabot, and the honesty of the name. |

## Licence compatibility

- **Go dependencies** — all direct and indirect modules are permissive
  (MIT / BSD-2 / BSD-3 / Apache-2.0), per
  [THIRD-PARTY-NOTICES.md](https://github.com/SukramJ/openccu-loom/blob/main/THIRD-PARTY-NOTICES.md).
  Permissive terms flow into an AGPL work without friction.
- **Apache-2.0 material** — the matter.js port under `internal/north/matter/`
  and the Home Assistant frontend patterns. Apache-2.0 is compatible with
  **GPLv3/AGPLv3** (it is *not* with GPLv2, which does not affect us). Those
  parts stay Apache-2.0 and keep their NOTICE and modification records.
- **eQ-3 data layer** — untouched. It was always a separate aggregation
  (ADR 0003) rather than a term of the code licence, and the aggregation
  argument holds identically under AGPL.
- **Sibling projects** — aiohomematic and its family are MIT. Code flow becomes
  one-way: MIT material may enter OpenCCU-Loom, but AGPL material may not go
  back. As sole author the maintainer may still relicense his own code in
  either direction; that stops being true for any contributed code.

## Consequences

1. **A CLA becomes mandatory.** A commercial exception can only be granted for
   code whose rights the maintainer holds in full. The first outside
   contribution merged without a CLA blocks the dual-licence model
   permanently for that code. `git commit -s` (DCO) stays, but does not
   substitute — it certifies provenance, it does not assign or licence rights
   back to the maintainer.
2. **§13 has to be implemented, not just declared.** Network users must be
   offered the corresponding source. Concretely: a prominent source link on the
   SPA About page and in the `/api/v1/info` payload — both surfaces already
   carry licence information today.
3. **Ecosystem cost.** Debian, Fedora and nixpkgs accept AGPL, so packaging
   stays possible; some corporate policies ban AGPL outright even for internal
   use, which is a real if modest reach reduction. The Home Assistant add-on
   route is unaffected.
4. **The name stays truthful.** "OpenCCU-Loom" remains accurate under AGPL. It
   would have become contestable under BSL or PolyForm.
5. **Governance overhead returns.** ADR 0001 rejected dual-licensing as
   "disproportionate governance overhead for a solo project". That assessment
   was correct and is not being retconned — the overhead (CLA bot, an exception
   template, answering requests) is now accepted as the price of the goal
   rather than judged to be zero.
6. **The MIT history is permanent.** Anyone may fork v0.65.0 and carry it
   commercially forever. This is a known, accepted, unavoidable outcome.

## Why this was proposed and not accepted

The decision above is deliberately held rather than taken. Weighed against
what the repository actually looks like, the case for acting now is weak:

- **The risk is hypothetical, the cost is immediate.** 0 forks, 4 stars,
  three months old. There is no incident, no enquiry, no observed
  appropriation. Relicensing would insure against something that has not
  happened, and pay for it in reach.
- **The barrier is already there.** Per [ADR 0003](./0003-embed-occu-extracts.md)
  the shipped binary cannot be redistributed commercially: the embedded CCU
  metadata is eQ-3 non-commercial. Anyone wanting to build a commercial
  product on the MIT source must source that metadata themselves — which is
  years of curation work.
- **Reach is the scarce resource at this size, not protection.** AGPL is
  banned outright by some corporate policies, including for internal use —
  the very use this project wants to keep permitted.
- **The ecosystem cost is real and daily.** aiohomematic and its family are
  MIT. Under AGPL, code flows one way only, and that friction lands on a
  solo maintainer moving between five repositories.

None of this argues that the decision is wrong — only that it is early. The
asymmetry cuts the other way too: AGPL can be relaxed to MIT at any time,
MIT can never be tightened retroactively.

## Revisit when

Any one of these should reopen this ADR. They are recorded so the decision
is deferred rather than forgotten:

1. **Someone forks and rebrands, or OpenCCU-Loom turns up inside a
   commercial product.** The threat stops being hypothetical, and the
   argument above loses its main premise.
2. **The maintainer wants to monetise** — support, hosting, a paid variant.
   Dual licensing is the foundation that has to exist first; it cannot be
   retrofitted onto contributions already merged.
3. **Adoption grows visibly** — three-digit stars, several forks. Both the
   exposure and the price of a later switch rise together, and the window
   that is wide today narrows from then on.

**What the second trigger costs.** Relicensing, and selling a commercial
exception, both require holding the rights to all the code. That ability ends
the first time an outside contribution is merged without a contributor licence
agreement, and no later decision can undo it — the DCO sign-off does not
substitute, it certifies where code came from and grants no rights.

Such an agreement was drafted for all eight repositories in August 2026 and
**deliberately not adopted**: there is no plan to sell commercial licences, and
without that it secures an option nobody intends to exercise, at the price of a
contribution barrier on projects whose scarcer resource is contributors. The
reasoning and both drafts are kept in
[`notes/reference/contributor-agreements-declined.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/reference/contributor-agreements-declined.md).

The consequence for this ADR is specific and accepted: **trigger 2 stops being
actionable once outside contributions arrive.** Triggers 1 and 3 — a fork or a
commercial appropriation, and growth in adoption — remain fully actionable,
because a move to AGPL for the maintainer's own code needs nobody's consent
where the project is otherwise permissively licensed. What is lost is the
commercial-exception model, not the ability to change licence.

## Implementation notes

Mechanical, once this ADR is accepted:

- `LICENSE` → AGPL-3.0 text; keep the pointer to
  `internal/ccudata/embedded/NOTICE` for the eQ-3 layer.
- `SPDX-License-Identifier: MIT` → `AGPL-3.0-only` across ~3 450 files
  (scriptable; the SPDX identifier is standard, so tooling keeps working).
- `THIRD-PARTY-NOTICES.md`, README badge, `docs/developer/adr-index.md`,
  `mkdocs.yml` nav.
- Both add-on manifests under `packaging/ha-addon/*/config.yaml`.
- `CHANGELOG.md` under the v0.66.0 heading — this is a user-visible change of
  the first order.
- `CONTRIBUTING.md`: CLA requirement and the commercial-licence contact route.
- New: source-offer link in the SPA About view and `/api/v1/info` (§13).

## Open questions — for legal review before acceptance

- Exact wording of the commercial exception: what it grants, what it withholds,
  whether it is per-version or perpetual.
- CLA text and mechanism (CLA-assistant bot vs. signed document), and whether
  it assigns copyright or takes a broad licence-back. A licence-back is the
  lighter instrument and is normally sufficient for granting exceptions.
- Whether the exception should be offered free-of-charge on request or priced.

The mechanical work can be prepared in full ahead of that review; none of it
should land before the wording is settled.
