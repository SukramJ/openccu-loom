// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// Parity tests for graceful secure-session teardown on the
// Secure-Channel wire path against matter.js HEAD:
//
//   - packages/protocol/src/securechannel/SecureChannelMessenger.ts:156
//     sendCloseSession → StatusReport(GeneralCode=Success,
//     ProtocolCode=CloseSession, requiresAck=false).
//   - packages/types/src/protocol/definitions/secure-channel.ts:76
//     CloseSession = 0x0003.
//   - packages/protocol/src/securechannel/SecureChannelProtocol.ts:54-82
//     handleInitialStatusReport — an inbound CloseSession closes the
//     session ("Closed by peer"); any other initial StatusReport is
//     ignored with a debug log.
//   - packages/protocol/src/protocol/ExchangeManager.ts:635/:658 —
//     gracefulClose observer ships the report on a fresh exchange.
//   - packages/node/src/behavior/system/network/ServerNetworkRuntime.ts
//     :410-447 — shutdown closes sessions before transport teardown.

import (
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/diagevent"
	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/operational"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// fakeSessionRegistry implements SessionRegistry plus both optional
// self-wiring capabilities.
type fakeSessionRegistry struct {
	mu           sync.Mutex
	closed       []uint16
	notifier     func(sessionID uint16, sess *channel.Session)
	reannounce   func()
	closeAll     func(deadline time.Time) int // optional override
	closeAllRuns int
}

func (f *fakeSessionRegistry) Close(sessionID uint16) error {
	f.mu.Lock()
	f.closed = append(f.closed, sessionID)
	f.mu.Unlock()
	return nil
}

func (f *fakeSessionRegistry) CloseAllGraceful(deadline time.Time) int {
	f.mu.Lock()
	f.closeAllRuns++
	fn := f.closeAll
	f.mu.Unlock()
	if fn != nil {
		return fn(deadline)
	}
	return 0
}

func (f *fakeSessionRegistry) SetGracefulCloseNotifier(fn func(sessionID uint16, sess *channel.Session)) {
	f.mu.Lock()
	f.notifier = fn
	f.mu.Unlock()
}

func (f *fakeSessionRegistry) SetReannounceTrigger(fn func()) {
	f.mu.Lock()
	f.reannounce = fn
	f.mu.Unlock()
}

func (f *fakeSessionRegistry) closedSessions() []uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint16(nil), f.closed...)
}

// closeSessionStatusReportBody encodes the exact wire payload matter.js
// emits for sendCloseSession: GeneralCode=Success (0x0000), ProtocolID=
// SecureChannel (0x00000000), ProtocolCode=CloseSession (0x0003).
func closeSessionStatusReportBody() []byte {
	return mrp.EncodeStatusReport(
		mrp.SCStatusGeneralSuccess,
		uint32(mrp.SecureChannelProtocolID),
		scStatusProtocolCloseSession,
		nil,
	)
}

// TestAttachSessionRegistry_SelfWiresManagerHooks verifies the attach
// call installs both reverse hooks on a registry that supports them —
// the Go translation of matter.js ExchangeManager.ts:635 observing
// session.gracefulClose at session-add time.
func TestAttachSessionRegistry_SelfWiresManagerHooks(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	reg := &fakeSessionRegistry{}
	b.AttachSessionRegistry(reg)
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if reg.notifier == nil {
		t.Error("graceful-close notifier not wired by AttachSessionRegistry")
	}
	if reg.reannounce == nil {
		t.Error("reannounce trigger not wired by AttachSessionRegistry")
	}
}

// TestInboundCloseSessionStatusReport_ClosesSession verifies the
// Secure-Channel router closes the session an authenticated
// CloseSession StatusReport rides on — mirrors matter.js
// SecureChannelProtocol.ts:78-82 ("Closed by peer" →
// session.handlePeerClose) — while every other StatusReport shape is
// ignored (SecureChannelProtocol.ts:71-77).
func TestInboundCloseSessionStatusReport_ClosesSession(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	reg := &fakeSessionRegistry{}
	b.AttachSessionRegistry(reg)

	hdr := &message.Header{SessionID: 9, MessageCounter: 1}
	proto := scProto(mrp.SCOpcodeStatusReport, 5, false, 0)

	if err := b.dispatchSecureChannel(loopbackSrc(), hdr, proto, closeSessionStatusReportBody()); err != nil {
		t.Fatalf("dispatchSecureChannel(CloseSession): %v", err)
	}
	if got := reg.closedSessions(); len(got) != 1 || got[0] != 9 {
		t.Fatalf("registry Close calls = %v, want [9]", got)
	}

	// A failure StatusReport must NOT close anything.
	failure := mrp.EncodeStatusReport(
		mrp.SCStatusGeneralFailure,
		uint32(mrp.SecureChannelProtocolID),
		mrp.SCStatusProtocolInvalidParameter,
		nil,
	)
	if err := b.dispatchSecureChannel(loopbackSrc(), &message.Header{SessionID: 9, MessageCounter: 2}, proto, failure); err != nil {
		t.Fatalf("dispatchSecureChannel(failure report): %v", err)
	}
	// A CloseSession on the unsecured session 0 is meaningless and
	// must be ignored.
	if err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), proto, closeSessionStatusReportBody()); err != nil {
		t.Fatalf("dispatchSecureChannel(session 0): %v", err)
	}
	// A truncated report must not panic nor close.
	if err := b.dispatchSecureChannel(loopbackSrc(), &message.Header{SessionID: 9, MessageCounter: 3}, proto, []byte{0x00, 0x00}); err != nil {
		t.Fatalf("dispatchSecureChannel(truncated): %v", err)
	}
	if got := reg.closedSessions(); len(got) != 1 {
		t.Errorf("registry Close calls after ignore cases = %v, want still [9]", got)
	}
}

// TestSendCloseSessionReport_WireShapeMatchesMatterJS decrypts the
// farewell datagram with the peer's half of the session and pins the
// wire shape to matter.js SecureChannelMessenger.ts:156 sendCloseSession
// → #sendStatusReport(Success, CloseSession, requiresAck=false) on a
// fresh bridge-initiated exchange (ExchangeManager.ts:658).
func TestSendCloseSessionReport_WireShapeMatchesMatterJS(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	peerConn, peerAddr := openPeerSocket(t)
	defer peerConn.Close()

	encKey := make([]byte, 16) // bridge → peer
	decKey := make([]byte, 16) // peer → bridge
	for i := range encKey {
		encKey[i] = byte(i)
		decKey[i] = byte(i + 16)
	}
	const (
		localSessionID uint16 = 7
		peerSessionID  uint16 = 0x1234
	)
	bridgeSess, err := channel.New(channel.Config{
		EncryptKey: encKey, DecryptKey: decKey,
		LocalNodeID: 0xB0B, PeerNodeID: 0xA11,
		PeerSessionID: peerSessionID, InitialCounter: 100,
	})
	if err != nil {
		t.Fatalf("channel.New (bridge): %v", err)
	}
	peerSess, err := channel.New(channel.Config{
		EncryptKey: decKey, DecryptKey: encKey,
		LocalNodeID: 0xA11, PeerNodeID: 0xB0B,
		InitialCounter: 100,
	})
	if err != nil {
		t.Fatalf("channel.New (peer): %v", err)
	}

	// Teach the bridge the peer's address the same way production
	// does: an authenticated Secure-Channel datagram on the session.
	ackProto := scProto(mrp.StandaloneAckOpcode, 3, true, 1)
	if err := b.dispatchSecureChannel(peerAddr, &message.Header{SessionID: localSessionID, MessageCounter: 1}, ackProto, nil); err != nil {
		t.Fatalf("dispatchSecureChannel(StandaloneAck): %v", err)
	}

	b.sendCloseSessionReport(localSessionID, bridgeSess)

	if err := peerConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 512)
	n, _, err := peerConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v (no CloseSession datagram received)", err)
	}
	datagram := buf[:n]

	rxHdr, hdrLen, err := message.UnmarshalHeader(datagram)
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	if rxHdr.SessionID != peerSessionID {
		t.Errorf("SessionID = %#x, want the peer's view %#x", rxHdr.SessionID, peerSessionID)
	}
	plain, duplicate, err := peerSess.Decrypt(&rxHdr, securityFlagsByte(&rxHdr), datagram[hdrLen:])
	if err != nil {
		t.Fatalf("peer Decrypt: %v", err)
	}
	if duplicate {
		t.Fatal("peer Decrypt flagged the farewell as a duplicate")
	}
	rxProto, protoLen, err := message.UnmarshalProtocolHeader(plain)
	if err != nil {
		t.Fatalf("UnmarshalProtocolHeader: %v", err)
	}
	if rxProto.ProtocolID != mrp.SecureChannelProtocolID {
		t.Errorf("ProtocolID = %#x, want SecureChannel (0x0000)", rxProto.ProtocolID)
	}
	if rxProto.Opcode != mrp.SCOpcodeStatusReport {
		t.Errorf("Opcode = %#x, want StatusReport (0x40)", rxProto.Opcode)
	}
	if !rxProto.Initiator {
		t.Error("Initiator = false, want true — the farewell rides a fresh bridge-initiated exchange")
	}
	if rxProto.NeedsAck {
		t.Error("NeedsAck = true, want false — matter.js sends CloseSession with requiresAck=false")
	}
	body := plain[protoLen:]
	if len(body) != 8 {
		t.Fatalf("StatusReport body length = %d, want 8", len(body))
	}
	if general := binary.LittleEndian.Uint16(body[0:2]); general != mrp.SCStatusGeneralSuccess {
		t.Errorf("GeneralCode = %#x, want Success (0x0000)", general)
	}
	if protocolID := binary.LittleEndian.Uint32(body[2:6]); protocolID != uint32(mrp.SecureChannelProtocolID) {
		t.Errorf("ProtocolID field = %#x, want 0x00000000", protocolID)
	}
	if code := binary.LittleEndian.Uint16(body[6:8]); code != scStatusProtocolCloseSession {
		t.Errorf("ProtocolCode = %#x, want CloseSession (0x0003)", code)
	}
}

// TestResolveSessionPeerAddr_FallbackChain verifies the address
// resolution order: the SC-learned record wins, then the subscription
// routing target, then the owed-ack exchange table, and an unknown
// session resolves to nil (the sender skips — best-effort like the
// try/catch around matter.js ExchangeManager.ts:658-666).
func TestResolveSessionPeerAddr_FallbackChain(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)

	_, scAddr := openPeerSocket(t)
	_, subAddr := openPeerSocket(t)
	_, exchAddr := openPeerSocket(t)

	// SC-learned record for session 41.
	b.sessionPeerAddrs.Store(uint16(41), scAddr)
	// Subscription target for session 42.
	b.routing.subTargets.Store(uint32(7), subTarget{src: subAddr, sessionID: 42})
	// Owed-ack exchange record for session 43.
	b.routing.exchangeSrcs.Store(
		mrp.ExchangeKey{SessionID: 43, ExchangeID: 1},
		exchangeReplyTarget{src: exchAddr, sessionID: 43},
	)

	if got := b.resolveSessionPeerAddr(41); got != scAddr {
		t.Errorf("resolve(41) = %v, want SC-learned %v", got, scAddr)
	}
	if got := b.resolveSessionPeerAddr(42); got != subAddr {
		t.Errorf("resolve(42) = %v, want subscription target %v", got, subAddr)
	}
	if got := b.resolveSessionPeerAddr(43); got != exchAddr {
		t.Errorf("resolve(43) = %v, want exchange record %v", got, exchAddr)
	}
	if got := b.resolveSessionPeerAddr(44); got != nil {
		t.Errorf("resolve(44) = %v, want nil for an unknown session", got)
	}
}

// TestStop_DrainsSessionsGracefully runs the full production chain
// with a real operational manager: Stop must ship a decryptable
// CloseSession StatusReport to the peer BEFORE the listener goes away
// and leave the session table empty. Mirrors matter.js's shutdown
// ordering (ServerNetworkRuntime.ts:410-447 — sessions close
// gracefully before transport teardown).
func TestStop_DrainsSessionsGracefully(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	peerConn, peerAddr := openPeerSocket(t)
	defer peerConn.Close()

	opMgr := operational.NewManager(nil)
	b.AttachSessionRegistry(opMgr)

	const (
		localNodeID   uint64 = 0xB0B
		peerNodeID    uint64 = 0xA11
		peerSessionID uint16 = 0x4321
	)
	secret := []byte("stop-drain-shared-secret")
	entry, err := opMgr.OpenFromPase(localNodeID, peerNodeID, peerSessionID, secret)
	if err != nil {
		t.Fatalf("OpenFromPase: %v", err)
	}

	// Peer-side keys: same HKDF split as OpenFromPase — I2R (peer
	// encrypt) || R2I (peer decrypt) per Matter §4.13.4.2.
	derived, err := hkdf.Key(sha256.New, secret, nil, "SessionKeys", 48)
	if err != nil {
		t.Fatalf("hkdf: %v", err)
	}
	peerSess, err := channel.New(channel.Config{
		EncryptKey:  derived[0:16],
		DecryptKey:  derived[16:32],
		LocalNodeID: peerNodeID,
		PeerNodeID:  localNodeID,
	})
	if err != nil {
		t.Fatalf("channel.New (peer): %v", err)
	}

	// Teach the bridge the peer's address via the SC receive path.
	ackProto := scProto(mrp.StandaloneAckOpcode, 3, true, 1)
	if err := b.dispatchSecureChannel(peerAddr, &message.Header{SessionID: entry.SessionID, MessageCounter: 1}, ackProto, nil); err != nil {
		t.Fatalf("dispatchSecureChannel(StandaloneAck): %v", err)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if active := opMgr.Active(); active != 0 {
		t.Errorf("operational sessions after Stop = %d, want 0", active)
	}

	if err := peerConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 512)
	n, _, err := peerConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v (Stop shipped no CloseSession farewell)", err)
	}
	rxHdr, hdrLen, err := message.UnmarshalHeader(buf[:n])
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	if rxHdr.SessionID != peerSessionID {
		t.Errorf("SessionID = %#x, want the peer's view %#x", rxHdr.SessionID, peerSessionID)
	}
	plain, _, err := peerSess.Decrypt(&rxHdr, securityFlagsByte(&rxHdr), buf[hdrLen:n])
	if err != nil {
		t.Fatalf("peer Decrypt: %v", err)
	}
	rxProto, protoLen, err := message.UnmarshalProtocolHeader(plain)
	if err != nil {
		t.Fatalf("UnmarshalProtocolHeader: %v", err)
	}
	if rxProto.Opcode != mrp.SCOpcodeStatusReport || rxProto.ProtocolID != mrp.SecureChannelProtocolID {
		t.Fatalf("farewell = protocol %#x opcode %#x, want SC StatusReport", rxProto.ProtocolID, rxProto.Opcode)
	}
	body := plain[protoLen:]
	if len(body) < 8 || binary.LittleEndian.Uint16(body[6:8]) != scStatusProtocolCloseSession {
		t.Fatalf("StatusReport body = %x, want ProtocolCode CloseSession (0x0003)", body)
	}
}

// TestStop_SessionCloseBudgetCapsShutdown verifies a wedged registry
// cannot stall Stop past the hard cap: the drain runs on its own
// goroutine and Stop proceeds once the budget elapses.
func TestStop_SessionCloseBudgetCapsShutdown(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	release := make(chan struct{})
	reg := &fakeSessionRegistry{
		closeAll: func(time.Time) int {
			<-release // wedged until the test ends
			return 0
		},
	}
	t.Cleanup(func() { close(release) })
	b.AttachSessionRegistry(reg)

	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	if err := b.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if elapsed := time.Since(start); elapsed > stopSessionCloseBudget+2*time.Second {
		t.Errorf("Stop took %v — the %v session-close budget did not cap the wedged drain", elapsed, stopSessionCloseBudget)
	}
	reg.mu.Lock()
	runs := reg.closeAllRuns
	reg.mu.Unlock()
	if runs != 1 {
		t.Errorf("CloseAllGraceful runs = %d, want 1", runs)
	}
}

// countingAdvertiser wraps the in-memory Noop advertiser and counts
// Publish calls so the reannounce trigger's republish is observable.
type countingAdvertiser struct {
	*mdns.Noop
	mu       sync.Mutex
	publishN int
}

func (c *countingAdvertiser) Publish(ctx context.Context, svc mdns.Service) error {
	c.mu.Lock()
	c.publishN++
	c.mu.Unlock()
	return c.Noop.Publish(ctx, svc)
}

func (c *countingAdvertiser) publishes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.publishN
}

// TestTriggerSessionReannounce_RepublishesActiveRecords verifies the
// broadcast-resume trigger re-publishes every active mDNS record
// (matter.js DeviceAdvertiser.ts:132-149 serviceDisconnected resumes
// broadcasting) and becomes a no-op once the bridge stopped
// (ServerNetworkRuntime.ts:427 — no re-announces during teardown).
func TestTriggerSessionReannounce_RepublishesActiveRecords(t *testing.T) {
	t.Parallel()
	adv := &countingAdvertiser{Noop: mdns.NewNoop()}
	b, err := New(NewFakeStore(), wbEmptySnapshotter, adv, Config{
		Listen: ":0", VendorID: 0x1234, ProductID: 0x5678, NodeLabel: "reannounce-test",
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})

	if err := adv.Publish(ctx, mdns.Service{
		InstanceName: "op-record",
		ServiceType:  "_matter._tcp",
		HostName:     "reannounce-test.local.",
		Port:         5540,
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	before := adv.publishes()

	b.triggerSessionReannounce()
	deadline := time.Now().Add(time.Second)
	for adv.publishes() == before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if adv.publishes() <= before {
		t.Fatalf("Publish count = %d after trigger, want > %d (re-publish of active records)", adv.publishes(), before)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	if err := b.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	after := adv.publishes()
	b.triggerSessionReannounce()
	time.Sleep(50 * time.Millisecond)
	if adv.publishes() != after {
		t.Errorf("trigger after Stop re-published records — must be a no-op during teardown")
	}
}

// TestSendCloseSessionReport_ForgetsPeerAddrEvenWhenTheFarewellFails
// pins that the per-session peer address is released on every graceful
// close, not only when the farewell datagram made it out. The session
// is gone either way — keeping the entry leaves a stale address that
// only a session-id wrap can overwrite. The failure modelled here is
// real: the reaper zeroises the keys, so the farewell's Encrypt fails.
func TestSendCloseSessionReport_ForgetsPeerAddrEvenWhenTheFarewellFails(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	_, peerAddr := openPeerSocket(t)

	const sessionID uint16 = 51
	b.sessionPeerAddrs.Store(sessionID, peerAddr)

	key := make([]byte, 16)
	sess, err := channel.New(channel.Config{
		EncryptKey: key, DecryptKey: key,
		LocalNodeID: 0xB0B, PeerNodeID: 0xA11,
		InitialCounter: 100,
	})
	if err != nil {
		t.Fatalf("channel.New: %v", err)
	}
	sess.Close()

	b.sendCloseSessionReport(sessionID, sess)

	if _, ok := b.sessionPeerAddrs.Load(sessionID); ok {
		t.Error("peer address for the closed session survived the failed farewell")
	}
}

// TestDiagnosticEventsRecordSessionAndDiscoveryKinds pins that the two
// non-pairing kinds the trace declares — and the operator-facing
// /matter/events surface promises — are actually produced. A declared
// kind no producer ever records reads to an operator as "nothing
// happened", which is indistinguishable from a healthy quiet bridge.
func TestDiagnosticEventsRecordSessionAndDiscoveryKinds(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	ring := diagevent.NewRing(64)
	b.AttachDiagnosticEvents(ring)

	// A peer closing its secure session records KindSession.
	reg := &fakeSessionRegistry{}
	b.AttachSessionRegistry(reg)
	closeBody := mrp.EncodeStatusReport(
		mrp.SCStatusGeneralSuccess,
		uint32(mrp.SecureChannelProtocolID),
		scStatusProtocolCloseSession,
		nil,
	)
	hdr := scHdr()
	hdr.SessionID = 42
	if err := b.dispatchSecureChannel(loopbackSrc(), hdr, scProto(mrp.SCOpcodeStatusReport, 3, false, 0), closeBody); err != nil {
		t.Fatalf("dispatchSecureChannel(CloseSession): %v", err)
	}

	// The mDNS re-announce that follows a teardown records KindDiscovery.
	b.triggerSessionReannounce()

	kinds := map[diagevent.Kind]bool{}
	for _, ev := range b.DiagnosticEvents() {
		kinds[ev.Kind] = true
	}
	for _, want := range []diagevent.Kind{diagevent.KindSession, diagevent.KindDiscovery} {
		if !kinds[want] {
			t.Errorf("no %q event recorded; the kind is declared and surfaced but never produced", want)
		}
	}
}
