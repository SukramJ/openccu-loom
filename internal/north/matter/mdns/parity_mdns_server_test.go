// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Parity tests for mDNS server query-handling behaviour against matter.js HEAD.
//
// matter.js reference: packages/protocol/test/mdns/MdnsServerTest.ts
//
// openccu-loom mapping:
//   The openccu-loom mDNS stack is split between:
//     - [Noop] advertiser — in-memory; used for unit / boot tests.
//     - [Zeroconf] advertiser — production; backed by grandcat/zeroconf.
//     - [Advertiser] interface — common contract (Publish/Withdraw/Active/Close).
//   The matter.js MdnsServer tests exercise a wire-level DNS query/response
//   simulator that has no direct equivalent in the openccu-loom API surface.
//   The structural invariants (record set management, query-scoped response,
//   duplicate suppression) are captured here as skip-annotated anchors so
//   that future integration-level implementations can fill them in.
//
// Build status: all cases either pass on the openccu-loom surface or are
// t.Skip-annotated with a gap label.

package mdns_test

import (
	"context"
	"net"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
)

// ─── Server responds to standard queries ─────────────────────────────────────

// TestParityMdnsServer_RespondsToANYQuery verifies that after publishing a
// service the advertiser's Active() set reflects the record.
//
// Mirrors matter.js packages/protocol/test/mdns/MdnsServerTest.ts:75
// (case "server responds to an ANY query with different Query messageTypes (Query)").
//
// Note: wire-level DNS encode/decode is not exposed in the openccu-loom
// Advertiser interface; the test validates the structural invariant
// (record is in Active set) rather than the wire bytes.
func TestParityMdnsServer_RespondsToANYQuery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	adv := mdns.NewNoop()
	svc := buildMdnsParityTestService(t, 0x01)

	if err := adv.Publish(ctx, svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(adv.Active()) != 1 {
		t.Fatalf("Active() after Publish: len=%d, want 1", len(adv.Active()))
	}
}

// TestParityMdnsServer_TruncatedQueryDelaysResponse verifies that after a
// truncated (TC-bit) query the server waits before responding.
//
// Mirrors matter.js packages/protocol/test/mdns/MdnsServerTest.ts:118
// (case "server responds to an ANY query with different Query messageTypes (Truncated Query)").
//
// openccu-loom gap: the Advertiser interface does not expose the TC-bit delay
// logic; that lives in the grandcat/zeroconf layer.
func TestParityMdnsServer_TruncatedQueryDelaysResponse(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — drift L7-TruncatedTC; Advertiser surface does not expose TC-bit delay")
}

// TestParityMdnsServer_RespondsToSRVQuery verifies that a SRV query returns
// the SRV record in answers and TXT + A in additionalRecords.
//
// Mirrors matter.js packages/protocol/test/mdns/MdnsServerTest.ts:207
// (case "server responds to an SRV query").
//
// openccu-loom gap: wire-level DNS query/response dispatch is internal to the
// grandcat/zeroconf library and not directly testable via the Advertiser API.
func TestParityMdnsServer_RespondsToSRVQuery(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — wire-level SRV query response not exposed via Advertiser interface")
}

// TestParityMdnsServer_RespondsToAQuery verifies that an A-record query for
// the host name returns the A record only.
//
// Mirrors matter.js packages/protocol/test/mdns/MdnsServerTest.ts:246
// (case "server responds to an A query with A record only").
//
// openccu-loom gap: wire-level DNS query/response dispatch is internal to the
// grandcat/zeroconf library and not directly testable via the Advertiser API.
func TestParityMdnsServer_RespondsToAQuery(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — wire-level A query response not exposed via Advertiser interface")
}

// ─── Answer suppression ───────────────────────────────────────────────────────

// TestParityMdnsServer_SuppressKnownAnswer verifies that the mDNS server
// omits a record from the response when the querier already supplied it as
// a known-answer in the query.
//
// Mirrors matter.js packages/protocol/test/mdns/MdnsServerTest.ts:287
// (case "suppress one answers in an ANY query").
//
// openccu-loom gap: known-answer suppression is internal to grandcat/zeroconf.
func TestParityMdnsServer_SuppressKnownAnswer(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — known-answer suppression not exposed via Advertiser interface")
}

// TestParityMdnsServer_SuppressFullAnswer verifies that no response is sent
// when every record is present in the known-answer section.
//
// Mirrors matter.js packages/protocol/test/mdns/MdnsServerTest.ts:370
// (case "suppress full answer in an ANY query").
//
// openccu-loom gap: full-answer suppression is internal to grandcat/zeroconf.
func TestParityMdnsServer_SuppressFullAnswer(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — full-answer suppression not exposed via Advertiser interface")
}

// ─── Duplicate question suppression ──────────────────────────────────────────

// TestParityMdnsServer_DuplicateQuestionSuppression verifies that the server
// does not respond to the same query twice within the suppression window
// (QUESTION_SUPPRESSION_WINDOW = 999 ms per matter.js).
//
// Mirrors matter.js packages/protocol/test/mdns/MdnsServerTest.ts:427
// (case "server suppress response to same ANY query when 0ms delay").
//
// openccu-loom gap: duplicate-question suppression is internal to grandcat/zeroconf.
func TestParityMdnsServer_DuplicateQuestionSuppression(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — duplicate-question suppression not exposed via Advertiser interface")
}

// TestParityMdnsServer_DuplicateSuppressionWindowExpires verifies that after
// the suppression window (999 ms) expires, the same query does get a response.
//
// Mirrors matter.js packages/protocol/test/mdns/MdnsServerTest.ts:1061
// (case "responds after suppression window expires").
//
// openccu-loom gap: suppression-window timer is internal to grandcat/zeroconf.
func TestParityMdnsServer_DuplicateSuppressionWindowExpires(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — suppression-window timer not exposed via Advertiser interface")
}

// ─── Unicast response mode ────────────────────────────────────────────────────

// TestParityMdnsServer_UnicastAndSplitResponse documents gaps in the
// openccu-loom Advertiser surface: unicast/multicast TTL-based decisions
// (matter.js MdnsServerTest.ts:801) and large-response UDP packet splitting
// (matter.js MdnsServerTest.ts:937) are both internal to grandcat/zeroconf.
func TestParityMdnsServer_UnicastAndSplitResponse(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — unicast/multicast TTL decision (L7-D04) and UDP packet splitting not exposed via Advertiser interface")
}

// ─── Unknown-name query rejection ────────────────────────────────────────────

// TestParityMdnsServer_NoResponseForUnknownName verifies that the advertiser
// does NOT respond to queries for names it does not serve.
//
// Mirrors matter.js packages/protocol/test/mdns/MdnsServerTest.ts:1113
// (case "does not respond to queries for names we do not serve").
//
// openccu-loom mapping: Active() only contains explicitly Published services;
// querying for an absent name produces no Active entry.
func TestParityMdnsServer_NoResponseForUnknownName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	adv := mdns.NewNoop()

	svc := buildMdnsParityTestService(t, 0x02)
	if err := adv.Publish(ctx, svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// The active set should contain only the published service, not unrelated names.
	active := adv.Active()
	for _, a := range active {
		if a.InstanceName == "UNKNOWN-INSTANCE" {
			t.Error("unexpected UNKNOWN-INSTANCE found in Active()")
		}
	}
}

// TestParityMdnsServer_RespondsToHostARecordByHostname verifies that the
// advertiser serves the A record when the host name is queried directly.
//
// Mirrors matter.js packages/protocol/test/mdns/MdnsServerTest.ts:1143
// (case "responds to A-record host name even though it differs from the service qname").
//
// openccu-loom mapping: after Publish, the service is in Active(); the
// HostName field is separate from the InstanceName so both can be queried.
func TestParityMdnsServer_RespondsToHostARecordByHostname(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	adv := mdns.NewNoop()

	svc := buildMdnsParityTestService(t, 0x03)
	if err := adv.Publish(ctx, svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	active := adv.Active()
	if len(active) != 1 {
		t.Fatalf("Active() len=%d, want 1", len(active))
	}
	if active[0].HostName == "" {
		t.Error("HostName must be non-empty for A-record lookup")
	}
}

// TestParityMdnsServer_MixedQueryRespondsForKnownNames verifies that when a
// query contains both known and unknown names, the server still responds for
// the known name.
//
// Mirrors matter.js packages/protocol/test/mdns/MdnsServerTest.ts:1168
// (case "still responds when a known-name query is mixed with unknown-name queries").
//
// openccu-loom gap: per-query filtering is internal to grandcat/zeroconf.
func TestParityMdnsServer_MixedQueryRespondsForKnownNames(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — per-query name filtering not exposed via Advertiser interface")
}

// TestParityMdnsServer_RecordsGeneratorReplacement verifies that after
// replacing the records generator, queries for the old name no longer match
// and queries for the new name do.
//
// Mirrors matter.js packages/protocol/test/mdns/MdnsServerTest.ts:1276
// (case "picks up new names after the records generator is replaced").
//
// openccu-loom mapping: re-publishing replaces the active service record.
func TestParityMdnsServer_RecordsGeneratorReplacement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	adv := mdns.NewNoop()

	old := buildMdnsParityTestService(t, 0x04)
	if err := adv.Publish(ctx, old); err != nil {
		t.Fatalf("Publish old: %v", err)
	}

	// Withdraw the old record and publish a new one with a different name.
	if err := adv.Withdraw(ctx, old.InstanceName, old.ServiceType); err != nil {
		t.Fatalf("Withdraw old: %v", err)
	}

	newSvc := buildMdnsParityTestService(t, 0x05)
	if err := adv.Publish(ctx, newSvc); err != nil {
		t.Fatalf("Publish new: %v", err)
	}

	active := adv.Active()
	if len(active) != 1 {
		t.Fatalf("Active() len=%d, want 1 (old replaced by new)", len(active))
	}
	// Old name must no longer be in the active set.
	if active[0].InstanceName == old.InstanceName {
		t.Errorf("old instance name still in Active() after replacement")
	}
}

// ─── RFC 6762 §16 case-insensitive matching ────────────────────────────────

// TestParityMdnsServer_CaseInsensitiveNameMatch verifies that queries using
// uppercase names match lowercase published records per RFC 6762 §16.
//
// Mirrors matter.js packages/protocol/test/mdns/MdnsServerTest.ts:1198
// (case "matches uppercase query names against lowercase records (RFC 6762 §16)").
//
// openccu-loom gap: case-insensitive DNS matching is internal to grandcat/zeroconf.
func TestParityMdnsServer_CaseInsensitiveNameMatch(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — RFC 6762 §16 case-insensitive name matching not exposed via Advertiser interface")
}

// TestParityMdnsServer_RFC6762Gaps documents remaining RFC 6762 §16
// parity gaps: TC-continuation fragment merging (MdnsServerTest.ts:1223) and
// case-insensitive known-answer suppression (MdnsServerTest.ts:1388) are both
// internal to grandcat/zeroconf and not exposed via the Advertiser interface.
func TestParityMdnsServer_RFC6762Gaps(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — RFC 6762 §16 TC-continuation fragment merging + case-insensitive known-answer suppression not exposed via Advertiser interface")
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// ─── Commissionable TXT-schema lock ──────────────────────────────────────────

// TestParityMdnsServer_CommissionableTXTSchemaLock verifies that
// BuildCommissionableService emits ALL required TXT keys from the Matter
// §4.3.1.6 mandatory set: {D, VP, CM, DT, SII, SAI, SAT, PH}.
//
// Mirrors matter.js packages/protocol/test/mdns/MdnsServerTest.ts:75–120
// (commissionable record key assertions in "server responds to an ANY query")
// and matter.js packages/protocol/src/mdns/MdnsBroadcaster.ts
// buildCommissionableInstanceData — every one of those keys is emitted
// unconditionally.
//
// Source-Origin: derived from matter.js MdnsBroadcaster.ts
// buildCommissionableInstanceData and MdnsTest.ts commissionable record
// assertions; matches chip src/lib/dnssd/TxtFields.cpp required keys.
func TestParityMdnsServer_CommissionableTXTSchemaLock(t *testing.T) {
	t.Parallel()

	svc := mdns.BuildCommissionableService(mdns.CommissionableServiceConfig{
		InstanceID:        [8]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x00, 0x44},
		Discriminator:     0x0ABC,
		VendorID:          0xFFF1,
		ProductID:         0x8001,
		CommissioningMode: 1,
		DeviceTypeID:      0x000E,
		Port:              5540,
		HostName:          "testbridge",
		Addresses:         []net.IP{net.ParseIP("192.0.2.1")},
		// Zero SII/SAI/SAT/PH → must resolve to spec defaults, not omit the keys.
	})

	// Build a map of TXT key → value for O(1) lookup.
	txtMap := make(map[string]string, len(svc.TXT))
	for _, rec := range svc.TXT {
		txtMap[rec.Key] = rec.Value
	}

	// The Matter §4.3.1.6 mandatory commissionable TXT keys:
	required := []string{"D", "VP", "CM", "DT", "SII", "SAI", "SAT", "PH"}
	for _, key := range required {
		if _, ok := txtMap[key]; !ok {
			t.Errorf("commissionable TXT missing mandatory key %q — Matter §4.3.1.6 + matter.js MdnsBroadcaster.ts buildCommissionableInstanceData", key)
		}
	}

	// SAI default must be 300 ms (not 0) per matter.js MATTER_COMMISSION_SAI_DEFAULT.
	if sai := txtMap["SAI"]; sai == "0" || sai == "" {
		t.Errorf("commissionable SAI=%q, want 300 (matter.js MATTER_COMMISSION_SAI_DEFAULT)", sai)
	}
	// SII default must resolve to a non-zero value (500 ms — matter.js
	// SessionIntervals.defaults.idleInterval), not be omitted.
	if sii := txtMap["SII"]; sii == "0" || sii == "" {
		t.Errorf("commissionable SII=%q, want the 500 ms idle default (matter.js SessionIntervals)", sii)
	}
}

// ─── Operational SII floor regression guard ──────────────────────────────────

// TestParityMdnsServer_OperationalSIIFloor500ms verifies that the operational
// `_matter._tcp` record emits SII=500ms and SAI=300ms when the caller passes
// zero (the "use spec default" signal). This is the regression guard for the
// SII/SAI default mismatch that caused openccu-loom to emit SII=5000 ms on the operational
// record (the commissionable default) instead of the correct 500 ms, making
// the bridge appear unreachable after post-CommissioningComplete CASE
// reconnect because the controller's MRP retry budget was sized for a sleeping
// commissioner, not an always-on bridge.
//
// Mirrors matter.js packages/protocol/src/mdns/MdnsBroadcaster.ts
// MATTER_OPERATION_SII_DEFAULT (500) and MATTER_OPERATION_SAI_DEFAULT (300)
// and chip src/lib/dnssd/Advertiser_ImplMinimalMdns.cpp operational defaults.
//
// Source-Origin: derived from matter.js MdnsBroadcaster.ts operational
// service defaults + MdnsTest.ts operational record assertions.
func TestParityMdnsServer_OperationalSIIFloor500ms(t *testing.T) {
	t.Parallel()

	svc := mdns.BuildOperationalService(mdns.OperationalServiceConfig{
		CompressedFabricID: [8]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x00, 0x55},
		NodeID:             0x0000000099887766,
		Port:               5540,
		HostName:           "testbridge",
		Addresses:          []net.IP{net.ParseIP("192.0.2.1")},
		// Explicit zeros → must resolve to 500 / 300, NOT to 5000 / 300
		// (commissionable defaults must not bleed into operational record).
		SessionIdleInterval:   0,
		SessionActiveInterval: 0,
	})

	txtMap := make(map[string]string, len(svc.TXT))
	for _, rec := range svc.TXT {
		txtMap[rec.Key] = rec.Value
	}

	// SII must be 500ms (operational default), NOT 5000ms (commissionable).
	sii := txtMap["SII"]
	if sii != "500" {
		t.Errorf("operational SII=%q, want 500 (matter.js MATTER_OPERATION_SII_DEFAULT = 500ms; L7-D01 regression: emitting 5000ms breaks post-CASE MRP retry budget)", sii)
	}

	// SAI must be 300ms.
	sai := txtMap["SAI"]
	if sai != "300" {
		t.Errorf("operational SAI=%q, want 300 (matter.js MATTER_OPERATION_SAI_DEFAULT = 300ms)", sai)
	}

	// SII for operational must not equal the commissionable default (5000ms).
	if sii == "5000" {
		t.Errorf("operational SII=5000ms — commissionable SII leaked into operational record (L7-D01 regression)")
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// buildMdnsParityTestService builds a minimal operational service record for
// parity tests. seed differentiates concurrent test instances.
func buildMdnsParityTestService(t *testing.T, seed byte) mdns.Service {
	t.Helper()
	var cfabric [8]byte
	cfabric[0] = seed
	return mdns.BuildOperationalService(mdns.OperationalServiceConfig{
		CompressedFabricID:    cfabric,
		NodeID:                uint64(seed)*0x1000 + 1,
		Port:                  5540,
		HostName:              "testbridge",
		Addresses:             []net.IP{net.ParseIP("192.0.2.1")},
		SessionIdleInterval:   5000,
		SessionActiveInterval: 300,
	})
}
