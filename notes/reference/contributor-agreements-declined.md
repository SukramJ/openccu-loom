# Contributor agreements — examined and declined

A contributor licence agreement was drafted for all eight Go repositories in
August 2026 and then **not adopted**. This note records what it was for, why
it was dropped, and what it would cost to revive it, so the question does not
have to be worked out from scratch a second time.

The decision in force: **DCO only** (`git commit -s`), no CLA.

## What it was for

A CLA bundles the rights to contributed code in one person. That matters for
exactly one thing: granting a licence that departs from the public one —
relicensing the project, or selling a commercial exception alongside it. The
window closes on its own: the first outside contribution merged without an
agreement makes any of that impossible for that code, permanently, and no
later decision can reopen it. A DCO sign-off does not substitute; it certifies
where code came from and grants no rights.

## Why it was dropped

Because the thing it protects was given up. There is no plan to sell
commercial licences or exceptions, and without that, the agreement secures an
option nobody intends to exercise — at the price of a contribution barrier on
projects whose scarcer resource is contributors, not protection.

The comparison that settled it: Mastodon and Grafana are both AGPL-3.0.
Grafana has a CLA, Mastodon does not. The difference is not the licence, it is
that one sells and the other does not. Linux runs on DCO alone and can never
relicense — which is fine, because it does not want to.

Three drafting rounds also showed how the document behaves under legal review:
each round asked for defensible precision, each request was sound, and the
text grew from 200 words of intent to 3,500 words carrying § 31a UrhG and
§ 307 BGB. Cutting it back to 874 words was possible, but the direction of
travel is instructive — a CLA is not a small permanent fixture.

## What reviving it would cost

The drafts below are complete and reviewed twice. Reviving means: adapting the
Scope table, deciding the three points that were never settled — the
pseudonymous maintainer, the § 32a exposure, standard-terms control under
§§ 305 ff. BGB — and having a German copyright lawyer sign off. It does **not**
mean starting over.

What cannot be revived is coverage of contributions merged in the meantime.
Their authors would have to be asked individually, or their code removed.

This is the same constraint [ADR 0066](../../docs/adr/0066-relicense-agpl-commercial-exception.md)
records for its second trigger: wanting to monetise. Without a CLA in place,
that trigger stops being actionable once outside contributions arrive.

---

# Draft A — short form (v1.0, 874 words)

The version that would have gone into force. Roughly the length of the Apache
ICLA; four lines in the pull-request template.

### Contributor License Agreement

**Version 1.0.** By ticking the CLA box in a pull request you accept this
agreement for that and your future contributions to the repositories listed
under [Scope](#scope).

### What this is for

These projects are maintained by one person. This agreement keeps the rights
in one place, so the licence can be changed later — or a commercial licence
granted alongside the public one — without tracking down every past
contributor. That option ends permanently at the first contribution merged
without it.

It takes nothing away from you. You keep full ownership of your contribution
and may use, publish and relicense it anywhere else. What you grant here is an
additional, parallel permission.

### 1. What you grant

You grant the Maintainer — the person operating the GitHub account
[`SukramJ`](https://github.com/SukramJ), reachable at `sukramj@icloud.com`,
who will disclose their civil name on legitimate request — a
**non-exclusive**, worldwide, perpetual, irrevocable right to use your
contribution, with the right to grant sublicences.

German law reads a grant narrowly where the modes of use are not named
(§ 31(5) UrhG), so specifically: **reproduction** (§ 16), **distribution**
(§ 17, including in compiled binaries and container images), **making
available to the public** (§ 19a), **adaptation and translation** (§ 23),
**inclusion in combined works**, **sublicensing**, **transfer** together with
the project, and **distribution under any licence terms the Maintainer
chooses — including proprietary and commercial ones**.

Two limits:

- The Maintainer can only pass on rights actually held. This grants nothing
  over third-party material, upstream projects or copyleft-licensed code, and
  is no promise that a project can be relicensed as a whole.
- It does **not** cover modes of use unknown today (§ 31a UrhG); those need a
  separate written agreement.

The grant takes effect when you submit and stays in effect whether or not your
pull request is merged — that keeps the rights chain complete for code that
was reviewed or partly adopted. It does not allow the Maintainer to publish or
sell a rejected contribution, only to keep history, records and backups.

**No payment is made.** This does not waive statutory rights you cannot waive,
in particular under §§ 32, 32a and 32c UrhG. Beyond those, this agreement
gives you no contractual claim to a share of any proceeds.

### 2. Patents

You grant the Maintainer and everyone who receives a project from them a
perpetual, worldwide, royalty-free, irrevocable licence under any patent
claims you own or may license that your contribution necessarily infringes,
alone or combined with the project you submitted it to. If you sue over such a
patent, that patent licence ends — the copyright grant above does not.

### 3. What you confirm

That the contribution is your own work, or that you otherwise hold the rights
to grant what is granted above; that you have named any third-party material
and its licence in the pull request; and that it does not, to your knowledge,
infringe anyone's rights. Where the contribution is not protected by
copyright, you grant the same permissions so far as the law allows.

If you wrote it at work, for a client, or using someone else's material, get
their permission first. **Do not accept this agreement on behalf of an
organisation** — ask the Maintainer for the entity version.

You give no warranty for the contribution itself; it is provided as-is. You
are liable only for an intentional or grossly negligent breach of the
confirmations above — an honest mistake made with reasonable care is not one.
Liability for injury to life, body or health and under the Product Liability
Act is unaffected.

### 4. Attribution

Copyright and moral rights stay with you; German law does not allow their
transfer (§ 29(1) UrhG). You accept that attribution may be collective — a
contributors file, the commit history — and not repeated in every artefact.
The Maintainer will preserve attribution where practicable and will not
attribute your work to someone else.

### 5. Versions, law, language

You are bound by the **version you accepted**. A later version applies only if
you accept it, and never retroactively.

German law applies, without its conflict-of-law rules. If you are a consumer,
this does not deprive you of mandatory protection where you live, and the
statutory rules on jurisdiction apply. English is the contract language; if a
German version is published, the English one prevails except where that would
remove mandatory consumer protection.

If a provision is invalid, the rest stands.

### Scope

| Repository |
| --- |
| [`openccu-loom`](https://github.com/SukramJ/openccu-loom) · [`go-mqtt`](https://github.com/SukramJ/go-mqtt) · [`godevccu`](https://github.com/SukramJ/godevccu) |
| [`go-daikin2mqtt`](https://github.com/SukramJ/go-daikin2mqtt) · [`go-homeconnect2mqtt`](https://github.com/SukramJ/go-homeconnect2mqtt) |
| [`go-mtec2mqtt`](https://github.com/SukramJ/go-mtec2mqtt) · [`go-unifi2mqtt`](https://github.com/SukramJ/go-unifi2mqtt) · [`go-zendure2mqtt`](https://github.com/SukramJ/go-zendure2mqtt) |

Each repository states its own licence, and upstream terms attaching to
material in it are untouched by this agreement — in `go-mtec2mqtt`,
`registers.yaml` stays under LGPL-3.0-or-later.

### How acceptance is recorded

One acceptance covers every repository above and every later contribution
under that version. Before your first merge the Maintainer adds a row to
[`CLA-SIGNATURES.md`](https://github.com/SukramJ/openccu-loom/blob/main/CLA-SIGNATURES.md)
in a signed-off commit, recording your GitHub handle and account id, the
version and hash of this file, the pull request and the date. There is no bot.

---

*A long-form version of this agreement, with the reasoning and the open
questions behind each clause, is kept at
[`notes/reference/cla-long-form.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/reference/cla-long-form.md).*

---

# Draft B — long form (v0.4, 3,500 words)

The version after three review rounds, with the reasoning and the ten open
questions behind each clause. The starting point if the question is ever
reopened.

The Projects listed in [Scope](#scope) are maintained by a single person.
Keeping the rights in one place preserves one specific option: to change the
licence of a Project later, or to grant a commercial licence alongside the
public one, without having to track down every past contributor for consent.

That option is lost permanently the first time a contribution is merged
without it: from then on, any change of licence would require the consent of
every contributor whose code is still present — including those who have long
since moved on and cannot be reached.

This agreement does **not** take Your rights away. You keep full ownership of
Your Contribution and may use, publish and relicense it however You like,
anywhere else. What You grant here is an additional, parallel permission. You
take on no obligation to contribute again, no exclusivity, and no say over the
Maintainer's later licensing decisions.

### 1. Definitions

- **"Maintainer"** — the natural person operating the GitHub account
  [`SukramJ`](https://github.com/SukramJ), reachable at `sukramj@icloud.com`,
  who owns or controls the rights held by the Maintainer in the Projects, and
  who is identified by civil name and address in the confidential identity
  record `[record id / hash]` deposited with `[custodian]` on `[date]`. The Maintainer publishes under that pseudonym
  (§ 13 UrhG) and will disclose the identity record on request to anyone with
  a legitimate interest — in particular to any contributor asserting rights
  under this agreement, to any prospective commercial licensee, and wherever
  the law requires it.

  *(Draft note — to be removed before this agreement is put into force. The
  identity record must exist first: civil name, address, the GitHub handle and
  numeric account id, the operative e-mail address, a statement that this
  person is the Maintainer and rights holder, the agreement version and date,
  the Maintainer's signature, and the custodian's. A lawyer, a notary or a
  permanently controlled encrypted archive is preferable to a private
  individual as sole custodian; two independent copies are better than one.)*
- **"Project"** — any repository listed in [Scope](#scope).
- **"You"** / **"Contributor"** — the natural person accepting this
  agreement. Contributions on behalf of an employer or client are governed
  by §8.
- **"Contribution"** — a work of authorship that You intentionally submit to
  a Project **for inclusion in, or consideration for inclusion in, that
  Project** — a pull request, a patch, or an attachment offered as such. Code
  posted in an issue or a discussion is a Contribution **only** if You
  identify it as submitted under this agreement. Whether the Contribution is
  ultimately merged does not matter.

### 2. Grant of rights in the Contribution

You grant the Maintainer the **non-exclusive** rights of use described below
in Your Contribution — worldwide, for an unlimited period and territory, with
the right to grant sublicences, and **transferable in connection with a
transfer, sale, merger, succession or comparable disposition of the relevant
Project or of substantially all assets relating to it**.

The grant is non-exclusive on purpose: You retain every right in Your own
work and may exploit it elsewhere without asking.

Because German law construes a grant narrowly where the modes of use are not
named individually (§ 31(5) UrhG), the rights are granted for the modes of use
expressly listed below, and for technically equivalent implementations of
those listed modes insofar as they do not constitute an unknown mode of use
within the meaning of § 31a UrhG:

1. **Reproduction** (§ 16 UrhG) in any form and on any medium, in source and
   in object form.
2. **Distribution** (§ 17 UrhG), including as part of compiled binaries,
   container images and firmware images.
3. **Making available to the public** (§ 19a UrhG), including via source
   repositories, package registries and module proxies.
4. **Adaptation, modification, translation and other transformation**
   (§ 23 UrhG), and exploitation of the results.
5. **Inclusion in collected and combined works** with material of the
   Maintainer or of third parties.
6. **Distribution under licence terms freely chosen by the Maintainer**,
   including open-source licences other than the one currently applied to the
   Project, and including **proprietary and commercial licence terms**.
7. **Sub-licensing** through any number of tiers, and **transfer** of the
   granted rights to a legal successor or acquirer of the Project.

**What §2(6) does not mean.** The Maintainer may license *Your Contribution*,
and a Project *insofar as the Maintainer holds the necessary rights*, under
terms of their choosing. This agreement grants no rights the Maintainer does
not otherwise hold, and it does not affect rights or obligations arising from
third-party material, upstream projects, or copyleft licences. It is not a
warranty that any Project can be relicensed as a whole.

**Modes of use not yet known.** The grant does **not** extend to modes of use
unknown at the time of acceptance within the meaning of § 31a UrhG. Such modes
of use require a separate agreement in written form; accepting this agreement
online does not satisfy that requirement.

**When the grant takes effect.** The grant takes effect upon submission and
remains effective whether or not the Contribution is merged. This is
deliberate — it keeps the rights chain complete for code that was reviewed,
adapted or partially adopted — and it is stated in the pull-request checkbox
so that it is not a surprise.

It does **not**, however, authorise the Maintainer to publish or commercially
exploit a Contribution that was rejected, except so far as necessary to
preserve repository history, review records, backups and legal records, or to
continue exploiting derivative work already created during review.

### 3. Consideration and statutory rights

No payment is made for the Contribution. The parties acknowledge that the
Contribution is ordinarily made in the context of a public open-source project
and without an upfront payment, the consideration in mind being the
opportunity to participate in the development and public distribution of the
Project. The parties do not intend that opportunity to constitute a
contractual royalty, a profit share or an employment relationship. This
statement describes the parties' present intention; it does not waive or limit
any statutory remuneration or information right.

**Nothing in this agreement excludes or limits Your non-waivable statutory
rights**, in particular under §§ 32, 32a and 32c UrhG. This agreement does not
purport to settle in advance any claim to equitable remuneration those
provisions may give You.

**Except for non-waivable statutory claims**, this agreement creates no
contractual entitlement to a share of revenue, royalties, licence fees or
other proceeds received by the Maintainer. Whether a statutory claim exists in
a given case is a question those provisions answer, not this agreement.

### 4. Patents

You grant the Maintainer, and every recipient who receives a Project under a
licence granted by the Maintainer or another authorised distributor, a
perpetual, worldwide, non-exclusive, royalty-free and irrevocable licence
under any patent claims You own or are authorised to license that are
necessarily infringed by Your Contribution alone, or by the combination of
Your Contribution with the Project — or a substantially unmodified version of
it — to which You submitted it. The licence covers making, using, selling,
offering for sale, importing and otherwise transferring the Contribution and
that Project.

This licence extends only to patent claims You own or are authorised to
license. It says nothing about patents held by third parties.

If You bring patent litigation against the Maintainer or any recipient
alleging that a Project or a Contribution constitutes patent infringement,
any patent licence granted to You under this section terminates as of the
date that action is filed. That termination reaches **only** the patent
licence granted under this section; it does not by itself end any copyright
or other right granted under this agreement.

### 5. Moral rights

German law permits neither the transfer of copyright itself (§ 29(1) UrhG) nor
of moral rights, and nothing here purports to do either. § 13 UrhG leaves the
recognition of Your authorship with You.

To the extent permitted by law, You consent to the forms of attribution — and
of omitted attribution — reasonably required for the development, distribution
and relicensing of the Project: in particular that attribution may be given
collectively, for example in a contributors file or the commit history, and
not necessarily in every distributed artefact.

In return, the Maintainer will use reasonable efforts to preserve attribution
in the repository history or in a contributors file wherever that is
technically and operationally practicable, and will not knowingly attribute
Your Contribution to another person.

### 6. Your assurances

You represent that:

1. The Contribution is **Your own original creation**, or You otherwise hold
   all rights necessary to make the grants above.
2. You have **disclosed any third-party material** contained in the
   Contribution, naming its source and its licence, in the pull request. You
   cannot grant more than You hold; third-party material remains under its own
   licence.
3. The Contribution does not, to Your knowledge, infringe the rights of any
   third party.

Where a Contribution is not protected by copyright, You grant the Maintainer
all rights and permissions necessary to use it on the same terms, so far as
the law allows.

### 7. Warranty and liability

**The Contribution itself.** So far as the law permits, You give no warranty
as to the quality, functionality, fitness for a particular purpose or
non-infringement of the Contribution. It is provided as-is.

**The assurances in §6.** You are liable to the Maintainer for losses caused
by an intentional or grossly negligent breach of those assurances — in
particular where You knowingly submit third-party material without the
required permission, or knowingly misrepresent Your authority to grant the
rights. Liability for simple negligence is excluded so far as the law permits,
and an honest mistake made despite reasonable care does not trigger liability
under this paragraph unless mandatory law provides otherwise.

Liability for injury to life, body or health, liability under the Product
Liability Act, and any other liability that cannot be excluded by agreement
remain unaffected in every case.

### 8. Contributing for an employer or client

If a Contribution is created in the course of employment, under an obligation
to a client, or using material owned or controlled by another person, **You
must obtain every permission needed to make the grants in this agreement
before You submit it**. On request, You will identify the employer, client or
third-party restriction concerned.

This agreement is for **natural persons acting for themselves**. Do not submit
material on behalf of an organisation unless You are authorised to bind that
organisation and a separate Entity CLA has been accepted by an authorised
representative. This agreement cannot supply rights Your employer holds, and
accepting it does not bind anyone but You.

### 9. No obligation

The Maintainer is under no obligation to use, merge or distribute Your
Contribution, and this agreement does not oblige You to contribute again.

### 10. Versions of this agreement

Each version of this agreement carries a version number and is archived
unchanged, so that it stays possible to establish what exactly was accepted
and when.

Your acceptance binds You to the **specific version** You accepted. A later
version applies to You only if You expressly accept that version, and
accepting a later version does not retroactively alter rights granted under
an earlier one unless expressly agreed in writing.

### 11. Governing law and jurisdiction

This agreement is governed by the law of the Federal Republic of Germany,
excluding its conflict-of-law rules and the UN Convention on Contracts for
the International Sale of Goods. Where You are a consumer, this choice of law
does not deprive You of the protection of mandatory provisions of the law of
Your habitual residence.

If You are a merchant within the meaning of the German Commercial Code, or
are otherwise acting in the course of business, the courts at the Maintainer's
place of residence have jurisdiction so far as the law permits. In every other
case, the statutory rules on jurisdiction apply. Nothing in this clause
restricts mandatory rules on jurisdiction that protect consumers or other
protected persons.

If any provision is or becomes invalid, the remainder stays in force.

### 12. Language

English is the working and contract language of the Projects. Should a German
version of this agreement be published, it is intended to carry the same
meaning as the English version. In the event of an inconsistency the English
version prevails — except so far as that would deprive a consumer of mandatory
statutory protection under the law of their habitual residence.

### Scope

This agreement applies to contributions to all of the following repositories:

| Repository | Current licence |
| --- | --- |
| [`SukramJ/openccu-loom`](https://github.com/SukramJ/openccu-loom) | MIT |
| [`SukramJ/go-mqtt`](https://github.com/SukramJ/go-mqtt) | MIT |
| [`SukramJ/godevccu`](https://github.com/SukramJ/godevccu) | MIT |
| [`SukramJ/go-daikin2mqtt`](https://github.com/SukramJ/go-daikin2mqtt) | MIT |
| [`SukramJ/go-homeconnect2mqtt`](https://github.com/SukramJ/go-homeconnect2mqtt) | MIT |
| [`SukramJ/go-unifi2mqtt`](https://github.com/SukramJ/go-unifi2mqtt) | MIT |
| [`SukramJ/go-zendure2mqtt`](https://github.com/SukramJ/go-zendure2mqtt) | MIT |
| [`SukramJ/go-mtec2mqtt`](https://github.com/SukramJ/go-mtec2mqtt) | project-specific; `registers.yaml`, and any other material legally covered by the applicable upstream licence, remains subject to that licence |

**The licence column is informational only.** It does not grant, change or
determine anything. Which licence applies to a given file or component follows
from that component's provenance, its copyright notices, the licence files in
the repository, and any applicable upstream terms — never from this table.

**Upstream rights are untouched.** Nothing in this agreement authorises the
Maintainer to remove, restrict or circumvent rights and obligations attaching
to upstream works or to third-party material. In `go-mtec2mqtt` in particular, `registers.yaml` remains subject to
LGPL-3.0-or-later, as recorded in that repository's own `LICENSE.registers`
and `NOTICE.md`; nothing here permits relicensing it contrary to that licence.
This agreement records no independent finding about the provenance of any
file — that follows from the repositories' own notices and history. The grants in §2 remain effective for Your own
material, subject to those upstream rights and obligations. Which code an
upstream licence actually covers is determined by the origin of that code, not
by a note in a repository.

### How to accept

Open a pull request and tick the CLA box in the pull-request template. That box
states that You have read this agreement, in the version named there, and
accept it for this and Your future Contributions.

Before merging Your first Contribution, the Maintainer adds a row for You to
[`CLA-SIGNATURES.md`](https://github.com/SukramJ/openccu-loom/blob/main/CLA-SIGNATURES.md)
in a signed-off commit of its own, recording Your GitHub handle and numeric
account id, the agreement version and its file hash, the pull request, the
commit of the Contribution, and the date.

That commit is the record, not the tick box: a pull-request description can be
edited afterwards and leaves no trace in this repository, whereas a commit is
content-addressed and any change to it is visible as a different commit. The
record also stays in the repository rather than in a third-party service.

It is **not** represented as tamper-proof: a repository history can be
rewritten or force-pushed. The Maintainer undertakes to preserve the recorded
commit and the surrounding history, not to rewrite or remove a CLA record, to
keep `main` protected against force-pushes, and to hold an off-site backup. It
The acceptance record may be supplemented by the preserved pull-request
record, GitHub audit information, repository backups and other contemporaneous
evidence, and the Maintainer keeps a copy of each agreement version outside
the repository as well.

It is evidence of acceptance; it is not a qualified electronic signature and
is not treated as one.

One acceptance of a given version covers every repository in
[Scope](#scope) and every later Contribution You make under that version. You
will not be asked again unless the agreement changes (§10).

Acceptance is a prerequisite for a Contribution being merged. There is no bot:
the Maintainer checks by hand, which at this project's size is a two-minute
step and avoids granting a third-party workflow write access to the
repositories.

---

### Open points for legal review

Prepared for the reviewing lawyer. Points 1–3 are the ones that most need a
decision; the rest are drafting questions.

1. **Pseudonymous Maintainer (§1).** §1 now points at a signed confidential
   identity record rather than relying on the handle alone; custodian and date
   are still to be filled in, and the record itself needs drafting. Confirm
   that this construction gives sufficient certainty about who the contracting
   party and rights holder is, or advise a named legal entity instead. Note
   that commercial licensing is likely to trigger the disclosure duty under
   § 5 DDG in any event.
2. **§§ 32, 32a, 32c UrhG (§3).** The draft no longer claims the grant is
   simply "free of charge"; it names the consideration and expressly
   preserves non-waivable statutory rights. Confirm this is the right
   posture, and assess the residual exposure under § 32a where the Maintainer
   later earns revenue from commercial licences.
3. **Standard-terms control (§§ 305 ff. BGB).** This is a pre-formulated
   agreement intended for repeated use, so it will likely be measured against
   § 307 BGB — and against § 309 BGB where the Contributor is a consumer.
   Review §2 (breadth), §7 (liability) and §11 (jurisdiction) for transparency
   and for unreasonable disadvantage.
4. **Online acceptance and § 31a UrhG.** Unknown modes of use are expressly
   excluded because a tick box does not meet the written-form requirement.
   Confirm this trade-off against requiring a signed document, and confirm
   that the acceptance record described under *How to accept* is adequate
   evidence — including how long it must be retained.
5. **Non-exclusive rather than exclusive (§2).** Chosen to keep the barrier to
   contributing low; sufficient for dual licensing. Confirm no scenario
   requires exclusivity.
6. **Entity contributions (§8).** The draft excludes acceptance on behalf of
   an organisation and requires a separate agreement. Advise whether a
   corresponding Entity CLA should be drafted now or the exclusion is
   sufficient for a project of this size.
7. **Patent termination (§4).** Modelled on the Apache-2.0 pattern. Confirm
   the termination trigger and its scope.
8. **A German-language version (§12).** The precedence rule is in place; the
   German version itself is not written. Advise whether one is needed for
   enforceability against contributors resident in Germany, given that an
   English-only standard term is more open to challenge where the counterparty
   could not reliably grasp its meaning.
9. **Personal data in the acceptance record.** GitHub handles and account ids
   are personal data. Advise on the lawful basis, the retention period, and
   erasure obligations against the need to keep the record for as long as the
   code is in use.
10. **Effect on unmerged Contributions (§2).** The grant takes effect on
   submission and survives rejection, which keeps the rights chain complete
   but is broad. Confirm this is defensible under § 307 BGB, given that it is
   disclosed in the acceptance checkbox rather than buried.
