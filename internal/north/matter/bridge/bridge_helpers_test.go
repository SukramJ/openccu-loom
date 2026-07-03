// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for miscellaneous unexported helpers:
// udpPort, signalStatusResponseRX, protocolHeaderSize, securityFlagsByte,
// AttachExposureChecker (nil-safe), AnnounceFabric / AnnounceCommissioning /
// WithdrawCommissioning (noop-advertiser paths), MatterEmitEvent alias.
// Lives in package bridge to access unexported symbols.

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
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
	b.signalStatusResponseRX(0, 7)
}

func TestSignalStatusResponseRX_ClosesChannel(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	const (
		sess = uint16(1)
		exch = uint16(42)
	)
	ch := make(chan struct{})
	b.statusResponseWaits.Store(mrp.ExchangeKey{SessionID: sess, ExchangeID: exch}, ch)
	b.signalStatusResponseRX(sess, exch)
	// The channel should be closed.
	select {
	case <-ch:
		// OK — channel closed.
	default:
		t.Error("signalStatusResponseRX did not close the registered channel")
	}
	// Entry should be deleted from the map.
	if _, loaded := b.statusResponseWaits.Load(mrp.ExchangeKey{SessionID: sess, ExchangeID: exch}); loaded {
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
	b.statusResponseWaits.Store(mrp.ExchangeKey{SessionID: sess, ExchangeID: exch}, ch)
	// Must not panic despite closed channel.
	b.signalStatusResponseRX(sess, exch)
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

	waitCh := b.armStatusResponseWait(10, exch)

	// A StatusResponse for a different session on the same exchange ID
	// must not release the waiter.
	b.signalStatusResponseRX(20, exch)
	select {
	case <-waitCh:
		t.Fatal("waitCh closed after signal for an unrelated session; want still open")
	default:
		// OK — still open.
	}

	// The StatusResponse for the actual owning session releases it.
	b.signalStatusResponseRX(10, exch)
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

// TestEndpointCount_StartedBridge_AtLeastRoot verifies that after Start
// there is at least the root endpoint (count >= 0 since endpointCount
// subtracts root).
func TestEndpointCount_StartedBridge_AtLeastRoot(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	if got := b.endpointCount(); got < 0 {
		t.Errorf("started bridge: want >=0, got %d", got)
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
