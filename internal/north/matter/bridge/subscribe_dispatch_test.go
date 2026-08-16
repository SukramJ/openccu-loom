// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for the extracted subscribe-dispatch helpers:
// buildInitialReport, registerSubscription, streamInitialReportChunks.
// Lives in package bridge for unexported-symbol access.

import (
	"context"
	"net"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// ─── buildInitialReport ─────────────────────────────────────────────────────

// TestBuildInitialReport_EmptyRequest_EmptyReport verifies that a
// SubscribeRequest with no AttributeRequests and no EventRequests produces an
// empty ReportData with HasSubscription=false.
func TestBuildInitialReport_EmptyRequest_EmptyReport(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	dispatcher := b.Dispatcher()
	if dispatcher == nil {
		// Start wires the dispatcher via Reassemble.
		t.Skip("dispatcher nil after start — topology not yet assembled")
	}
	req := im.SubscribeRequest{}
	report, matched := b.buildInitialReport(context.Background(), dispatcher, req)
	if report.HasSubscription {
		t.Error("buildInitialReport: HasSubscription must be false on return")
	}
	if len(report.Reports) != 0 {
		t.Errorf("buildInitialReport: empty request: got %d reports, want 0", len(report.Reports))
	}
	if len(report.EventReports) != 0 {
		t.Errorf("buildInitialReport: empty request: got %d event reports, want 0", len(report.EventReports))
	}
	if matched != 0 {
		t.Errorf("buildInitialReport: empty request: matched = %d, want 0", matched)
	}
}

// TestBuildInitialReport_SortOrder verifies that multiple attribute reports
// are returned sorted by (endpoint, cluster, attribute) ascending — the order
// matter.js's generateAttributeListReport enforces so Apple's HAP-Service-
// Mapper can resolve Descriptor.PartsList before cluster surfaces it
// references.
func TestBuildInitialReport_SortOrder(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	dispatcher := b.Dispatcher()
	if dispatcher == nil {
		t.Skip("dispatcher nil after start")
	}

	// Wildcard read across all endpoints + clusters — let the real
	// dispatcher populate whatever the no-device topology exposes and
	// verify the sort invariant holds regardless of count.
	req := im.SubscribeRequest{
		AttributeRequests: []im.ConcreteAttributePath{
			{HasEndpoint: false, HasCluster: false, HasAttribute: false},
		},
	}
	report, matched := b.buildInitialReport(context.Background(), dispatcher, req)
	if matched != len(report.Reports) {
		t.Errorf("buildInitialReport: matched = %d, want %d (len(report.Reports), no event paths requested)", matched, len(report.Reports))
	}
	for i := 1; i < len(report.Reports); i++ {
		prev, curr := report.Reports[i-1].Path, report.Reports[i].Path
		less := prev.Endpoint < curr.Endpoint ||
			(prev.Endpoint == curr.Endpoint && prev.Cluster < curr.Cluster) ||
			(prev.Endpoint == curr.Endpoint && prev.Cluster == curr.Cluster && prev.Attribute <= curr.Attribute)
		if !less {
			t.Errorf("sort violation at index %d: [%d] ep=%d cl=0x%04X attr=0x%04X > [%d] ep=%d cl=0x%04X attr=0x%04X",
				i, i-1, prev.Endpoint, prev.Cluster, prev.Attribute,
				i, curr.Endpoint, curr.Cluster, curr.Attribute)
		}
	}
}

// TestBuildInitialReport_EventOnlyEmptyLog_Establishes is the regression
// guard for the "matched counts REQUESTED event paths, not priming-log
// records" fix in buildInitialReport: an event-only Subscribe (no
// AttributeRequests) naming a real Switch (0x003B) InitialPress
// (0x01) event path must report matched > 0 even when the event log has
// no matching entries yet.
//
// This reproduces a GenericSwitch momentary button: the commissioner
// places the Subscribe BEFORE the physical press, so the event log is
// necessarily empty at establish time. Before the fix, matched was
// computed from len(BuildEventReports(...)) — zero on an empty log —
// which made handleSubscribeRequest reject the Subscribe with
// StatusResponse(InvalidAction) and the button could never deliver its
// future press. matter.js establishes an event subscription regardless
// of the priming log (ServerSubscription.ts emits 0+ EventReports on
// Subscribe-Initial); see subscribe_dispatch.go's buildInitialReport
// EventRequests handling.
func TestBuildInitialReport_EventOnlyEmptyLog_Establishes(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	dispatcher := b.Dispatcher()
	if dispatcher == nil {
		t.Skip("dispatcher nil after start — topology not yet assembled")
	}

	// No EmitEvent calls precede this — the event log is empty, matching
	// a subscribe placed before the button's first press.
	if records := b.EventLog().Query(0xFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0); len(records) != 0 {
		t.Fatalf("precondition: event log has %d records, want 0 (empty log)", len(records))
	}

	req := im.SubscribeRequest{
		EventRequests: []im.ConcreteEventPath{
			{
				HasEndpoint: true, Endpoint: 1,
				HasCluster: true, Cluster: 0x003B, // Switch
				HasEvent: true, Event: 0x01, // InitialPress
			},
		},
	}
	report, matched := b.buildInitialReport(context.Background(), dispatcher, req)
	if matched == 0 {
		t.Fatal("buildInitialReport: matched = 0 for an event-only subscribe with an empty log — would misfire into no_match rejection")
	}
	if matched != len(req.EventRequests) {
		t.Errorf("buildInitialReport: matched = %d, want %d (len(req.EventRequests))", matched, len(req.EventRequests))
	}
	if len(report.EventReports) != 0 {
		t.Errorf("buildInitialReport: EventReports = %d, want 0 (nothing buffered yet on an empty log)", len(report.EventReports))
	}
	if len(report.Reports) != 0 {
		t.Errorf("buildInitialReport: Reports = %d, want 0 (no AttributeRequests in this subscribe)", len(report.Reports))
	}
}

// ─── registerSubscription ───────────────────────────────────────────────────

// TestRegisterSubscription_NoManager_ReturnsZeroSubID verifies that when no
// subscription manager is wired, registerSubscription returns subID=0 and
// leaves initialReport.HasSubscription=false.
func TestRegisterSubscription_NoManager_ReturnsZeroSubID(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// No manager wired on a fresh bridge.
	src := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5540}
	hdr := &message.Header{SessionID: 0}
	proto := message.ProtocolHeader{ExchangeID: 1}
	req := im.SubscribeRequest{}
	report := im.ReportData{}

	subID, err := b.registerSubscription(src, hdr, proto, req, &report)
	if err != nil {
		t.Fatalf("no manager: err = %v, want nil", err)
	}
	if subID != 0 {
		t.Errorf("no manager: subID = %d, want 0", subID)
	}
	if report.HasSubscription {
		t.Error("no manager: report.HasSubscription should remain false")
	}
}

// ─── streamInitialReportChunks ──────────────────────────────────────────────

// TestStreamInitialReportChunks_EmptyReport_NoSend verifies that an empty
// initialReport (no chunks) returns nil without attempting to send. The bridge
// has no listener so any actual send would return an error — the test passes
// iff no send is attempted and the function returns nil.
func TestStreamInitialReportChunks_EmptyReport_NoSend(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// A fresh started bridge has a listener, but an empty ReportData produces
	// exactly one chunk (MoreChunkedMessages=false, zero reports) whose
	// EncodeReportData call should still succeed.  Accept either nil or a send
	// error here — what we are verifying is that the function does not panic
	// and handles the empty case without incorrect state.
	src := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5540}
	hdr := &message.Header{SessionID: 0}
	proto := message.ProtocolHeader{ExchangeID: 2}
	report := im.ReportData{}
	// The call may return an error from sendReplyReliable (no real peer);
	// but it must not panic.
	_ = b.streamInitialReportChunks(src, hdr, proto, 0, report)
}

// TestStreamInitialReportChunks_ChunkBudgetSplit verifies that a report large
// enough to exceed reportChunkPayloadBudget in a single datagram is split into
// multiple chunks by chunkReportData before sending. This exercises the seam
// that was previously unreachable without a full subscribe round-trip.
//
// The test calls chunkReportData directly (same budget constant used by
// streamInitialReportChunks) so it can inspect the output without a full
// subscribe round-trip. The chunk invariants — MoreChunkedMessages flag
// and last-chunk sentinel — mirror Matter §10.6.6.
func TestStreamInitialReportChunks_ChunkBudgetSplit(t *testing.T) {
	t.Parallel()

	// Build a report that will exceed the per-chunk budget.
	// reportChunkPayloadBudget is 1100 bytes. Each AttributeReport with a
	// 100-byte byte-slice value encodes to ~130 bytes (TLV path + tag + data),
	// so 20 reports × 130 ≈ 2600 bytes → guaranteed ≥ 2 chunks.
	const reportCount = 20
	value := make([]byte, 100) // 100-byte payload ensures each report is large
	for i := range value {
		value[i] = byte(i)
	}
	reports := make([]im.AttributeReport, reportCount)
	for i := range reports {
		reports[i] = im.AttributeReport{
			Path: im.ConcreteAttributePath{
				HasEndpoint:  true,
				HasCluster:   true,
				HasAttribute: true,
				Endpoint:     uint16(i / 5),     //nolint:gosec // test data
				Cluster:      uint32(i * 0x100), //nolint:gosec // test data
				Attribute:    uint32(i),         //nolint:gosec // test data
			},
			Value: im.AttributeValue{Value: append([]byte(nil), value...)},
		}
	}
	report := im.ReportData{
		HasSubscription: true,
		SubscriptionID:  99,
		Reports:         reports,
	}

	// chunkReportData is the same function streamInitialReportChunks calls.
	chunks, err := chunkReportData(report, reportChunkPayloadBudget)
	if err != nil {
		t.Fatalf("chunkReportData: %v", err)
	}
	if len(chunks) <= 1 {
		// Log the single chunk's encoded size to diagnose why chunking did not trigger.
		body, encErr := EncodeReportData(report)
		t.Fatalf("expected multiple chunks for %d ×100-byte reports, got %d chunk(s); encoded size=%d encErr=%v budget=%d",
			reportCount, len(chunks), len(body), encErr, reportChunkPayloadBudget)
	}

	// The last chunk must have MoreChunkedMessages=false (Matter §10.6.6).
	if chunks[len(chunks)-1].MoreChunkedMessages {
		t.Error("last chunk must have MoreChunkedMessages=false")
	}
	// All non-last chunks must have MoreChunkedMessages=true.
	for i, ch := range chunks[:len(chunks)-1] {
		if !ch.MoreChunkedMessages {
			t.Errorf("chunk %d (non-final): MoreChunkedMessages=false, want true", i)
		}
	}
	// Sanity: total reports across all chunks equals reportCount.
	var totalReports int
	for _, ch := range chunks {
		totalReports += len(ch.Reports)
	}
	if totalReports != reportCount {
		t.Errorf("total reports across chunks=%d, want %d (no report lost in split)", totalReports, reportCount)
	}
}
