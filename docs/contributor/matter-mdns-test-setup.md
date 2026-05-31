# Matter mDNS Test Setup

This document describes how to run `TestMDNS_DiscoverCommissionable`
(in `tests/chiptool/mdns_test.go`) — the only chip-tool suite member
that requires a host with working multicast DNS publishing. The test
is `t.Skip()`-gated by default so the standard CI run (and any
developer running `go test -tags=chiptool ./tests/chiptool/...`) does
not hit the host-setup requirements.

---

## 1. What the test verifies

The test brings up an isolated bridge with the `zeroconf` mDNS
advertiser, then runs `chip-tool discover commissionables` against
that bridge and asserts:

- The bridge publishes a `_matterc._udp.local.` SRV/TXT record for
  the commissioning window.
- The TXT record carries `VendorID=0xFFF1` (the harness's configured
  vendor ID) and `Discriminator=0xF00`.
- chip-tool's discovery output reports both via the `Vendor ID:` /
  `Discovered:` markers.

It does NOT exercise the post-discovery PASE flow — that path is
covered by every other chip-tool test in the suite via
`pairing already-discovered` which bypasses mDNS.

The drift class the test catches is `MDNS-TXT-DRIFT`: a missing or
mis-typed TXT record that breaks Apple Home / chip-tool's
post-discovery filter. See `docs/parity/by_design.md` (BD-Matter-mDNSDeviceType) for the
matter.js HEAD reference.

---

## 2. Why the test is opt-in

The default chip-tool suite is `mdns_advertise: noop` because real
mDNS publishing has three host-level pitfalls that surface as
flakiness in CI runners:

1. **Avahi daemon contamination.** When `avahi-daemon` is running on
   the host, the bridge's `_matter._tcp.local.` and
   `_matterc._udp.local.` records appear on the host's actual LAN.
   Subsequent test runs interfere with each other unless every
   record is taken down cleanly — which it is, but only after the
   test cleanup hook fires.

2. **IPv6 multicast on loopback.** chip-tool's
   `discover commissionables` listens on link-local IPv6
   (`ff02::fb`). A host that disables multicast on the loopback
   interface, or that runs in a container without `--cap-add=NET_ADMIN`
   and `--network=host`, gets zero discovery responses regardless of
   whether the bridge advertises correctly.

3. **Firewall/SELinux blocks.** Some hosts firewall outbound
   `5353/udp` even on loopback. The bridge's `grandcat/zeroconf`
   advertiser silently fails to bind and the test times out.

---

## 3. Host requirements

A host that wants to run the opt-in mDNS test needs:

| Requirement | Why |
|---|---|
| `avahi-daemon` running (`systemctl status avahi-daemon`) or `mdnsd` on macOS | provides the kernel-level multicast routing chip-tool consults |
| IPv6 enabled on at least the loopback interface | chip-tool resolves `_matterc._udp.local.` over `ff02::fb` |
| No firewall block on `5353/udp` for the loopback path | the bridge publishes there; chip-tool reads back |
| No other Matter bridge advertising the same VID/discriminator pair on the same LAN | otherwise chip-tool may discover the wrong record |
| chip-tool installed (`snap install chip-tool` or set `OPENCCU_LOOM_CHIPTOOL_BIN`) | required by every chip-tool test |

### Quick host check

```sh
# Check Avahi
sudo systemctl status avahi-daemon

# Check 5353/udp is not blocked locally
nc -u -z -v localhost 5353

# Check the chiptool binary is on PATH
which chip-tool || echo "set OPENCCU_LOOM_CHIPTOOL_BIN"
```

---

## 4. Activating the test

```sh
# Enable the opt-in flag and run only the mDNS test
OPENCCU_LOOM_CHIPTOOL_MDNS=1 \
  go test -tags=chiptool -v \
  -run TestMDNS_DiscoverCommissionable \
  ./tests/chiptool/...
```

Expected output on a properly-configured host:

```
=== RUN   TestMDNS_DiscoverCommissionable
    [chip-tool brings up its KVS, scans for ~15s]
    [TOO]   Discovered commissionable/commissioner device:
    [TOO]   Hostname: <hostname>.local.
    [TOO]   Vendor ID: 65521
    [TOO]   Product ID: 32768
--- PASS: TestMDNS_DiscoverCommissionable (15.8s)
```

When the flag is unset (the default) the test self-skips:

```
=== RUN   TestMDNS_DiscoverCommissionable
    mdns_test.go:33: opt-in: set OPENCCU_LOOM_CHIPTOOL_MDNS=1 to enable mDNS discovery
--- SKIP: TestMDNS_DiscoverCommissionable (0.00s)
```

---

## 5. Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `chip-tool found no commissionable records` | Avahi not running or 5353 blocked | start `avahi-daemon`; open 5353/udp |
| `chip-tool found no commissionable records` with snap-installed chip-tool **and** avahi-daemon running | snap chip-tool's `avahi-observe` plug routes mDNS through avahi-daemon, which does not see the bridge's `grandcat/zeroconf` publisher (both bind 5353 via SO_REUSEPORT, only avahi answers snap queries) | use a non-snap chip-tool build (`OPENCCU_LOOM_CHIPTOOL_BIN=/path/to/host-chip-tool`), OR temporarily stop avahi for the test (`sudo systemctl stop avahi-daemon avahi-daemon.socket`), OR run on a host without avahi-daemon |
| `commissionable record missing harness Vendor ID 0xFFF1` | another Matter device on the LAN advertises with the same VID | use a quiet LAN segment or stop the conflicting device |
| `harness did not switch to zeroconf advertiser` | bridge brought up with `mdns_advertise: noop` despite the env flag | inspect the rendered `config.yaml` in the test's data dir; the harness should set `mdns_advertise: zeroconf` unconditionally now |
| Test times out at 25s | IPv6 multicast routing broken on this host | enable IPv6 on loopback (`sysctl -w net.ipv6.conf.lo.disable_ipv6=0`); see container docs for `--network=host` requirement |

---

## 6. CI configuration

The default CI workflow runs the chip-tool suite without
`OPENCCU_LOOM_CHIPTOOL_MDNS=1`. The mDNS test therefore appears as a
SKIP in CI output — that is intentional and documented in the
commit history that introduced the opt-in (`9e31b24`).

To run the mDNS test in CI, the runner needs:

- A non-root account with `avahi-daemon` permission, or root with
  `--cap-add=NET_BIND_SERVICE`.
- Network mode `host` (not `bridge`) so the runner sees its own
  multicast domain.
- An `OPENCCU_LOOM_CHIPTOOL_MDNS=1` env var on the relevant matrix.

Until a dedicated CI lane exists for this, treat the test as a
developer-on-Linux-laptop smoke test rather than a CI gate.

---

## 7. Reference

- Test source: `tests/chiptool/mdns_test.go`
- Harness mDNS wiring:
  `tests/chiptool/harness/harness.go` (Bridge.MDNS, `mdns_advertise` config field)
- Bridge mDNS implementation:
  `internal/north/matter/mdns/` (zeroconf + noop advertisers)
- Reference: `docs/parity/by_design.md` BD-Matter-mDNSDeviceType (mDNS)
- The 5 mDNS-related by_design.md entries: `BD-Matter-mDNS-*`
