# M2 — the ratchet audit

82 entries across 16 ratchet maps, every one of them read against the code it
claims something about. Verdicts: **58 decision · 1 stale · 15 deferral ·
8 false**.

Per-map audit tables: `scratchpad/m2/reports/{setters,mcp,rest,events,small}.tsv`.

The headline is not the 58. It is that **23 of 82 entries (28 %) are either an
open finding wearing a decision's face or a justification that is factually
wrong**, on a surface that four full-codebase audit rounds could not see —
because everything in it is marked as known.

---


## Caller verification and what was acted on

Every finding acted on below was re-checked by hand first; this pass has twice
produced a confident claim that did not survive reading the code.

**Verified and fixed:**

- the one **stale** entry (`httpClientOwnershipExempt`) — removed. Negative
  control: with it gone, dropping `Transport` from `httpx.NewClient` now fails
  the guard, which it could not while the file was exempt.
- **eight false reasons**, all verified individually. Five were text in
  `restDomainsWithoutMCPTools` and are rewritten to what the code does. Two —
  `north.rest.auth.users` and `.tokens` — were not a wording problem: the YAML
  maps are re-read into an in-memory secondary on every boot, so an edit
  reaches nothing until restart. Both leaves now carry a restart rule and the
  exemptions are gone. The eighth, `RPCParameterReceivedEvent`, is worse than
  reported: its publisher has no production caller either, so the event does
  not occur in a running daemon at all.
- both **Tier-2 deferrals** — `SetLoadAndRefreshForInterfaceFn` and
  `SetParamsetInvalidator`. Investigated, and the answer was delete rather than
  build: the first feeds a method with no caller, and the WS reload commands
  are device- and channel-scoped, so the interface granularity was never asked
  for; the second duplicates an eviction production already performs per device
  and per channel. Both chains removed with their tests.

**Entry count: 82 → 77.**

## 1. The free tightening

One entry, one map.

### `httpClientOwnershipExempt` — `tests/contract/http_transport_ownership_test.go:58`

```go
var httpClientOwnershipExempt = map[string]string{
	// internal/httpx is the helper that supplies the transport; the
	// client it returns carries one by construction.
	"internal/httpx/transport.go": "constructs the owned transport itself",
}
```

**What changed:** the reason describes a file that could not satisfy the guard.
It now does. `internal/httpx/transport.go:36` is the file's only `http.Client`
literal and it sets the field the guard demands:

```go
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: NewTransport()}
}
```

The file contains no `http.DefaultClient` and none of the
`http.Get/Post/Head/PostForm` wrappers the guard also rejects
(`http_transport_ownership_test.go:31-35`).

**Negative control** (the check that makes this a finding rather than a guess):
the guard's exact AST checks, replicated in scratch, yield **0 offenders** on
`internal/httpx/transport.go` and **3** on a synthetic file carrying a bare
literal, a `DefaultClient` reference and an `http.Get`. The checks fire when the
defect is present and stay silent when it is not, so "the exemption exempts
nothing at HEAD" is verified, not assumed.

**Quantified:** 1 of 82 entries (1.2 %) can be deleted at zero cost. That empties
the map completely — 1 → 0 — and the map's own header says
(`http_transport_ownership_test.go:56`) *"Keep it empty unless there is a genuine
case."* What the guard starts covering is not incidental: the newly guarded file
is `internal/httpx` itself, the helper every other call site is told to use
(`internal/httpx/transport.go:33`, "Prefer this over `&http.Client{Timeout: d}`
everywhere"). A regression in the one file that defines the correct pattern is
the regression that propagates.

Nothing else is stale. Every other exemption still describes a condition that
holds — which is the uncomfortable part: the frozen surface is frozen because it
is still true, not because nobody has moved.

---

## 2. The deferrals — open findings in disguise

15 entries. Ranked by what the unguarded gap costs if the exemption is wrong or
becomes wrong, not by how many entries sit in a map.

### Tier 1 — a wrong exemption that already falsifies a second one

**1. `restDomainsAwaitingMCPTools["interfaces"]`** — `mcp_rest_parity_test.go:152`,
*"3 routes: per-interface state and reconnect"*.
3 routes (`internal/north/rest/router.go:1318-1320`), no `interface` tool among
the 30 registered; `list_centrals` returns names only. The cost is not only that
an assistant cannot see interface reachability or trigger a reconnect. It is that
the *other* map leans on this ground being covered: `restDomainsWithoutMCPTools["snapshot"]`
(`mcp_rest_parity_test.go:124`) is justified with *"the per-domain read tools cover
the same ground"*, while `snapshot.go:76-83` aggregates Devices + **Interfaces** +
Hub. One backlog item makes a *decision* untrue.
**Needs:** a fix (the `interfaces` read tool), which retires both entries. A guard
cannot express "these two maps must not contradict each other" cheaply;
`TestMCPExemptionsAreStillReal` (`mcp_rest_parity_test.go:86-109`) already checks
that a domain is not in both maps, but not that one map's reason depends on the
other's backlog.

### Tier 2 — plumbing that looks present and is a no-op

**2. `CacheCoordinator.SetParamsetInvalidator`** — `wiring_setter_callers_test.go:41`,
*"no type implements ParamsetInvalidator and InvalidateParamsetDescriptions has no
caller; bulk per-interface eviction is unbuilt"*. All three facts hold
(`internal/central/coordinators/cache.go:293`). The word doing the damage is
**"unbuilt"**: the entry records an unanswered build-or-delete question as though
it were an answer. The interface + setter + method trio reads as a working
eviction path. If per-interface paramset eviction is ever needed — the obvious
trigger is a firmware update reshaping paramsets — the plumbing is there and does
nothing, and stale descriptions persist silently.
**Needs:** a decision. Build it or delete the trio; do not leave a third state.

**3. `Unit.SetLoadAndRefreshForInterfaceFn`** — `wiring_setter_callers_test.go:39`,
*"the method it feeds has no callers at all"*. True
(`internal/central/central.go:1453`). This is precisely the shape the map's own
header disqualifies (`wiring_setter_callers_test.go:24-26`): *"a seam kept for tests
belongs deleted, because a setter only tests call is a setter production does not
need."* A dead scoped-reload API advertises a capability that does not exist; a
future caller wiring only the method gets the global-refresh fallback semantics
without noticing.
**Needs:** a decision (delete or wire), not a guard.

### Tier 3 — bracketing tests, the defect CLAUDE.md names by name

**4. `Unit.SetSaveFilesFn`** — `wiring_setter_callers_test.go:40`. `Unit.SaveFiles`
(`internal/central/central.go:1210`) is called only from
`internal/central/central_service_hooks_test.go:129,141`.
**5. `Unit.SetValidateConfigFn`** — `wiring_setter_callers_test.go:41`.
`ValidateConfigAndGetSystemInformation` (`internal/central/central.go:1229`), same
shape, same test file.

Both reasons say *"no callers outside tests"* — and the map header says a
tests-only seam **belongs deleted, not exempted**. The exemption is being used
against the rule it sits under. What survives is a green test proving a
collaboration production never makes: the bracketing test, verbatim from
`CLAUDE.md` § *Wiring*.
**Needs:** a fix (delete seam + hook + the tests that certify them), and — because
this shape was admitted twice — a tightening of the *admission* rule so
"outside tests" is a rejected reason rather than an accepted one.

### Tier 4 — dead surface a reader must keep re-auditing

**6. `QueryFacade.SetHubStatePathProvider`** — `wiring_setter_callers_test.go:36`.
`GetStatePaths` / `GetStatePathEntries` (`internal/central/queryfacade.go:570,619`)
have zero production callers and no API surface mentions state paths. *"the
combined state-path list is unbuilt"* is again an open build-vs-delete question.
No functional cost; ~150 lines of dead facade every future auditor re-reads, and
one more precedent for "unbuilt" as an accepted reason.
**Needs:** a decision.

**7. `DefaultDiscoveryBuilder.WithTranslations`** — `wiring_setter_callers_test.go:57`,
kept as a test override. Repo-wide grep finds **zero** callers; the
`WithTranslations` calls in `tests/integration` are `DevicePipeline.WithTranslations`
(`internal/central/adapter/device_pipeline.go:280`), a different method. So the
stated purpose — test override — is not exercised by any test.
No functional cost (`NewDefaultDiscoveryBuilder` auto-loads the catalogues); a
decorative API held open by a reason nothing uses.
**Needs:** deletion.

### Tier 5 — the declared MCP backlog (8 remaining)

`restDomainsAwaitingMCPTools`, `mcp_rest_parity_test.go:149-159`. Every reason is
accurate; the map is honestly labelled *"the declared backlog: domains that SHOULD
have tools and do not yet"* (`mcp_rest_parity_test.go:135-136`). Ranked by breadth
of what an assistant cannot reach:

| Domain | Routes | Where | Missing capability |
|---|---|---|---|
| `groups` | 6 | `router.go:1126-1134` | heating-group roster + administration; no `group` tool |
| `areas` | 5 | `router.go:835,1010-1013` | the operator's spatial model |
| `history` | 3 | `router.go:957,960-961` | any recorded measurement series; alias `measurement` matches nothing |
| `visibility` | 3 | `router.go:1241-1243` | surfacing/unhiding filtered parameters |
| `hub` | 1 | `router.go:1292`, `hub_data_points.go:103-121` | per-interface connectivity, install-mode, update state the SPA sees |
| `links` | 1 | `router.go:913` | direct device-to-device links when diagnosing behaviour |
| `schedules` | 1 | `router.go:942` | fleet-wide view; `get_device_schedule` (`tools_hub.go:586`) is per-device |
| `energy` | 1 | `router.go:964` | energy aggregates |

**Needs:** tools, one per row. The map's contract is *"entries here are expected to
disappear"* (`mcp_rest_parity_test.go:145`) — nine were declared, nine are still
there, and no mechanism notices. That is section 4's problem, not this table's.

---

## 3. False reasons

Eight exemptions justified by a statement about the code that is not true. This is
worse than no exemption: an entry with a wrong reason reads as settled, so the next
reader stops at the sentence and never reaches the code.

All eight passed `TestRatchetReasonsAreNotDeferrals`
(`tests/contract/ratchet_reason_purity_test.go`), which is exactly what that guard
promises — it checks wording, never truth.

### `restDomainsWithoutMCPTools` — 5 of 14 entries false (36 %)

1. **`sessions`** (`mcp_rest_parity_test.go:120`) — *"browser session lifecycle,
   meaningless to a token-authenticated client"*. All four `/sessions` routes
   (`router.go:1393-1396`) are the **concurrent-edit session lock**, which MCP
   already projects as `open_edit_session` / `close_edit_session`
   (`internal/north/mcp/tools.go`). The domain is not unprojected; the noun
   `edit_session` merely fails the guard's exact-match. Once an alias exists the
   entry belongs in **neither** map.
2. **`webhook`** (`mcp_rest_parity_test.go:132`) — *"outbound notification
   configuration — config-shaped"*. `router.go:784-785`: both routes are **inbound
   ingestion**. `WebhookInboundValue` writes datapoints, `WebhookInboundProgram`
   triggers programs. A write capability (already reachable via `set_datapoint` /
   `trigger_program`), the opposite of configuration.
3. **`i18n`** (`mcp_rest_parity_test.go:131`) — *"translation catalogue for the SPA; static content"*.
   `internal/north/rest/handlers/i18n.go:12-25,37-45`: `/i18n/entities` exists
   *precisely for non-SPA REST/WS consumers* — the HA integration rendering the
   daemon's entity names (ADR 0046) — and the SPA namespaces `nav.`, `login.`,
   `setup.` are deliberately **excluded** from it. The reason inverts the
   endpoint's purpose.
4. **`me`** (`mcp_rest_parity_test.go:119`) — *"the caller's own session identity"*.
   `router.go:820-822`: the whole `/me` domain is the per-user **preferences store**
   (`Get`/`Put`/`DeletePreference`). `/auth/me` lives in the `auth` domain. The
   exclusion may still be right; the stated reason is about a different endpoint.
5. **`snapshot`** (`mcp_rest_parity_test.go:124`) — *"the per-domain read tools
   cover the same ground"*. `snapshot.go:76-83` aggregates Devices + Hub +
   Interfaces; `interfaces` has **no read tool at all** and sits in the backlog map
   (`mcp_rest_parity_test.go:152`). The claim becomes true when that backlog item
   lands, and not before.

### `eventsWithoutSubscriber` — 1 of 7 false

6. **`RPCParameterReceivedEvent`** (`event_subscriber_coverage_test.go:42`) —
   *"per-parameter wire trace; the value change is carried by
   DataPointValueChangedEvent"*. There is no trace of any kind. The only publish
   site, `EventCoordinator.PublishBackendParameterEvent`
   (`internal/central/coordinators/event.go:248-265`), has **zero production
   callers** — repo-wide grep finds only tests. No wire path publishes the event;
   even its `EventStats` counter stays 0. The method doc's *"Callers invoke this
   before calling HandleRawEvent"* names callers that do not exist, and the empty
   `eventsWithoutPublisher` ratchet is satisfied only by the publish expression
   inside this dead method. Two guards look green on a path nothing walks.

### `configLeavesAppliedWithoutRestart` — 2 of 6 false (33 %)

7. **`north.rest.auth.users`** (`config_restart_required_test.go:108-109`) —
   *"the config map is a one-time seed for a database with no users"*. It is a
   **per-boot credential source**: `buildAuthStores` re-hashes the YAML users into
   the in-memory store on **every** boot (`cmd/openccu-loom/daemon_north.go:89-97`)
   and `loginChainWithCCU` keeps that store as the chain's Secondary
   (`cmd/openccu-loom/ccu_auth_wiring.go:73-76`). A YAML edit mid-run changes
   nothing and takes effect at the next restart — behaviourally restart-required,
   which is the opposite of the map it sits in. Practical exposure is nil only
   because `sectionUnmanagedPaths` strips the field from every save
   (`internal/configstore/store.go:119-124`), so no save can report a false success.
8. **`north.rest.auth.tokens`** (`config_restart_required_test.go:110-111`) — same
   mechanism: `buildTokenMap` re-reads YAML tokens into the memory store every boot
   (`cmd/openccu-loom/daemon_north.go:441-446`), `ChainedTokenStore` keeps it as
   Secondary (`cmd/openccu-loom/daemon_rest.go:162`), and the SQLite seed
   (`daemon_rest.go:111-122`) fires only while the table is empty. Restart-applied,
   not live. Save-path exposure nil via `internal/configstore/store.go:122`.

Entries 7 and 8 are the dangerous kind: the classification is wrong *and* the
protection that makes it harmless lives in a different file, unlinked from the
reason. Delete `sectionUnmanagedPaths` for those paths and the false reason
becomes a live defect with no guard between.

---

## 4. What this says about the ratchet mechanism

**The mechanism is well built in one direction and has nothing at all in the
other.**

Growth is guarded, and visibly so. A new REST domain in neither map fails
`TestMCPCatalogueCoversEveryRESTDomain` (`mcp_rest_parity_test.go:70-80`). A new
setter with no caller fails `TestEveryWiringSetterHasAProductionCaller`. A domain
that vanished from the router fails `TestMCPExemptionsAreStillReal`
(`mcp_rest_parity_test.go:86-97`). Wording that sounds like procrastination fails
`TestRatchetReasonsAreNotDeferrals`. Two maps are deliberately kept apart so that
"we decided against it" and "nobody did it" cannot wear the same face
(`mcp_rest_parity_test.go:138-143`) — the design anticipated exactly this failure.

Shrinking has no mechanism whatsoever. No entry has an owner, an age, or an
expiry. No CI job costs anything as an entry ages. `restDomainsAwaitingMCPTools`
documents itself as *"entries here are expected to disappear"*
(`mcp_rest_parity_test.go:145`) and **not one of its nine has**. The one entry that
became deletable — `httpClientOwnershipExempt` — has been deletable since
`internal/httpx/transport.go:36` was written, and nothing noticed, because a stale
exemption is indistinguishable from a live one to every guard in the repo.

**The ratio, plainly.** 15 deferrals to 58 decisions is 1 : 3.9 overall, and the
average hides where it concentrates:

| Map | Entries | Decision | Deferral | False |
|---|---|---|---|---|
| `restDomainsAwaitingMCPTools` | 9 | 0 | **9 (100 %)** | 0 |
| `restDomainsWithoutMCPTools` | 14 | 9 | 0 | **5 (36 %)** |
| `configLeavesAppliedWithoutRestart` | 6 | 4 | 0 | **2 (33 %)** |
| `wiringSettersWithoutCaller` | 26 | 20 | **6 (23 %)** | 0 |
| `eventsWithoutSubscriber` | 7 | 6 | 0 | 1 (14 %) |
| `routerOpenAPIExemptions` | 9 | 9 | 0 | 0 |
| `authzScopeExemptions` | 4 | 4 | 0 | 0 |
| remainder (5 maps) | 7 | 6 | 0 | 0 (+1 stale) |

Two readings, and both are true. The backlog map is at 100 % deferral **by
construction** — that is the map working as designed, and its honesty is why every
one of those nine was cheap to audit. `wiringSettersWithoutCaller` at 23 % is not
by design: six entries there are open questions filed under a map whose header
says a caller-less seam should be **deleted**. And `restDomainsWithoutMCPTools` —
the map explicitly reserved for *decisions*, `mcp_rest_parity_test.go:114-116`
(*"Each entry is a decision, not a backlog item"*) — is the map with the most wrong
statements in it. **The map that claims the most certainty is the least accurate
one.** That is the finding about the mechanism.

**What would change it,** cheapest first:

1. **Delete the stale entry now.** One line; the map empties and the guard covers
   the file that defines the pattern.
2. **Fix the eight false reasons in the same pass.** A false reason is not a
   bookkeeping error, it is a claim the next reader will trust instead of the code.
   Two of them (`auth.users`, `auth.tokens`) are misclassified in a way that only
   an unrelated file keeps harmless.
3. **Make the shrink direction cost something.** The one mechanical form available:
   assert the size of each backlog map against a committed constant that may only
   be lowered. It cannot force a fix, but it makes every merge that leaves the map
   at nine an explicit, visible choice rather than the default.
4. **Ban "unbuilt" and "outside tests" as admissible reasons.**
   `TestRatchetReasonsAreNotDeferrals` already parses reason text — it currently
   rejects "yet" and "for now". Three of the six wiring deferrals
   (`SetHubStatePathProvider`, `SetParamsetInvalidator`, and both `outside tests`
   entries) would have been rejected at admission by two more tokens. That is the
   negative control this guard is missing: it fires on the words that *sound* like
   deferral and stays silent on the words that *are* one.
5. **Generalise the staleness probe.** `TestMCPExemptionsAreStillReal` proves the
   pattern works. The wiring map deserves the same: assert the exempted setter still
   exists *and* still has no caller, so an entry whose seam got wired fails instead
   of quietly protecting nothing.
6. **Route this surface into the pre-release comment-claims sweep.** `CLAUDE.md`'s
   release checklist already lists "ratchet justifications" as sweep scope. Eight
   false reasons survived every sweep to date — so either the scope line was never
   operative or the sweep never reached these files. Name the map files explicitly
   in the sweep partition.

The one thing that would not help is another guard on *admission*. Admission is
already the well-guarded end. Every finding in this report is about entries that
were admitted correctly and then stopped being true, or were admitted with a
sentence nobody checked. **Ratchets can only shrink if somebody shrinks them —
this audit is the first time anybody has, and steps 3–6 are the only reason it
would not be the last.**
