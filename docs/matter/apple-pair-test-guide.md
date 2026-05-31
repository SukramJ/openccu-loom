# Apple-Pair Test Guide — OpenCCU-Loom Matter Bridge

**Audience:** Anyone (human or AI agent) preparing to test or debug a Matter
pair flow between an Apple commissioner (iPhone / iPad / Apple Home Hub)
and the OpenCCU-Loom Matter bridge. The mDNS-only path goes through Apple
Home App; chip-tool on Ubuntu is the Apple-independent verifier.

**Always test chip-tool first.** Apple is a black-box verifier; chip-tool
reports specific protocol errors. Run the chip-tool brief, fix daemon-side
gaps, then bring Apple into the loop.

---

## 1 — Mental Model

**Apple as commissioner has three faces:**

1. **iPhone / iPad** — opens commissioning window, scans QR / enters
   manual code, drives PASE + AddNOC + CommissioningComplete for **its
   own fabric** (vendor `0x1349`).
2. **Apple Home Hub** (HomePod / Apple TV / iPad designated as hub) —
   automatically re-pairs to its own fabric (vendor `0x1384`) ~15 s
   after the user-driven pair succeeds. This is the **multi-admin
   second AddNOC** — the bridge must accept it without manual scan.
3. **HMHome database** — Apple's local accessory database that ALL
   three (iPhone / iPad / Hub) sync with via iCloud. After commissioning
   the bridge runs ongoing Subscribes from the HMHome layer; HMHome
   caches accessory shape ("HMOutlet", "HMTemperatureSensor", …).

**The most important failure mode is HMHome cache corruption.**
Verified empirically (2026-05-12): after ~20 pair attempts against the
same iPad, the HMHome database accumulates stale per-bridge state that
no daemon-side fix can recover. Either reset the cache (see §6) or
swap to a fresh device.

---

## 2 — Setup

### 2.1 Apple device

- iPad / iPhone running iOS / iPadOS ≥ 16.5 (Matter support landed in 16.4).
- Same WLAN as the OpenCCU-Loom daemon. Both IPv4 + IPv6 enabled. AP
  must NOT enable "client isolation" or "AP isolation" (blocks mDNS).
- iCloud signed in. Apple Home app installed.
- A "Mein Zuhause" / "My Home" must exist already (Apple Home →
  + → "Add Home" if missing).
- Ideally a clean iPad — once the HMHome cache for a given Matter UniqueID
  family rots, no daemon-side `bootid` rotation fully recovers it.

### 2.2 Daemon side

The daemon must rotate its `bootid` salt every restart (already wired
since commit `85a8b1f`); this guarantees that every UniqueID surface
Apple sees is fresh and uncached.

Clean run procedure:

```bash
cd /Users/markus/Documents/GitHub/openccu-loom
go build -o /tmp/openccu-loom-pair ./cmd/openccu-loom/

# 1. Stop any running daemon.
PID=$(pgrep -f 'openccu-loom-pair run' | head -1)
[ -n "$PID" ] && kill $PID && sleep 1

# 2. Clean fabric/identity/resumption state so the next pair starts fresh.
sqlite3 /Users/markus/Documents/GitHub/openccu-loom/var/openccu-loom.db <<EOF
DELETE FROM matter_fabrics;
DELETE FROM matter_node_identities;
DELETE FROM matter_acl_entries;
DELETE FROM matter_group_keys;
DELETE FROM matter_group_key_map;
DELETE FROM matter_resumption;
EOF

# 3. Start daemon with timestamped log.
NEWLOG="/tmp/openccu-loom-pair-logs/daemon-$(date +%Y%m%d-%H%M%S).log"
mkdir -p /tmp/openccu-loom-pair-logs
nohup /tmp/openccu-loom-pair run --config config.yaml > "$NEWLOG" 2>&1 &
until grep -q 'daemon.ready' "$NEWLOG" 2>/dev/null; do sleep 1; done

# 4. Open commissioning window — 600 s is comfortable.
curl -sS -X POST http://localhost:8080/api/v1/matter/commissioning/window \
  -u 'admin:change-me' -H 'Content-Type: application/json' \
  -d '{"duration_seconds": 600}'
# → {"qr_code": "MT:-24J0AFN00KA0648G00", "manual_code": "3497-011-2332", ...}

# 5. Verify the three boot indicators (all must appear in the log):
grep -E 'measurement_listeners_wired|initial_load.central_done|daemon\.ready' "$NEWLOG"
```

Expected log fingerprints after a healthy start:

```
matter.bridge.measurement_listeners_wired registered=N    # N = number of exposable DPs with push
matter.bridge.initial_load.central_done   devices=N loaded=M errored=0
daemon.ready
```

If `registered=0` or `errored>0`, fix that before pairing — Apple gives
zero diagnostic information about it.

### 2.3 Exposable DPs

Pair needs at least one exposed matter device. Confirm via:

```bash
sqlite3 -header /Users/markus/Documents/GitHub/openccu-loom/var/openccu-loom.db \
  "SELECT central_name, device_address, channel_no, dp_kind, dp_key, enabled
   FROM matter_exposures WHERE enabled = 1;"
```

Toggle entries via the SPA UI or the REST API (`POST
/api/v1/matter/exposable/bulk`). Hot-reload is not supported — every
exposure change requires a daemon restart.

---

## 3 — Wire Diagnostics

### 3.1 Daemon log (primary source of truth)

The daemon logs every PASE / CASE / Subscribe / Invoke event as
structured JSON. Useful one-liner monitors:

```bash
LOG=$(ls -t /tmp/openccu-loom-pair-logs/daemon-*.log | head -1)

tail -F "$LOG" | grep -E --line-buffered \
  'pase\.session_established|case\.session_established|opcreds\.addnoc|update_fabric_label|fabric\.removed|fabric_published|subscribe\.report.*reports":[1-9]|measurement\.notify'
```

### 3.2 tcpdump (when daemon log isn't enough)

On macOS:

```bash
sudo tcpdump -i en0 -w /tmp/matter.pcap 'udp port 5540 or udp port 5353'
# Open in Wireshark with display filter: udp.port == 5540 || mdns
```

Note: every Matter datagram is AES-CCM-encrypted after PASE. tcpdump shows
the wire shape (lengths, source/dest, MRP counters) but no plaintext.
Useful for spotting retransmit storms, MTU issues, missing ACKs.

### 3.3 iPad/iPhone syslog (optional, gives Apple-side state)

Plug iPad/iPhone in via Lightning/USB-C. Install `libimobiledevice`:

```bash
brew install libimobiledevice
idevicesyslog | grep -iE 'home|matter|chip|hap|mtrdevice|sigma|fabric' > /tmp/ipad-syslog-$(date +%H%M).log
```

iOS ≥ 17 redacts most identifying fields (`<private>`) but the state
transitions are still visible. Key strings:

- `MTRDevice` — Matter device wrapper (Apple's high-level handle)
- `HMHome` — Home database
- `HAPErrorDomain` — pair / projection error codes
  - `Code=14`: "No Endpoints In Use" — bridged endpoint not mountable
  - `Code=19`: "Device Unreachable" — Subscribe stopped reporting
  - `Code=24`: "Failed to rebuild HAP services of CHIP Accessory" — HAP build failed
- `Sigma2`, `Sigma3` — handshake-level progress

---

## 4 — Pair Walk-Through

1. **Apple Home App → "+" → "Add or scan accessory" → scan QR.**
2. App prompts "Add to Home" / "Zubehör hinzufügen" — confirm.
3. App enters "Connecting…" / "Verbinden…" phase. Daemon should log
   within 5 s:
   ```
   matter.bridge.pase.session_established
   matter.opcreds.addnoc.params         admin_vendor_id=0x1349
   matter.bridge.case.session_established
   matter.rx.im.invoke req_paths="ep=0 cl=0x0030 cmd=0x4"   # CommissioningComplete
   matter.mdns.fabric_published
   ```
4. App phase changes to "Adding to Home" / "zu Zuhause hinzufügen".
   Apple Home Hub now pairs autonomously — daemon should log a **second**
   AddNOC ~15–25 s later:
   ```
   matter.opcreds.addnoc.params   admin_vendor_id=0x1384
   matter.bridge.case.session_established  fabric_index=2
   ```
5. App switches to per-accessory naming / room assignment. Daemon logs
   one Subscribe per Apple source node (typically 3–5 from
   iPad + Hub + iCloud workers). Each subscription ships its
   Subscribe-Initial-Report.
6. UpdateFabricLabel may arrive after CommissioningComplete:
   ```
   matter.opcreds.update_fabric_label   label="Mein Zuhause"
   ```
7. The bridge appears in Apple Home with its bridged endpoints as
   individual accessories.

If steps 1–3 succeed but step 4 never produces the second AddNOC,
the Apple Home Hub couldn't be reached or HMHome rejected the bridge.
See §5 failure modes.

---

## 5 — Known Failure Modes

| Apple Home UI state | Daemon log signature | Likely cause |
|---------------------|----------------------|--------------|
| "Connecting…" hangs > 90 s | PASE only, no AddNOC | iPad↔daemon network path issue (firewall? mDNS? IPv6 disabled?) |
| "Adding to Home" hangs > 3 min | 1 AddNOC + CASE, no `fabric.removed`, no 2nd AddNOC | HMHome cache corruption — Apple Home Hub refuses to re-pair |
| "Bridge added but not supported" | Full multi-admin pair flow, then `fabric.removed` | Schema drift — Apple's HAP mapper rejected the topology. Cross-check with chip-tool v5 report |
| Bridge appears, accessories say "No response" | 2 fabrics, Subscribes alive, `subscribe.report reports=0` | DP not pushing values OR DataVersionFilter caching stale 1. Verify `measurement.notify endpoint=N` fires |
| Bridge appears, sensor shows "—" / no value | Same as above | First Subscribe-Initial shipped a `null` (DP unobserved); the `load_data_point_value` boot warm-up should prevent this — check `initial_load.central_done errored=0` |
| Bridge appears but only some accessories show | HAP-mapper rejected specific endpoints | Likely cluster-mandatory miss on those endpoints. Cross-check `cluster_servers` log for the missing ep |
| Pair succeeds first time, second daemon restart fails | Old fabric still in DB | `bootid` salt rotation requires the daemon to start without prior fabric rows — clean DB before re-pair |

---

## 6 — Reset Paths (escalating)

| Severity | Action | Restores |
|----------|--------|----------|
| 1 — daemon | DB clean + restart (`§2.2` recipe) | bootid rotated, fresh fabric state |
| 2 — Apple Home App | Long-press bridge tile → "Zubehör entfernen". Wait 60 s for iCloud sync | App-level cache for that bridge |
| 3 — iPad reboot | Settings → Allgemein → Ausschalten / hold side button | HMHome-Daemon restart |
| 4 — iPad WLAN reset | Settings → Allgemein → Übertragen → "Netzwerkeinstellungen zurücksetzen" | mDNS cache, WLAN credentials lost |
| 5 — Apple Home full reset | Open Home app → top-right home icon → "Mein Zuhause entfernen" | All accessories, rooms, scenes — destructive |
| 6 — iPad full reset | Settings → Allgemein → Übertragen → "Alle Inhalte und Einstellungen löschen" | Clean Slate. Very destructive |

After every level-1 reset, allow ≥ 30 s before the next pair attempt so
the daemon's mDNS announcements settle. After a level-3 reset, allow
60–90 s for HMHome to fully restart. Never re-pair within the same
30 s window — Apple's Bonjour cache amplifies stale records.

---

## 7 — Empirical Verification Order

When validating a daemon-side change, follow this escalation ladder.
**Stop at the first failure** and fix before moving up the ladder.

1. `go build && go test ./...` — green (allow pre-existing
   `tests/contract` failures: `TestServiceDiscoveryShape_Cover_Position`,
   `TestDocPurity`).
2. `go test ./internal/north/matter/... -run 'Parity'` — Track B parity
   tests green. 244 PASS / 33 SKIP / 0 FAIL is the baseline (commit `2111bd0`).
3. `go test -tags=integration ./tests/integration/... -run 'MatterBridgeSmoke'`
   — 9 PASS / 0 SKIP / 0 FAIL.
4. **chip-tool on Ubuntu** — all 12 tests green. Apple-independent.
5. **iPad / iPhone Apple Home pair** — Multi-Admin flow per §4.
6. **Hub-managed re-pair after daemon restart** — restart the daemon
   (DB cleaned), confirm the Apple Home Hub silently re-pairs to its
   prior fabric within ~60 s without user action. Tests
   `case resumption persist` (drift L5-D1).

Apple-side empirical verification should always come **last** because
HMHome cache state is non-reproducible across attempts.

---

## 8 — Current State

- **Track A**: 84/84 drift entries addressed (15 CRITICAL + 14 HIGH +
  24 MED + 31 LOW). 100% closure rate.
- **Track B**: 244 parity tests PASS, 33 SKIP-tagged future drift IDs.
- **chip-tool v5 report**: pair succeeds via `pairing already-discovered`
  in ~4 s; voll-wildcard returns 107 attribute reports without any
  `UnsupportedAttribute` / `UnsupportedCluster`. Apple-independent
  wire-correctness confirmed.
- **Apple pair**: blocked by the developer's iPad HMHome cache being
  corrupted from ~60 prior pair attempts on 2026-05-11/12. Daemon-side
  is correct; verifying against a fresh iPad (or after a level-5 reset)
  is the open empirical step.

Known stable invariants (verified by Track A audit, byte-exact):

- TLV codec byte-exact against matter.js HEAD `ebe091744`
- Bug A (Sigma2 multicast dedupe) wire-correct
- Bug G (Multi-fabric CASE responder) wire-correct
- Bug M (Fabric-scoped ACL read) wire-correct
- Bug O (Operational mDNS SII/SAI floor) wire-correct
- Bug P (OpCreds fabric resolution) wire-correct

---

## 9 — Report Template

When reporting an Apple-pair test outcome (success or failure), provide:

```markdown
# Apple-Pair Test — <date>

**Daemon commit:** <git rev-parse --short HEAD>
**Test device:** iPad mini (A2568) iOS 17.6.1 / iPhone 14 iOS 18.0 / …
**Apple Home Hub:** HomePod mini (S2) / Apple TV 4K (A2169) / none
**WLAN:** 5 GHz, no client isolation, IPv6 enabled

## Pair Phase Trace

| Time | Apple Home UI | Daemon log event |
|------|---------------|------------------|
| 14:23:01 | Scan QR | (idle) |
| 14:23:04 | "Connecting…" | matter.bridge.pase.session_established |
| 14:23:09 | "Connecting…" | matter.opcreds.addnoc.params vendor=0x1349 |
| 14:23:09 | "Connecting…" | matter.bridge.case.session_established session=2 fabric=1 |
| 14:23:11 | "Adding to Home" | matter.rx.im.invoke cl=0x30 cmd=0x4 |
| 14:23:25 | "Adding to Home" | matter.opcreds.addnoc.params vendor=0x1384 ← Hub auto-pair |
| 14:23:26 | "Adding to Home" | matter.bridge.case.session_established session=3 fabric=2 |
| 14:23:30 | "Set Room" | matter.opcreds.update_fabric_label "Wohnzimmer" |
| 14:23:35 | Bridge visible | (5 active subscriptions, no fabric.removed) |

## Outcome

[Success — bridge visible, all N accessories show live values]

## Anomalies

- Sensor X showed "no response" for 12 s after pair before recovering
- Apple Home Hub took 16 s for autonomous re-pair (typical 10–20 s)

## Counts (from daemon log)

- pase.session_established: N
- case.session_established: N
- opcreds.addnoc: N
- subscription added: N
- subscribe.report reports>0: N
- measurement.notify: N
- fabric.removed: N

## Daemon log tarball

Attach `/tmp/openccu-loom-pair-logs/daemon-…-pair-result.log.gz`
```

---

## 10 — When the bug isn't in OpenCCU-Loom

If chip-tool tests pass + Track-B parity tests pass + the Apple pair
still fails on a fresh iPad, suspect (in this order):

1. **Apple Home Hub side**: the Hub itself may have a stale Matter bridge
   record. Restart the Hub (HomePod: pull power 30 s; Apple TV:
   Settings → System → Restart).
2. **iCloud sync window**: HMHome propagation can take 60–90 s. Wait
   it out before retrying.
3. **iOS version regression**: cross-check matter.js's
   [Apple compat issue tracker](https://github.com/project-chip/matter.js/labels/apple%20home)
   for the iOS build the test iPad is on.
4. **WLAN-side dropping IPv6 link-local multicast**: some consumer
   routers silently filter `ff02::fb` (mDNS over IPv6). Confirm with
   `tcpdump -i en0 ip6 multicast` while the iPad scans.
5. **bridge-side bootid not rotating**: confirm `bootid.Salt()` returns
   different bytes between two daemon restarts.

---

*This guide is living documentation. Update §8 after every committed
fix and §5 / §10 after every empirical run that produces a new failure
mode.*
