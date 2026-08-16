// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for miscellaneous unexported helpers:
// udpPort, signalStatusResponseRX, protocolHeaderSize, securityFlagsByte,
// AttachExposureChecker (nil-safe), AnnounceFabric / AnnounceCommissioning /
// WithdrawCommissioning (noop-advertiser paths and the noop-vs-real
// advertiser log-level distinction), MatterEmitEvent alias.
// Lives in package bridge to access unexported symbols.

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/udp"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ─── udpPort ─────────────────────────────────────────────────────────────────

func TestUDPPort_EmptyString(t *testing.T) {
	t.Parallel()
	if got := udpPort(""); got != udp.MatterPort {
		t.Errorf("empty string: want %d, got %d", udp.MatterPort, got)
	}
}

func TestUDPPort_ColonPort(t *testing.T) {
	t.Parallel()
	if got := udpPort(":5541"); got != 5541 {
		t.Errorf(":5541: want 5541, got %d", got)
	}
}

func TestUDPPort_HostAndPort(t *testing.T) {
	t.Parallel()
	if got := udpPort("0.0.0.0:8888"); got != 8888 {
		t.Errorf("0.0.0.0:8888: want 8888, got %d", got)
	}
}

func TestUDPPort_ColonZero_FallsBack(t *testing.T) {
	t.Parallel()
	// ":0" → dynamic port at bind time; udpPort cannot determine the
	// real port at config parse time — returns MatterPort as fallback.
	if got := udpPort(":0"); got != udp.MatterPort {
		t.Errorf(":0: want MatterPort=%d, got %d", udp.MatterPort, got)
	}
}

func TestUDPPort_NoColon_FallsBack(t *testing.T) {
	t.Parallel()
	if got := udpPort("5540"); got != udp.MatterPort {
		t.Errorf("no colon: want MatterPort=%d, got %d", udp.MatterPort, got)
	}
}

func TestUDPPort_NonNumericPort_FallsBack(t *testing.T) {
	t.Parallel()
	if got := udpPort(":abc"); got != udp.MatterPort {
		t.Errorf("non-numeric: want MatterPort=%d, got %d", udp.MatterPort, got)
	}
}

// ─── signalStatusResponseRX ──────────────────────────────────────────────────

func TestSignalStatusResponseRX_NoPendingWait_IsNoop(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// No wait is registered — must not panic.
	b.signalStatusResponseRX(0, 7, true)
}

func TestSignalStatusResponseRX_ClosesChannel(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	const (
		sess = uint16(1)
		exch = uint16(42)
	)
	ch := make(chan struct{})
	b.routing.statusResponseWaits.Store(mrp.ExchangeKey{SessionID: sess, ExchangeID: exch, Initiator: true}, ch)
	b.signalStatusResponseRX(sess, exch, true)
	// The channel should be closed.
	select {
	case <-ch:
		// OK — channel closed.
	default:
		t.Error("signalStatusResponseRX did not close the registered channel")
	}
	// Entry should be deleted from the map.
	if _, loaded := b.routing.statusResponseWaits.Load(mrp.ExchangeKey{SessionID: sess, ExchangeID: exch, Initiator: true}); loaded {
		t.Error("signalStatusResponseRX should have deleted the map entry")
	}
}

func TestSignalStatusResponseRX_IdempotentOnClosedChannel(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	const (
		sess = uint16(1)
		exch = uint16(55)
	)
	ch := make(chan struct{})
	close(ch) // already closed
	b.routing.statusResponseWaits.Store(mrp.ExchangeKey{SessionID: sess, ExchangeID: exch, Initiator: true}, ch)
	// Must not panic despite closed channel.
	b.signalStatusResponseRX(sess, exch, true)
}

// TestStatusResponseWait_SessionScopedExchangeCollision verifies that two
// sessions sharing the same exchange ID keep independent rendezvous
// channels: signalling the wrong session's (session, exchange) pair must
// not release a waiter armed for a different session on the same exchange
// ID. Mirrors matter.js ExchangeManager.ts:287 — exchange identity is
// scoped to its owning session.
func TestStatusResponseWait_SessionScopedExchangeCollision(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	const exch = uint16(5)

	waitCh := b.armStatusResponseWait(10, exch, true)

	// A StatusResponse for a different session on the same exchange ID
	// must not release the waiter.
	b.signalStatusResponseRX(20, exch, true)
	select {
	case <-waitCh:
		t.Fatal("waitCh closed after signal for an unrelated session; want still open")
	default:
		// OK — still open.
	}

	// The StatusResponse for the actual owning session releases it.
	b.signalStatusResponseRX(10, exch, true)
	select {
	case <-waitCh:
		// OK — closed.
	default:
		t.Fatal("waitCh still open after signal for the owning session; want closed")
	}
}

// ─── protocolHeaderSize ───────────────────────────────────────────────────────

func TestProtocolHeaderSize_Minimal(t *testing.T) {
	t.Parallel()
	p := message.ProtocolHeader{}
	if got := protocolHeaderSize(p); got != 6 {
		t.Errorf("minimal: want 6, got %d", got)
	}
}

func TestProtocolHeaderSize_WithVendorID(t *testing.T) {
	t.Parallel()
	p := message.ProtocolHeader{HasVendorID: true}
	if got := protocolHeaderSize(p); got != 8 {
		t.Errorf("with VendorID: want 8, got %d", got)
	}
}

func TestProtocolHeaderSize_WithAck(t *testing.T) {
	t.Parallel()
	p := message.ProtocolHeader{HasAck: true}
	if got := protocolHeaderSize(p); got != 10 {
		t.Errorf("with Ack: want 10, got %d", got)
	}
}

func TestProtocolHeaderSize_WithVendorIDAndAck(t *testing.T) {
	t.Parallel()
	p := message.ProtocolHeader{HasVendorID: true, HasAck: true}
	if got := protocolHeaderSize(p); got != 12 {
		t.Errorf("with VendorID+Ack: want 12, got %d", got)
	}
}

func TestProtocolHeaderSize_WithSecuredExtension(t *testing.T) {
	t.Parallel()
	// 6 fixed + 2 length prefix + 3-byte block = 11; the payload must
	// start past the secured-extension block, not at its first byte.
	p := message.ProtocolHeader{HasSecuredExt: true, SecuredExtension: []byte{0x01, 0x02, 0x03}}
	if got := protocolHeaderSize(p); got != 11 {
		t.Errorf("with SecuredExt: want 11, got %d", got)
	}
}

// ─── securityFlagsByte ───────────────────────────────────────────────────────

func TestSecurityFlagsByte_AllZero(t *testing.T) {
	t.Parallel()
	hdr := &message.Header{}
	if got := securityFlagsByte(hdr); got != 0 {
		t.Errorf("all-zero header: want 0x00, got 0x%02X", got)
	}
}

func TestSecurityFlagsByte_PrivacyBit(t *testing.T) {
	t.Parallel()
	hdr := &message.Header{Privacy: true}
	got := securityFlagsByte(hdr)
	if got&0x80 == 0 {
		t.Errorf("Privacy bit not set: got 0x%02X", got)
	}
}

func TestSecurityFlagsByte_ControlBit(t *testing.T) {
	t.Parallel()
	hdr := &message.Header{Control: true}
	got := securityFlagsByte(hdr)
	if got&0x40 == 0 {
		t.Errorf("Control bit not set: got 0x%02X", got)
	}
}

func TestSecurityFlagsByte_ExtensionBit(t *testing.T) {
	t.Parallel()
	hdr := &message.Header{HasExtension: true}
	got := securityFlagsByte(hdr)
	if got&0x20 == 0 {
		t.Errorf("HasExtension bit not set: got 0x%02X", got)
	}
}

func TestSecurityFlagsByte_SessionType(t *testing.T) {
	t.Parallel()
	hdr := &message.Header{SessionType: 3}
	got := securityFlagsByte(hdr)
	if got&0x1F != 3 {
		t.Errorf("SessionType bits: want 3 in low 5 bits, got 0x%02X", got)
	}
}

// ─── AttachExposureChecker nil-safe ─────────────────────────────────────────

func TestAttachExposureChecker_NilBridge_NoPanic(t *testing.T) {
	t.Parallel()
	var b *Bridge
	b.AttachExposureChecker(nil) // must not panic
}

func TestAttachExposureChecker_NilChecker_NoPanic(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	b.AttachExposureChecker(nil) // must not panic
}

// ─── AnnounceFabric / AnnounceCommissioning / WithdrawCommissioning ──────────

func TestAnnounceFabric_NilAdvertiser_NoPanic(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t) // noop advertiser is not nil, but we test without it
	// newStartedBridge uses mdns.NewNoop() which satisfies the advertiser interface.
	// Call should not panic.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b.AnnounceFabric(ctx, [8]byte{}, 0xDEAD)
}

func TestAnnounceCommissioning_NilBridge_NoPanic(t *testing.T) {
	t.Parallel()
	var b *Bridge
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_ = b.AnnounceCommissioning(ctx, CommissioningAdvertisement{})
}

func TestAnnounceAndWithdrawCommissioning_RoundTrip(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	params := CommissioningAdvertisement{
		Discriminator: 0xABC,
		VendorID:      0x1234,
		ProductID:     0x5678,
		NodeLabel:     "test",
	}
	if err := b.AnnounceCommissioning(ctx, params); err != nil {
		t.Fatalf("AnnounceCommissioning: %v", err)
	}
	// Withdraw should be idempotent and not panic.
	b.WithdrawCommissioning(ctx)
	b.WithdrawCommissioning(ctx) // second call: no-op
}

// ─── noop-vs-real advertiser log-level distinction ───────────────────────────
//
// AnnounceCommissioning / AnnounceFabric log at WARN with a
// "*_not_advertised" message when the wired advertiser is the in-memory
// [mdns.Noop] (Publish succeeds without ever putting a record on the
// network — an operator reading an INFO "published" line would then chase
// a commissioning failure that has an obvious, already-known cause). A
// real advertiser keeps the original INFO "*_published" line.

// fakeAdvertiser is a minimal non-Noop [mdns.Advertiser] used to verify
// that the "published" log path is taken whenever the wired advertiser is
// not [mdns.Noop] — regardless of whether it actually reaches the network.
type fakeAdvertiser struct {
	mu    sync.Mutex
	items map[string]mdns.Service
}

func newFakeAdvertiser() *fakeAdvertiser {
	return &fakeAdvertiser{items: make(map[string]mdns.Service)}
}

func (f *fakeAdvertiser) Publish(_ context.Context, svc mdns.Service) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[svc.InstanceName+"|"+svc.ServiceType] = svc
	return nil
}

func (f *fakeAdvertiser) Withdraw(_ context.Context, instanceName, serviceType string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, instanceName+"|"+serviceType)
	return nil
}

func (f *fakeAdvertiser) Active() []mdns.Service {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]mdns.Service, 0, len(f.items))
	for k := range f.items {
		out = append(out, f.items[k])
	}
	return out
}

func (f *fakeAdvertiser) Close() error { return nil }

// newBridgeWithAdvertiser constructs a Bridge wired to advertiser and a
// logger writing to buf, without starting the UDP listener —
// AnnounceFabric/AnnounceCommissioning only touch the advertiser and the
// logger, not the started runtime.
func newBridgeWithAdvertiser(t *testing.T, advertiser mdns.Advertiser, buf *bytes.Buffer) *Bridge {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(buf, nil))
	b, err := New(
		NewFakeStore(),
		wbEmptySnapshotter,
		advertiser,
		Config{
			Listen:    ":0",
			VendorID:  0x1234,
			ProductID: 0x5678,
			NodeLabel: "log-test",
		},
		logger,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

// TestAnnounceCommissioning_NoopAdvertiser_LogsNotAdvertised verifies that
// a Noop advertiser produces the WARN "commissioning_not_advertised" line
// and never the INFO "commissioning_published" line.
func TestAnnounceCommissioning_NoopAdvertiser_LogsNotAdvertised(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	b := newBridgeWithAdvertiser(t, mdns.NewNoop(), &buf)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := b.AnnounceCommissioning(ctx, CommissioningAdvertisement{
		Discriminator: 0xABC,
		VendorID:      0x1234,
		ProductID:     0x5678,
		NodeLabel:     "test",
	}); err != nil {
		t.Fatalf("AnnounceCommissioning: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "matter.mdns.commissioning_not_advertised") {
		t.Errorf("expected commissioning_not_advertised in log output, got: %s", out)
	}
	if strings.Contains(out, "matter.mdns.commissioning_published") {
		t.Errorf("noop advertiser must not log commissioning_published, got: %s", out)
	}
}

// TestAnnounceCommissioning_RealAdvertiser_LogsPublished verifies that a
// non-Noop advertiser produces the INFO "commissioning_published" line and
// never the WARN "commissioning_not_advertised" line.
func TestAnnounceCommissioning_RealAdvertiser_LogsPublished(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	b := newBridgeWithAdvertiser(t, newFakeAdvertiser(), &buf)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := b.AnnounceCommissioning(ctx, CommissioningAdvertisement{
		Discriminator: 0xABC,
		VendorID:      0x1234,
		ProductID:     0x5678,
		NodeLabel:     "test",
	}); err != nil {
		t.Fatalf("AnnounceCommissioning: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "matter.mdns.commissioning_published") {
		t.Errorf("expected commissioning_published in log output, got: %s", out)
	}
	if strings.Contains(out, "matter.mdns.commissioning_not_advertised") {
		t.Errorf("real advertiser must not log commissioning_not_advertised, got: %s", out)
	}
}

// TestAnnounceFabric_NoopAdvertiser_LogsNotAdvertised verifies that a Noop
// advertiser produces the WARN "fabric_not_advertised" line and never the
// INFO "fabric_published" line.
func TestAnnounceFabric_NoopAdvertiser_LogsNotAdvertised(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	b := newBridgeWithAdvertiser(t, mdns.NewNoop(), &buf)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b.AnnounceFabric(ctx, [8]byte{0x01}, 0xDEAD)

	out := buf.String()
	if !strings.Contains(out, "matter.mdns.fabric_not_advertised") {
		t.Errorf("expected fabric_not_advertised in log output, got: %s", out)
	}
	if strings.Contains(out, "matter.mdns.fabric_published") {
		t.Errorf("noop advertiser must not log fabric_published, got: %s", out)
	}
}

// TestAnnounceFabric_RealAdvertiser_LogsPublished verifies that a non-Noop
// advertiser produces the INFO "fabric_published" line and never the WARN
// "fabric_not_advertised" line.
func TestAnnounceFabric_RealAdvertiser_LogsPublished(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	b := newBridgeWithAdvertiser(t, newFakeAdvertiser(), &buf)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b.AnnounceFabric(ctx, [8]byte{0x01}, 0xDEAD)

	out := buf.String()
	if !strings.Contains(out, "matter.mdns.fabric_published") {
		t.Errorf("expected fabric_published in log output, got: %s", out)
	}
	if strings.Contains(out, "matter.mdns.fabric_not_advertised") {
		t.Errorf("real advertiser must not log fabric_not_advertised, got: %s", out)
	}
}

// ─── MatterEmitEvent alias ───────────────────────────────────────────────────

func TestMatterEmitEvent_IsAliasOfEmitEvent(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// MatterEmitEvent is an alias of EmitEvent; calling it must not panic.
	// No subscription manager is wired in tests so the event is only logged.
	b.MatterEmitEvent(0, 0x0028, 0x00, nil, interfaces.MatterEventPriorityInfo)
}

// ─── endpointCount ───────────────────────────────────────────────────────────

// TestEndpointCount_NilTopology_ReturnsZero verifies that endpointCount
// returns 0 when the topology has not been assembled yet.
func TestEndpointCount_NilTopology_ReturnsZero(t *testing.T) {
	t.Parallel()
	b := &Bridge{}
	if got := b.endpointCount(); got != 0 {
		t.Errorf("nil topology: want 0, got %d", got)
	}
}

// TestEndpointCount_StartedBridgeWithoutSources_IsZero verifies that a
// bridge started against an empty snapshot reports zero bridged
// endpoints: the assembler still seeds the root and the aggregator, and
// neither is a bridged device.
func TestEndpointCount_StartedBridgeWithoutSources_IsZero(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	if got := b.endpointCount(); got != 0 {
		t.Errorf("started bridge without sources: want 0, got %d", got)
	}
}

// ─── replyTimedStatus ────────────────────────────────────────────────────────

// TestReplyTimedStatus_NilSrc_DoesNotPanic verifies that replyTimedStatus
// does not panic when src is nil (send fails; error is returned).
func TestReplyTimedStatus_NilSrc_DoesNotPanic(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	hdr := &message.Header{SessionID: 0, MessageCounter: 1}
	proto := message.ProtocolHeader{ExchangeID: 1}
	// nil src → sendReply will fail; function must not panic.
	_ = b.replyTimedStatus(nil, hdr, proto, "test", im.StatusSuccess)
}

// TestReplyTimedStatus_WithSrc_DoesNotPanic verifies that replyTimedStatus
// returns without panicking when src is a valid loopback address (reply goes
// out on the wire; session=0 so no encryption needed).
func TestReplyTimedStatus_WithSrc_DoesNotPanic(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	hdr := &message.Header{SessionID: 0, MessageCounter: 2}
	proto := message.ProtocolHeader{ExchangeID: 2}
	_ = b.replyTimedStatus(loopbackSrc(), hdr, proto, "test", im.StatusSuccess)
}

// ─── handlerContext ───────────────────────────────────────────────────────────

// TestHandlerContext_NilCtx_ReturnsBackground verifies that a bridge
// with no handlerCtx set returns context.Background().
func TestHandlerContext_NilCtx_ReturnsBackground(t *testing.T) {
	t.Parallel()
	b := &Bridge{}
	ctx := b.handlerContext()
	if ctx == nil {
		t.Error("handlerContext: returned nil, want non-nil context")
	}
}

// TestHandlerContext_StartedBridge_ReturnsNonNil verifies that after
// Start, handlerContext returns a non-nil, non-Background context.
func TestHandlerContext_StartedBridge_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	ctx := b.handlerContext()
	if ctx == nil {
		t.Error("handlerContext on started bridge: returned nil")
	}
}

// ─── handleDatagram ───────────────────────────────────────────────────────────

// TestHandleDatagram_TooShortDoesNotPanic verifies that handleDatagram
// does not panic for a too-short buffer (dispatch returns an error which
// handleDatagram discards).
func TestHandleDatagram_TooShortDoesNotPanic(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// Must not panic; errors are silently dropped by handleDatagram.
	b.handleDatagram([]byte{0x00}, loopbackSrc())
}

// ─── AttachRootClusters / AttachAggregatorClusters with live topology ─────────
//
// The topology is populated after Start (initial Reassemble). Calling
// Attach*Clusters on a started bridge exercises the topology-update branches
// inside those methods (b.topology != nil paths at 71.4% without these tests).

func TestAttachRootClusters_WithStartedBridge_TopologyPath(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// After Start, b.topology is non-nil (initial Reassemble ran).
	// Attaching clusters must not panic and must update the root endpoint.
	c := &noopCluster{id: 0x0028}
	b.AttachRootClusters([]interfaces.MatterClusterServer{c})
	// A second call exercises the overwrite path.
	b.AttachRootClusters(nil)
}

func TestAttachAggregatorClusters_WithStartedBridge_TopologyPath(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	c := &noopCluster{id: 0x001D}
	b.AttachAggregatorClusters([]interfaces.MatterClusterServer{c})
	b.AttachAggregatorClusters(nil)
}

// noopCluster is a minimal MatterClusterServer for topology-path tests.
type noopCluster struct {
	id uint32
}

func (n *noopCluster) MatterClusterID() uint32 { return n.id }
func (n *noopCluster) MatterRead(_ uint32) (any, bool) {
	return nil, false
}

func (n *noopCluster) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return nil
}

func (n *noopCluster) MatterInvoke(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, nil
}
func (n *noopCluster) MatterReportable() []uint32 { return nil }
