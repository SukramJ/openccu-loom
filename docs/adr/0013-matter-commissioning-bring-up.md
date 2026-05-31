# ADR 0013 — Matter wire-protocol design rules from chip-tool bring-up

- **Status:** accepted
- **Date:** 2026-05-06
- **Supersedes:** none
- **Related:** [ADR 0012 — Matter pure-Go implementation](0012-matter-pure-go-implementation.md)

## Context

ADR 0012 locks the implementation form (pure-Go) and the cluster
subset for the Matter bridge. During chip-tool bring-up against the
live daemon a series of design rules surfaced that are not obvious
from the Matter spec but are required for interoperability with a
real commissioner. Each rule fills a gap that ADR 0012 left implicit.

The unit / contract test suite stayed green throughout the bring-up
runs — the gaps lived between in-process simulators (godevccu,
fakeStores) and what chip-tool actually expects on the wire. This
ADR records the rules so future bridge changes do not regress them
and so contract-test design has a target to aim at.

## Decisions

### 1. Outbound replies stamp the **peer's** session ID

In Matter every secure-channel side allocates its own session ID. A
reply addressed to the peer must carry the *peer's* slot number, not
the receiver's local slot. `channel.Session.PeerSessionID()` is the
authoritative source; `bridge.sendReply` and
`bridge.sendUnsolicitedIM` stamp it.

**Why this is non-obvious:** when the peer sends to us, the inbound
header's `SessionID` *is* our local slot — echoing it back feels
reflexive. The asymmetry is buried in the spec.

### 2. The UDP receive loop is panic-safe

A bad datagram must not take down the receive goroutine. The bridge
wraps every handler dispatch in a `defer recover()` (`safeDispatch`).
Recovered panics are intentionally suppressed — the underlying bug
is fixed at the source, but the receive loop survives the malformed
input that exposed it.

**Why this is non-obvious:** "panics indicate bugs, let them
surface" is the standard Go advice. It does not apply when the
parser receives untrusted wire bytes — a single bad datagram must
not affect every other in-flight exchange.

### 3. `AttributeDataIB.DataVersion` is always emitted

Matter §10.6.1.4 lists `DataVersion` as a required field of every
`AttributeDataIB`. chip-tool's `ClusterStateCache` parses every
Read response (not just subscriptions) and rejects entries missing
the tag-0 element. `AttributeReport.DataVersion` defaults to 1 on
the wire when the bridge has no per-cluster version tracking.

**Why this is non-obvious:** the spec narrative for DataVersion
focuses on subscription-cache invalidation, easy to read as "only
relevant during subscriptions". chip-tool reads the field
unconditionally.

### 4. InvokeResponse `Path.Command` is the **response** command ID

Matter §10.6.7 marks command request/response IDs as asymmetric
(e.g. ArmFailSafe = 0x00, ArmFailSafeResponse = 0x01).
`bridge.rewriteInvokeResponseCommand` walks the response entries
before encoding and patches `Path.Command` from the Go response
struct type. Echoing the request's `Path.Command` verbatim breaks
chip-tool's `TypedCommandCallback` decoder lookup.

**Why this is non-obvious:** the path looks symmetric (request →
response on the same path). The spec calls out the asymmetry but
not loudly.

### 5. Root-endpoint cluster servers attach via a dedicated API

The endpoint assembler is source-driven (Custom DPs declare their
clusters). Endpoint 0 carries spec-defined clusters
(BasicInformation, GeneralCommissioning, OperationalCredentials, …)
that have no source DP. `Endpoint.RootClusterServers` plus
`Bridge.AttachRootClusters` are the explicit attach surface; the
daemon constructs the standard cluster bundle and wires it
post-Start.

**Why this is non-obvious:** the bridged-endpoint code path is
comprehensive enough that "root will follow the same pattern" is a
plausible assumption. Root does not — its cluster set is
spec-driven, not device-driven.

### 6. `CommandFieldsReader` is path-aware

A cluster-native command-fields decoder cannot dispatch on
`(cluster, command)` from `tlv.Element` alone. The decoder
signature carries the path:

```go
type CommandFieldsReader func(path ConcreteCommandPath, dec *tlv.Decoder, el tlv.Element) (any, error)
```

The bridge's reader switches on path and decodes per-command. A
generic / minimal API is attractive in the abstract, but
cluster-native field decoding is intrinsically path-aware.

### 7. `AttestationChallenge` is derived from the PASE/CASE key schedule

`AttestationRequest` and `CSRRequest` signatures bind to
`(elements || AttestationChallenge)` per Matter §11.18.4.7. The
challenge is the third 16-byte block of the PASE HKDF expansion
(48 bytes total: 16 I2RKey || 16 R2IKey || 16 AttestationChallenge).
`Entry.AttestationChallenge` exposes it; `SetAttestationChallenge`
plumbs it into `OperationalCredentials`.

**Why this is non-obvious:** the spec defines the challenge in §4.13
(secure channel key schedule) but consumes it in §11.18.4.7
(operational credentials). The cross-section binding is easy to miss.

## Consequences

- The bridge is structurally complete for chip-tool commissioning
  with `--bypass-attestation-verifier true`. All 19 commissioning
  stages run to first CASE pairing.
- Production-grade attestation requires CSA-signed CD + a vendor-
  supplied DAC chain. ADR 0012 §"Implementation strategy" treats
  vendor DAC/PAI/CD bundles as config-only inputs; the bridge code
  does not generate them.
- The seven rules above pin invariants that contract tests must
  enforce. The corresponding tests live in `bridge/`, `im/`, and
  `cluster/core/` packages.

## Adoption guide

When adding new wire-protocol surfaces (new clusters, new IM verbs,
new secure-channel paths) the same family of pitfalls recurs:

1. **Live commissioner smoke is non-negotiable** — chip-tool has
   parser strictness no in-process simulator replicates. Add a
   smoke run to the release checklist whenever a wire-shape changes.
2. **Asymmetric protocols are common in Matter**: SessionID,
   `Path.Command`, and cluster command ID assignment all have
   non-symmetric request / response meanings. When in doubt, read
   the spec twice.
3. **Test-rig limits**: `chip-tool` snap-confined does not multicast
   on `lo` reliably. `pairing already-discovered <ip> <port>` is the
   loopback-friendly mode; full DNS-SD discovery needs a real LAN.
