// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// scenarioHarness drives a single scenario file end-to-end against a
// real Bridge + real CASE session pair on UDP loopback. Phase-A scope:
// the F4 round-trip — CCU echo → fresh-exchange ReportData → peer
// StatusResponse → bridge status_ack log. Phase B/C extend the kinds
// table without changing the harness shape.
type scenarioHarness struct {
	t  *testing.T
	s  *scenario
	br *Bridge

	peerConn *net.UDPConn
	peerAddr *net.UDPAddr
	bridgeIn *net.UDPAddr

	bridgeNodeID uint64
	peerNodeID   uint64

	subMgr *subscription.Manager

	// Per-subscription state, indexed in scenario order. Each
	// subscription has its own (bridgeSess, peerSess) pair and its
	// own *subscription.Subscription. peerSessionsByID is the
	// reverse-lookup for inbound peer datagrams (the harness sees
	// only the session id in the header).
	subs              []*subscription.Subscription
	bridgeSessions    []*channel.Session
	peerSessions      []*channel.Session
	bridgeSessionByID map[uint16]*channel.Session
	peerSessionByID   map[uint16]*channel.Session

	topology *scenarioTopology

	// t0 anchors deterministic engine ticks; captured at setup.
	// engine_tick_at offsets compute against this.
	t0 time.Time

	bindings   map[string]any
	logCapture *scenarioLogCapture
}

// Per-subscription session keys derive from a fixed base + the
// subscription index so multi-subscription scenarios get distinct
// AES-CCM-128 contexts. Wire bytes are deterministic across runs
// except for message counters and exchange IDs (the harness binds
// those via $vars).
const (
	scenarioBridgeNodeID uint64 = 0xBBBBAAAA
	scenarioPeerNodeID   uint64 = 0xCCCCDDDD
)

// newScenarioHarness fully wires a harness for s. Phase-A wiring:
//   - Bridge built via newStartedBridgeWithLogger so log records can
//     be captured.
//   - CASE session pair injected via AttachSessionLookup.
//   - Real subscription.Manager attached so reportSubscription has a
//     live Subscription to ship.
//   - One subscription created on s.Given.Subscription's path with
//     SessionID = s.Given.SessionID.
//   - subTarget planted for that subscription pointing at the peer
//     UDP socket with peerInitiator=true (commissioner-opened
//     Subscribe exchange = s.Given.PeerSubscribeExchangeID).
func newScenarioHarness(t *testing.T, s *scenario) *scenarioHarness {
	t.Helper()

	logCap := newScenarioLogCapture()
	logger := slog.New(logCap)

	topology := resolveTopology(s.Given.Topology)
	snapshotter := wbEmptySnapshotter
	includeMeasurements := false
	if topology != nil {
		snapshotter = topology.snapshotter
		includeMeasurements = true
	}

	br, err := New(
		NewFakeStore(),
		snapshotter,
		mdns.NewNoop(),
		Config{
			Listen:              "127.0.0.1:0",
			VendorID:            0x1234,
			ProductID:           0x5678,
			NodeLabel:           "scenario-harness",
			IncludeMeasurements: includeMeasurements,
		},
		logger,
	)
	if err != nil {
		t.Fatalf("scenario: bridge New: %v", err)
	}
	// Scenarios exercise the wire, not the AccessControl entries: they drive
	// operational reads on fabrics no scenario ever provisions an ACL for.
	// An unwired source denies those, so the harness states its opt-out
	// rather than inheriting it — the scenario for per-fabric projection
	// otherwise reports an empty AttributeDataIB, which reads as a codec
	// defect rather than as a denied read.
	br.AttachACLLister(endpoint.UnenforcedACL{})
	startCtx, startCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer startCancel()
	if err := br.Start(startCtx); err != nil {
		t.Fatalf("scenario: bridge Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = br.Stop(stopCtx)
	})

	// Engage the outbound-reliable tracker so sendUnsolicitedIM ships
	// reports as Reliable (NeedsAck=true) and stores retransmit
	// entries. The pump goroutine does NOT start (AttachAckTracker
	// after Start leaves it dormant); the tick_retransmit step kind
	// drives ticks manually for deterministic fault-injection
	// scenarios.
	br.AttachAckTracker(mrp.NewAckTracker(50 * time.Millisecond))

	bridgeListenerAddr := br.listener.LocalAddr()

	peerConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("scenario: peer ListenUDP: %v", err)
	}
	t.Cleanup(func() { _ = peerConn.Close() })
	peerAddr, ok := peerConn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("scenario: unexpected peer addr type %T", peerConn.LocalAddr())
	}

	if len(s.Given.Subscriptions) == 0 {
		t.Fatalf("scenario: given carries no subscriptions; loader bug?")
	}

	bridgeSessionByID := make(map[uint16]*channel.Session, len(s.Given.Subscriptions))
	peerSessionByID := make(map[uint16]*channel.Session, len(s.Given.Subscriptions))
	bridgeSessions := make([]*channel.Session, len(s.Given.Subscriptions))
	peerSessions := make([]*channel.Session, len(s.Given.Subscriptions))

	for i, spec := range s.Given.Subscriptions {
		bridgeKey := bytes.Repeat([]byte{0xA0 | byte(i)}, 16)
		peerKey := bytes.Repeat([]byte{0xB0 | byte(i)}, 16)
		bSess, err := channel.New(channel.Config{
			EncryptKey:     bridgeKey,
			DecryptKey:     peerKey,
			LocalNodeID:    scenarioBridgeNodeID,
			PeerNodeID:     scenarioPeerNodeID,
			InitialCounter: 100 + uint32(i)*1000,
		})
		if err != nil {
			t.Fatalf("scenario: bridge session[%d]: %v", i, err)
		}
		pSess, err := channel.New(channel.Config{
			EncryptKey:     peerKey,
			DecryptKey:     bridgeKey,
			LocalNodeID:    scenarioPeerNodeID,
			PeerNodeID:     scenarioBridgeNodeID,
			InitialCounter: 200 + uint32(i)*1000,
		})
		if err != nil {
			t.Fatalf("scenario: peer session[%d]: %v", i, err)
		}
		if _, dupe := bridgeSessionByID[spec.SessionID]; dupe {
			t.Fatalf("scenario: subscriptions[%d]: session_id %d already used by an earlier subscription", i, spec.SessionID)
		}
		bridgeSessionByID[spec.SessionID] = bSess
		peerSessionByID[spec.SessionID] = pSess
		bridgeSessions[i] = bSess
		peerSessions[i] = pSess
	}

	fabricByID := make(map[uint16]uint8, len(s.Given.Subscriptions))
	for _, spec := range s.Given.Subscriptions {
		if spec.FabricIndex != 0 {
			fabricByID[spec.SessionID] = spec.FabricIndex
		}
	}
	br.AttachSessionLookup(scenarioFabricResolver{
		sessions: bridgeSessionByID,
		fabrics:  fabricByID,
	})

	// Wire the real bridge reporter so engine-tick step kinds drive
	// the production ship path. The reporter is harmless to the
	// direct-invocation path (fire_attribute_change) — that path
	// bypasses the engine and calls reportSubscription itself.
	subMgr := subscription.NewManager(subscription.Config{}, br.SubscriptionReporter(), logger)
	br.AttachSubscriptionManager(subMgr)
	engineManual := false
	for _, spec := range s.Given.Subscriptions {
		if spec.EngineManualOnly {
			engineManual = true
			break
		}
	}
	if !engineManual {
		subMgrCtx, subMgrCancel := context.WithCancel(context.Background())
		subMgr.Start(subMgrCtx)
		t.Cleanup(func() {
			subMgrCancel()
			subMgr.Stop()
		})
	}

	// Anchor t0 immediately before the Subscribe calls so engine_tick_at
	// offsets relate to subscription admission time.
	t0 := time.Now()

	subs := make([]*subscription.Subscription, len(s.Given.Subscriptions))
	for i, spec := range s.Given.Subscriptions {
		if spec.SkipAutoSubscribe {
			continue
		}
		paths := make([]im.ConcreteAttributePath, 0, len(spec.effectivePaths()))
		for _, p := range spec.effectivePaths() {
			paths = append(paths, im.ConcreteAttributePath{
				Endpoint:     p.Endpoint,
				Cluster:      p.Cluster,
				Attribute:    p.Attribute,
				HasEndpoint:  true,
				HasCluster:   true,
				HasAttribute: true,
			})
		}
		minFloor := spec.MinIntervalFloorSeconds
		if minFloor == 0 {
			minFloor = 1
		}
		maxCeil := spec.MaxIntervalCeilingSeconds
		if maxCeil == 0 {
			maxCeil = 60
		}
		sub, err := subMgr.Subscribe(subscription.SubscribeArgs{
			FabricIndex:        uint8(1 + i), //nolint:gosec // G115: i is bounded by len(s.Given.Subscriptions) which is small; fits uint8 by test design
			SessionID:          spec.SessionID,
			MinIntervalFloor:   minFloor,
			MaxIntervalCeiling: maxCeil,
			AttributePaths:     paths,
		})
		if err != nil {
			t.Fatalf("scenario: subscribe[%d]: %v", i, err)
		}
		br.routing.subTargets.Store(sub.ID, subTarget{
			src:                 peerAddr,
			hasPeerSourceNodeID: true,
			peerSourceNodeID:    scenarioPeerNodeID,
			exchangeID:          spec.PeerSubscribeExchangeID,
			sessionID:           spec.SessionID,
			peerInitiator:       true,
		})
		subs[i] = sub
	}

	return &scenarioHarness{
		t:                 t,
		s:                 s,
		br:                br,
		peerConn:          peerConn,
		peerAddr:          peerAddr,
		bridgeIn:          bridgeListenerAddr,
		bridgeNodeID:      scenarioBridgeNodeID,
		peerNodeID:        scenarioPeerNodeID,
		subMgr:            subMgr,
		subs:              subs,
		bridgeSessions:    bridgeSessions,
		peerSessions:      peerSessions,
		bridgeSessionByID: bridgeSessionByID,
		peerSessionByID:   peerSessionByID,
		topology:          topology,
		t0:                t0,
		bindings:          make(map[string]any),
		logCapture:        logCap,
	}
}

// activeSub returns the subscription state the step targets via the
// optional subscription_idx field. Defaults to index 0 (the primary
// subscription, all single-subscription scenarios).
func (h *scenarioHarness) activeSub(idx int) (*subscription.Subscription, *channel.Session, scenarioSubSpec) {
	if idx < 0 || idx >= len(h.subs) {
		h.t.Fatalf("scenario: subscription_idx %d out of range (have %d subscriptions)", idx, len(h.subs))
	}
	return h.subs[idx], h.peerSessions[idx], h.s.Given.Subscriptions[idx]
}

// scenarioFabricResolver implements both bridge.SessionLookup and
// bridge.SessionFabricResolver so per-spec FabricIndex values
// flow through the bridge's resolveSessionFabric path and stamp
// the FabricFiltered dispatch context.
type scenarioFabricResolver struct {
	sessions map[uint16]*channel.Session
	fabrics  map[uint16]uint8
}

func (r scenarioFabricResolver) Lookup(id uint16) (*channel.Session, bool) {
	sess, ok := r.sessions[id]
	return sess, ok
}

func (r scenarioFabricResolver) FabricFor(id uint16) (uint8, bool) {
	idx, ok := r.fabrics[id]
	return idx, ok
}

// sortedSessionIDs is a deterministic dump of a session-id keyset
// used in error messages so diagnostics don't drift across runs.
func sortedSessionIDs(m map[uint16]*channel.Session) []uint16 {
	out := make([]uint16, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// run walks every step in order. Any per-step failure is reported via
// t.Errorf and the run continues so the test surfaces every problem
// from one scenario run rather than just the first.
func (h *scenarioHarness) run() {
	h.t.Helper()
	for i := range h.s.Steps {
		st := h.s.Steps[i] // local copy avoids 360-byte rangeValCopy on the range variable
		stepCtx := fmt.Sprintf("step[%d] %s.%s", i, st.Actor, st.Kind)
		switch st.Kind {
		case kindFireAttributeChange:
			h.fireAttributeChange(stepCtx, st)
		case kindFireViaEngine:
			h.fireViaEngine(stepCtx, st)
		case kindFireNotifierSource:
			h.fireNotifierSource(stepCtx, st)
		case kindExpectTX:
			h.expectTX(stepCtx, st)
		case kindExpectNoTX:
			h.expectNoTX(stepCtx, st)
		case kindSendStatusResponse:
			h.sendStatusResponse(stepCtx, st, false)
		case kindExpectLog:
			h.expectLog(stepCtx, st)
		case kindCloseSession:
			h.closeSession(stepCtx, st)
		case kindDropNextTX:
			h.dropNextTX(stepCtx, st)
		case kindTickRetransmit:
			h.tickRetransmit(stepCtx, st)
		case kindWait:
			h.wait(stepCtx, st)
		case kindSendWriteRequest:
			h.sendWriteRequest(stepCtx, st)
		case kindSendSubscribeRequest:
			h.sendSubscribeRequest(stepCtx, st)
		case kindSendReadRequest:
			h.sendReadRequest(stepCtx, st)
		case kindAssertGT:
			h.assertGT(stepCtx, st)
		case kindDrainSubscribeChunks:
			h.drainSubscribeChunks(stepCtx, st)
		case kindEngineTickAt:
			h.engineTickAt(stepCtx, st)
		case kindSendInvokeMoveToLevel:
			h.sendInvokeMoveToLevel(stepCtx, st)
		default:
			h.t.Errorf("%s: unhandled kind", stepCtx)
		}
	}
}

// fireAttributeChange simulates a CCU echo. Phase-A: directly invokes
// reportSubscription on the subscription's path so the wire-side
// invariants the scenario asserts (fresh exchange, Initiator=true)
// become observable. Phase B will wire the full dirty-mark → engine
// tick path via a fake cluster server's notifier.
func (h *scenarioHarness) fireAttributeChange(ctx string, st scenarioStep) {
	h.t.Helper()
	sub, _, spec := h.activeSub(st.SubscriptionIdx)
	p := spec.effectivePaths()[0]
	paths := []im.ConcreteAttributePath{
		{
			Endpoint:     p.Endpoint,
			Cluster:      p.Cluster,
			Attribute:    p.Attribute,
			HasEndpoint:  true,
			HasCluster:   true,
			HasAttribute: true,
		},
	}
	h.br.reportSubscription(context.Background(), sub, paths)
	_ = ctx
}

// fireViaEngine drives the bridge through the dirty-mark → engine
// tick → reportSubscription path that production uses for every CCU
// echo. The subscription manager was Started during setup with the
// bridge's real Reporter, so OnAttributeChanged enqueues a dirty
// mark and the next tick drains it.
//
// Caveat: the tick interval is bounded by subscription.MinIntervalFloor
// (set to 1 s in the harness Subscribe call). expectTX's 2 s peer
// ReadDeadline absorbs that latency.
func (h *scenarioHarness) fireViaEngine(ctx string, st scenarioStep) {
	h.t.Helper()
	_, _, spec := h.activeSub(st.SubscriptionIdx)
	p := spec.effectivePaths()[0]
	path := im.ConcreteAttributePath{
		Endpoint:     p.Endpoint,
		Cluster:      p.Cluster,
		Attribute:    p.Attribute,
		HasEndpoint:  true,
		HasCluster:   true,
		HasAttribute: true,
	}
	h.subMgr.OnAttributeChanged(path)
	_ = ctx
}

// fireNotifierSource drives the bridge through the full
// MatterChangeNotifier callback chain: looks up a fake source in
// the topology recipe and calls its fire(). The production
// wireMeasurementListeners callback registered during reassemble
// then dirty-marks the source's own cluster paths (the F2 filter
// path). The engine drains on the next tick and reports.
func (h *scenarioHarness) fireNotifierSource(ctx string, _ scenarioStep) {
	h.t.Helper()
	if h.topology == nil {
		h.t.Errorf("%s: scenario does not declare given.topology — fire_notifier_source needs a topology recipe", ctx)
		return
	}
	key := h.s.Given.FireSourceKey
	if key == "" && len(h.topology.sources) == 1 {
		for k := range h.topology.sources {
			key = k
		}
	}
	src, ok := h.topology.sources[key]
	if !ok {
		h.t.Errorf("%s: topology %q has no source keyed %q (have %v)", ctx, h.s.Given.Topology, key, sortedStringKeys(h.topology.sources))
		return
	}
	src.fire()
}

// expectTX captures the next outbound datagram on peerConn, decrypts
// it against peerSess, decodes the protocol header, and asserts the
// fields the step specifies. Bindings declared via bind_*_to enter
// the harness's variable table for later steps.
func (h *scenarioHarness) expectTX(ctx string, st scenarioStep) {
	h.t.Helper()
	_ = h.peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := h.peerConn.ReadFromUDP(buf)
	if err != nil {
		h.t.Errorf("%s: peer ReadFromUDP: %v", ctx, err)
		return
	}
	got := buf[:n]

	hdr, hdrLen, err := message.UnmarshalHeader(got)
	if err != nil {
		h.t.Errorf("%s: UnmarshalHeader: %v", ctx, err)
		return
	}
	peerSess, ok := h.peerSessionByID[hdr.SessionID]
	if !ok {
		h.t.Errorf("%s: no peer session for header.SessionID=%d (have %v)", ctx, hdr.SessionID, sortedSessionIDs(h.peerSessionByID))
		return
	}
	plain, _, err := peerSess.Decrypt(&hdr, securityFlagsByte(&hdr), got[hdrLen:])
	if err != nil {
		h.t.Errorf("%s: decrypt: %v", ctx, err)
		return
	}
	proto, protoLen, err := message.UnmarshalProtocolHeader(plain)
	if err != nil {
		h.t.Errorf("%s: UnmarshalProtocolHeader: %v", ctx, err)
		return
	}
	imBody := plain[protoLen:]

	switch st.Opcode {
	case "ReportData":
		if proto.Opcode != im.OpcodeReportData {
			h.t.Errorf("%s: proto.Opcode = %#x, want ReportData (%#x)", ctx, proto.Opcode, im.OpcodeReportData)
		}
	case "WriteResponse":
		if proto.Opcode != im.OpcodeWriteResponse {
			h.t.Errorf("%s: proto.Opcode = %#x, want WriteResponse (%#x)", ctx, proto.Opcode, im.OpcodeWriteResponse)
		}
	case "StatusResponse":
		if proto.Opcode != im.OpcodeStatusResponse {
			h.t.Errorf("%s: proto.Opcode = %#x, want StatusResponse (%#x)", ctx, proto.Opcode, im.OpcodeStatusResponse)
		}
	case "SubscribeResponse":
		if proto.Opcode != im.OpcodeSubscribeResponse {
			h.t.Errorf("%s: proto.Opcode = %#x, want SubscribeResponse (%#x)", ctx, proto.Opcode, im.OpcodeSubscribeResponse)
		}
	case "InvokeResponse":
		if proto.Opcode != im.OpcodeInvokeResponse {
			h.t.Errorf("%s: proto.Opcode = %#x, want InvokeResponse (%#x)", ctx, proto.Opcode, im.OpcodeInvokeResponse)
		}
	case "":
		// caller does not constrain opcode
	default:
		h.t.Errorf("%s: opcode %q not yet supported by harness", ctx, st.Opcode)
	}

	if st.Initiator != nil && proto.Initiator != *st.Initiator {
		h.t.Errorf("%s: proto.Initiator = %t, want %t", ctx, proto.Initiator, *st.Initiator)
	}

	if st.ExchangeIDFresh {
		if proto.ExchangeID == 0 {
			h.t.Errorf("%s: proto.ExchangeID = 0 (allocator must skip zero)", ctx)
		}
		if proto.ExchangeID > 0x7FFF {
			h.t.Errorf("%s: proto.ExchangeID = %d > 0x7FFF (allocator must mask to 15 bits)", ctx, proto.ExchangeID)
		}
	}

	if st.ExchangeIDNeqSubscribe {
		for _, spec := range h.s.Given.Subscriptions {
			if proto.ExchangeID == spec.PeerSubscribeExchangeID {
				h.t.Errorf("%s: proto.ExchangeID = %d equals subscription[session=%d] subscribe-exchange (F4 regression — ongoing report reused the peer-opened exchange)",
					ctx, proto.ExchangeID, spec.SessionID)
				break
			}
		}
	}

	if st.BindExchangeIDTo != "" {
		h.bindings[st.BindExchangeIDTo] = proto.ExchangeID
	}
	if st.BindCounterTo != "" {
		h.bindings[st.BindCounterTo] = hdr.MessageCounter
	}
	if st.BindAttributeValueTo != "" {
		v, err := tlvFirstAttributeUintValue(imBody)
		if err != nil {
			h.t.Errorf("%s: extract attribute value: %v", ctx, err)
		} else {
			h.bindings[st.BindAttributeValueTo] = v
		}
	}
	if st.ExpectAttributeValue != nil {
		got, err := tlvFirstAttributeUintValue(imBody)
		if err != nil {
			h.t.Errorf("%s: extract attribute value: %v", ctx, err)
		} else if got != *st.ExpectAttributeValue {
			h.t.Errorf("%s: attribute value = %d, want %d", ctx, got, *st.ExpectAttributeValue)
		}
	}
	if st.BindDataVersionTo != "" {
		v, err := tlvFirstAttributeDataVersion(imBody)
		if err != nil {
			h.t.Errorf("%s: extract DataVersion: %v", ctx, err)
		} else {
			h.bindings[st.BindDataVersionTo] = v
		}
	}
	if st.BindMaxIntervalTo != "" {
		v, err := tlvSubscribeResponseMaxInterval(imBody)
		if err != nil {
			h.t.Errorf("%s: extract MaxInterval: %v", ctx, err)
		} else {
			h.bindings[st.BindMaxIntervalTo] = v
		}
	}
	if st.ExpectInvokeStatus != nil {
		got, err := tlvInvokeResponseFirstStatus(imBody)
		if err != nil {
			h.t.Errorf("%s: extract InvokeResponse status: %v", ctx, err)
		} else if got != *st.ExpectInvokeStatus {
			h.t.Errorf("%s: InvokeResponse status = 0x%02X, want 0x%02X", ctx, got, *st.ExpectInvokeStatus)
		}
	}
	if st.ExpectMaxInterval != nil {
		got, err := tlvSubscribeResponseMaxInterval(imBody)
		if err != nil {
			h.t.Errorf("%s: extract MaxInterval: %v", ctx, err)
		} else if got != *st.ExpectMaxInterval {
			h.t.Errorf("%s: MaxInterval = %d, want %d", ctx, got, *st.ExpectMaxInterval)
		}
	}

	if st.AttributeReportsCount != nil {
		count, err := tlvAttributeReportsCount(imBody)
		if err != nil {
			h.t.Errorf("%s: count attribute_reports: %v", ctx, err)
		} else if count != *st.AttributeReportsCount {
			h.t.Errorf("%s: attribute_reports count = %d, want %d (F2 narrowing regression — notifier emitted reports for paths outside its own cluster)",
				ctx, count, *st.AttributeReportsCount)
		}
	}

	if len(st.TLVTagsPresent) > 0 || len(st.TLVTagsAbsent) > 0 {
		tags, err := tlvTopLevelContextTags(imBody)
		if err != nil {
			h.t.Errorf("%s: decode TLV body: %v", ctx, err)
			return
		}
		for _, want := range st.TLVTagsPresent {
			if !tags[want] {
				h.t.Errorf("%s: tag %d absent at TLV top level; got %v", ctx, want, sortedTagKeys(tags))
			}
		}
		for _, forbidden := range st.TLVTagsAbsent {
			if tags[forbidden] {
				h.t.Errorf("%s: tag %d present at TLV top level (forbidden); got %v", ctx, forbidden, sortedTagKeys(tags))
			}
		}
	}
}

// sendStatusResponse ships an IM:StatusResponse from the peer to the
// bridge on the specified exchange — the action Apple Home performs
// after consuming a ReportData. The bridge's receive pipeline then
// fires signalStatusResponseRX(exchange) and emits the
// matter.rx.im.status_ack debug log, which is the observable signal
// downstream steps assert.
func (h *scenarioHarness) sendStatusResponse(ctx string, st scenarioStep, peerOpenedExchange bool) {
	h.t.Helper()
	exch, ok := h.resolveExchange(st.Exchange)
	if !ok {
		h.t.Errorf("%s: cannot resolve exchange %q", ctx, st.Exchange)
		return
	}
	status, ok := scenarioStatusFromName(st.Status)
	if !ok {
		h.t.Errorf("%s: unknown status %q", ctx, st.Status)
		return
	}

	body, err := EncodeStatusResponse(im.StatusResponse{Status: status})
	if err != nil {
		h.t.Errorf("%s: EncodeStatusResponse: %v", ctx, err)
		return
	}
	proto := message.ProtocolHeader{
		// Matter §4.4.3.1: the I flag says whether the SENDER opened the
		// exchange, and it stays set on every message that side sends.
		// On a bridge-opened exchange (an ongoing report) the peer answers
		// with I=0; on a peer-opened one (its own SubscribeRequest, whose
		// initial chunks it acks) the peer keeps I=1.
		Initiator:  peerOpenedExchange,
		Opcode:     im.OpcodeStatusResponse,
		ExchangeID: exch,
		ProtocolID: im.InteractionModelProtocolID,
	}
	payload := append(proto.Marshal(), body...) //nolint:gocritic

	_, peerSess, spec := h.activeSub(st.SubscriptionIdx)
	hdr := message.Header{
		SessionID:  spec.SessionID,
		DestSize:   message.DestNodeID,
		DestNodeID: h.bridgeNodeID,
	}
	enc, err := peerSess.Encrypt(&hdr, securityFlagsByte(&hdr), payload)
	if err != nil {
		h.t.Errorf("%s: peer.Encrypt: %v", ctx, err)
		return
	}
	datagram := append(hdr.Marshal(), enc.Ciphertext...)
	if _, err := h.peerConn.WriteToUDP(datagram, h.bridgeIn); err != nil {
		h.t.Errorf("%s: WriteToUDP: %v", ctx, err)
		return
	}
}

// expectLog scans the captured log records for one matching the step's
// msg / match_exchange. Polls briefly to absorb the receive pipeline's
// asynchronous dispatch — without that the assertion fires before the
// bridge has processed the inbound StatusResponse and times out.
func (h *scenarioHarness) expectLog(ctx string, st scenarioStep) {
	h.t.Helper()
	wantExch, exchBound := uint16(0), false
	if st.MatchExchange != "" {
		v, ok := h.resolveExchange(st.MatchExchange)
		if !ok {
			h.t.Errorf("%s: cannot resolve exchange %q", ctx, st.MatchExchange)
			return
		}
		wantExch, exchBound = v, true
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, rec := range h.logCapture.snapshot() {
			if rec.message != st.Msg {
				continue
			}
			if exchBound {
				e, ok := rec.uint16("exchange")
				if !ok || e != wantExch {
					continue
				}
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Errorf("%s: timed out waiting for log msg=%q match_exchange=%q; captured: %s",
		ctx, st.Msg, st.MatchExchange, h.logCapture.dump())
}

// expectNoTX asserts the bridge ships NO outbound datagram for the
// given quiet window (default 500 ms). Used after fault-injection
// steps such as close_session, where any outbound on a closed
// subscription's exchange is a regression.
func (h *scenarioHarness) expectNoTX(ctx string, st scenarioStep) {
	h.t.Helper()
	window := time.Duration(st.TimeoutMillis) * time.Millisecond
	if window == 0 {
		window = 500 * time.Millisecond
	}
	_ = h.peerConn.SetReadDeadline(time.Now().Add(window))
	buf := make([]byte, 1500)
	n, _, err := h.peerConn.ReadFromUDP(buf)
	if err != nil {
		// Read timeout = no traffic = pass.
		return
	}
	h.t.Errorf("%s: peer received %d B during the quiet window — bridge sent when it should not have. hex: %x",
		ctx, n, buf[:n])
}

// closeSession drives the F1 cascade: subMgr.CloseSession evicts
// every subscription on the session, mirroring the daemon's
// opMgr.SetOnSessionClose(subMgr.CloseSession) hook fired on CASE
// teardown. Any subsequent fire_via_engine on the same path is then
// expected to be a no-op (no dirty mark fires because the
// subscription is gone) — the scenario asserts that via expect_no_tx.
func (h *scenarioHarness) closeSession(ctx string, st scenarioStep) {
	h.t.Helper()
	_, _, spec := h.activeSub(st.SubscriptionIdx)
	h.subMgr.CloseSession(spec.SessionID)
	_ = ctx
}

// dropNextTX reads the next outbound datagram from the peer socket
// without acknowledging it. The bridge's MRP retransmit pump then
// re-ships the same datagram after the §4.12.6 backoff. The next
// expect_tx step asserts the retransmit lands.
func (h *scenarioHarness) dropNextTX(ctx string, _ scenarioStep) {
	h.t.Helper()
	_ = h.peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	if _, _, err := h.peerConn.ReadFromUDP(buf); err != nil {
		h.t.Errorf("%s: peer ReadFromUDP (intended-drop): %v", ctx, err)
	}
}

// engineTickAt drives the subscription manager's tick loop with a
// controlled wall-clock value (t0 + at_millis). Combined with
// engine_manual_only on a scenarioSubSpec, this lets cadence
// scenarios advance the engine deterministically — no wall-clock
// sleeps needed.
func (h *scenarioHarness) engineTickAt(ctx string, st scenarioStep) {
	h.t.Helper()
	now := h.t0.Add(time.Duration(st.AtMillis) * time.Millisecond)
	h.subMgr.Tick(context.Background(), now)
	_ = ctx
}

// drainSubscribeChunks reads ReportData chunks from the peer
// socket, ACKs each with a StatusResponse on the same exchange,
// and stops once the bridge ships a SubscribeResponse. Locks the
// F5 (per-chunk handshake) and F6 (MRP piggyback-ack) regressions
// for multi-chunk initial-burst payloads — without those the
// negotiation deadlocks between chunk N and chunk N+1 because
// Apple's MTRDevice stays in the per-chunk read state until each
// StatusResponse round-trips.
//
// Binds the SubscribeResponse's exchange ID into the step's
// bind_exchange_id_to slot. The intermediate ReportData chunks all
// share the peer-opened Subscribe exchange (see Phase J).
func (h *scenarioHarness) drainSubscribeChunks(ctx string, st scenarioStep) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	chunks := 0
	for time.Now().Before(deadline) {
		_ = h.peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 1500)
		n, _, err := h.peerConn.ReadFromUDP(buf)
		if err != nil {
			h.t.Errorf("%s: peer ReadFromUDP (after %d chunks): %v", ctx, chunks, err)
			return
		}
		got := buf[:n]
		hdr, hdrLen, err := message.UnmarshalHeader(got)
		if err != nil {
			h.t.Errorf("%s: UnmarshalHeader: %v", ctx, err)
			return
		}
		peerSess, ok := h.peerSessionByID[hdr.SessionID]
		if !ok {
			h.t.Errorf("%s: no peer session for SessionID=%d", ctx, hdr.SessionID)
			return
		}
		plain, _, err := peerSess.Decrypt(&hdr, securityFlagsByte(&hdr), got[hdrLen:])
		if err != nil {
			h.t.Errorf("%s: decrypt: %v", ctx, err)
			return
		}
		proto, _, err := message.UnmarshalProtocolHeader(plain)
		if err != nil {
			h.t.Errorf("%s: UnmarshalProtocolHeader: %v", ctx, err)
			return
		}
		switch proto.Opcode {
		case im.OpcodeReportData:
			chunks++
			// Ack the chunk; the bridge needs the StatusResponse to
			// release the next chunk's send (matter.js
			// InteractionMessenger.ts sendDataReportMessage(_, waitForAck=true)).
			ackStep := scenarioStep{
				Status:          "Success",
				SubscriptionIdx: st.SubscriptionIdx,
			}
			// Inline the exchange resolution since we already have
			// proto.ExchangeID — no $var indirection needed.
			h.bindings["$__drain_exch"] = proto.ExchangeID
			ackStep.Exchange = "$__drain_exch"
			// The chunks ride the exchange the peer opened with its
			// SubscribeRequest, so its ack keeps I=1.
			h.sendStatusResponse(ctx, ackStep, true)
		case im.OpcodeSubscribeResponse:
			if st.BindExchangeIDTo != "" {
				h.bindings[st.BindExchangeIDTo] = proto.ExchangeID
			}
			if st.MinChunks > 0 && chunks < st.MinChunks {
				h.t.Errorf("%s: drained %d chunks before SubscribeResponse, want >= %d (multi-chunk regression)",
					ctx, chunks, st.MinChunks)
			}
			return
		default:
			h.t.Errorf("%s: unexpected opcode 0x%02X (want ReportData or SubscribeResponse)", ctx, proto.Opcode)
			return
		}
	}
	h.t.Errorf("%s: drained %d chunks but never saw SubscribeResponse within deadline", ctx, chunks)
}

// assertGT is a cross-step assertion: bindings[gt.LHS] >
// bindings[gt.RHS] as uint32. The two binding values typically come
// from bind_data_version_to or bind_counter_to in earlier
// expect_tx steps. Used by DataVersion-monotonicity scenarios.
func (h *scenarioHarness) assertGT(ctx string, st scenarioStep) {
	h.t.Helper()
	if st.GT == nil {
		h.t.Errorf("%s: assert_gt requires `gt: {lhs, rhs}` body", ctx)
		return
	}
	lhs, ok1 := h.bindingAsUint32(st.GT.LHS)
	rhs, ok2 := h.bindingAsUint32(st.GT.RHS)
	if !ok1 || !ok2 {
		h.t.Errorf("%s: bindings missing or non-numeric: %s=%v %s=%v",
			ctx, st.GT.LHS, h.bindings[st.GT.LHS], st.GT.RHS, h.bindings[st.GT.RHS])
		return
	}
	if lhs <= rhs {
		h.t.Errorf("%s: assert_gt failed: %s=%d > %s=%d expected, got %d > %d false",
			ctx, st.GT.LHS, lhs, st.GT.RHS, rhs, lhs, rhs)
	}
}

func (h *scenarioHarness) bindingAsUint32(name string) (uint32, bool) {
	v, ok := h.bindings[name]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case uint32:
		return t, true
	case uint16:
		return uint32(t), true
	case uint8:
		return uint32(t), true
	case int:
		if t < 0 {
			return 0, false
		}
		return uint32(t), true //nolint:gosec // G115: t >= 0 guarded by check above
	}
	return 0, false
}

// sendInvokeMoveToLevel ships an IM:InvokeRequestMessage carrying
// a LevelControl.MoveToLevel command (cluster 0x0008, command 0x00)
// from the peer. The command-fields struct contains tag 0 Level
// (uint8) only. Locks the commandFieldsReader → MatterInvoke
// contract for non-commissioning clusters that carry field payloads.
func (h *scenarioHarness) sendInvokeMoveToLevel(ctx string, st scenarioStep) {
	h.t.Helper()
	_, peerSess, spec := h.activeSub(st.SubscriptionIdx)
	level, ok := st.Value.(float64) // JSON numbers decode to float64
	if !ok {
		intLevel, intOK := st.Value.(int)
		if !intOK {
			h.t.Errorf("%s: send_invoke_move_to_level requires numeric `value` (got %T)", ctx, st.Value)
			return
		}
		level = float64(intLevel)
	}
	if level < 0 || level > 255 {
		h.t.Errorf("%s: level %v out of uint8 range", ctx, level)
		return
	}
	target := spec.effectivePaths()[0]
	body, err := encodeScenarioInvokeMoveToLevel(target.Endpoint, uint8(level))
	if err != nil {
		h.t.Errorf("%s: encode InvokeRequest: %v", ctx, err)
		return
	}
	exch := st.PeerExchangeID
	if exch == 0 {
		exch = 0x6000 + uint16(len(h.bindings)%0x1FFF) //nolint:gosec // G115: modulo ensures value fits uint16
	}
	proto := message.ProtocolHeader{
		Initiator:  true,
		Opcode:     im.OpcodeInvokeRequest,
		ExchangeID: exch,
		ProtocolID: im.InteractionModelProtocolID,
		NeedsAck:   true,
	}
	payload := append(proto.Marshal(), body...) //nolint:gocritic

	hdr := message.Header{
		SessionID:  spec.SessionID,
		DestSize:   message.DestNodeID,
		DestNodeID: h.bridgeNodeID,
	}
	encMsg, err := peerSess.Encrypt(&hdr, securityFlagsByte(&hdr), payload)
	if err != nil {
		h.t.Errorf("%s: peer.Encrypt: %v", ctx, err)
		return
	}
	datagram := append(hdr.Marshal(), encMsg.Ciphertext...)
	if _, err := h.peerConn.WriteToUDP(datagram, h.bridgeIn); err != nil {
		h.t.Errorf("%s: WriteToUDP: %v", ctx, err)
		return
	}
}

// sendReadRequest ships an IM:ReadRequestMessage from the peer to
// the bridge — drives the Read code path. The bridge resolves the
// requested paths through the dispatcher (with FabricFiltered
// scoping when the request flag is set, Matter §10.6.3), assembles
// the ReportData, and ships it back on the same exchange. The
// scenario steps after this one assert the ReportData via expect_tx
// with `opcode: "ReportData"`.
func (h *scenarioHarness) sendReadRequest(ctx string, st scenarioStep) {
	h.t.Helper()
	_, peerSess, spec := h.activeSub(st.SubscriptionIdx)

	paths := make([]im.ConcreteAttributePath, 0, len(spec.effectivePaths()))
	for _, p := range spec.effectivePaths() {
		paths = append(paths, im.ConcreteAttributePath{
			Endpoint:     p.Endpoint,
			Cluster:      p.Cluster,
			Attribute:    p.Attribute,
			HasEndpoint:  true,
			HasCluster:   true,
			HasAttribute: true,
		})
	}
	req := im.ReadRequest{
		AttributeRequests: paths,
		FabricFiltered:    st.FabricFiltered,
	}
	enc := tlv.NewEncoder()
	req.MarshalTLV(enc)
	body, err := enc.Bytes()
	if err != nil {
		h.t.Errorf("%s: encode ReadRequest: %v", ctx, err)
		return
	}

	exch := st.PeerExchangeID
	if exch == 0 {
		exch = 0x5000 + uint16(len(h.bindings)%0x2FFF) //nolint:gosec // G115: modulo ensures value fits uint16
	}
	proto := message.ProtocolHeader{
		Initiator:  true,
		Opcode:     im.OpcodeReadRequest,
		ExchangeID: exch,
		ProtocolID: im.InteractionModelProtocolID,
		NeedsAck:   true,
	}
	payload := append(proto.Marshal(), body...) //nolint:gocritic

	hdr := message.Header{
		SessionID:  spec.SessionID,
		DestSize:   message.DestNodeID,
		DestNodeID: h.bridgeNodeID,
	}
	encMsg, err := peerSess.Encrypt(&hdr, securityFlagsByte(&hdr), payload)
	if err != nil {
		h.t.Errorf("%s: peer.Encrypt: %v", ctx, err)
		return
	}
	datagram := append(hdr.Marshal(), encMsg.Ciphertext...)
	if _, err := h.peerConn.WriteToUDP(datagram, h.bridgeIn); err != nil {
		h.t.Errorf("%s: WriteToUDP: %v", ctx, err)
		return
	}
}

// sendSubscribeRequest ships an IM:SubscribeRequestMessage from the
// peer to the bridge — drives the full Subscribe-negotiation
// pipeline. The bridge replies with the initial ReportData stream
// (one or more chunks, each acked via per-chunk StatusResponse),
// then a SubscribeResponse echoing the negotiated subscriptionId
// and MaxInterval. The scenario steps after this one assert the
// reply chain via expect_tx / send_status_response.
func (h *scenarioHarness) sendSubscribeRequest(ctx string, st scenarioStep) {
	h.t.Helper()
	_, peerSess, spec := h.activeSub(st.SubscriptionIdx)

	// Build attribute paths from the spec's effective paths, or
	// substitute one all-wildcard path when the step opts in.
	var paths []im.ConcreteAttributePath
	if st.Wildcard {
		paths = []im.ConcreteAttributePath{{}}
	} else {
		paths = make([]im.ConcreteAttributePath, 0, len(spec.effectivePaths()))
		for _, p := range spec.effectivePaths() {
			paths = append(paths, im.ConcreteAttributePath{
				Endpoint:     p.Endpoint,
				Cluster:      p.Cluster,
				Attribute:    p.Attribute,
				HasEndpoint:  true,
				HasCluster:   true,
				HasAttribute: true,
			})
		}
	}
	req := im.SubscribeRequest{
		KeepSubscriptions:  true,
		MinIntervalFloor:   1,
		MaxIntervalCeiling: 60,
		AttributeRequests:  paths,
	}
	enc := tlv.NewEncoder()
	req.MarshalTLV(enc)
	body, err := enc.Bytes()
	if err != nil {
		h.t.Errorf("%s: encode SubscribeRequest: %v", ctx, err)
		return
	}

	exch := spec.PeerSubscribeExchangeID
	proto := message.ProtocolHeader{
		Initiator:  true,
		Opcode:     im.OpcodeSubscribeRequest,
		ExchangeID: exch,
		ProtocolID: im.InteractionModelProtocolID,
		NeedsAck:   true,
	}
	payload := append(proto.Marshal(), body...) //nolint:gocritic

	hdr := message.Header{
		SessionID:  spec.SessionID,
		DestSize:   message.DestNodeID,
		DestNodeID: h.bridgeNodeID,
	}
	encMsg, err := peerSess.Encrypt(&hdr, securityFlagsByte(&hdr), payload)
	if err != nil {
		h.t.Errorf("%s: peer.Encrypt: %v", ctx, err)
		return
	}
	datagram := append(hdr.Marshal(), encMsg.Ciphertext...)
	if _, err := h.peerConn.WriteToUDP(datagram, h.bridgeIn); err != nil {
		h.t.Errorf("%s: WriteToUDP: %v", ctx, err)
		return
	}
}

// sendWriteRequest ships an IM:WriteRequestMessage from the peer
// to the bridge — drives the Apple-write code path. The bridge's
// dispatcher resolves the path, calls MatterWrite on the matching
// cluster server (or surfaces a Status when the path isn't
// resolvable), and replies with a WriteResponse on the same
// exchange. Use the standard expect_tx with `opcode: "WriteResponse"`
// to assert the reply shape.
func (h *scenarioHarness) sendWriteRequest(ctx string, st scenarioStep) {
	h.t.Helper()
	_, peerSess, spec := h.activeSub(st.SubscriptionIdx)

	target := spec.effectivePaths()[0]
	if st.WritePath != nil {
		target = *st.WritePath
	}
	value, ok := st.Value.(bool)
	if !ok {
		h.t.Errorf("%s: send_write_request requires a bool `value` (got %T)", ctx, st.Value)
		return
	}
	exch := st.PeerExchangeID
	if exch == 0 {
		exch = 0x4000 + uint16(len(h.bindings)%0x3FFF) //nolint:gosec // G115: modulo ensures value fits uint16
	}

	body, err := encodeScenarioWriteRequest(im.ConcreteAttributePath{
		Endpoint:     target.Endpoint,
		Cluster:      target.Cluster,
		Attribute:    target.Attribute,
		HasEndpoint:  true,
		HasCluster:   true,
		HasAttribute: true,
	}, value)
	if err != nil {
		h.t.Errorf("%s: encode WriteRequest: %v", ctx, err)
		return
	}
	proto := message.ProtocolHeader{
		Initiator:  true, // peer opened this exchange
		Opcode:     im.OpcodeWriteRequest,
		ExchangeID: exch,
		ProtocolID: im.InteractionModelProtocolID,
		NeedsAck:   true,
	}
	payload := append(proto.Marshal(), body...) //nolint:gocritic

	hdr := message.Header{
		SessionID:  spec.SessionID,
		DestSize:   message.DestNodeID,
		DestNodeID: h.bridgeNodeID,
	}
	enc, err := peerSess.Encrypt(&hdr, securityFlagsByte(&hdr), payload)
	if err != nil {
		h.t.Errorf("%s: peer.Encrypt: %v", ctx, err)
		return
	}
	datagram := append(hdr.Marshal(), enc.Ciphertext...)
	if _, err := h.peerConn.WriteToUDP(datagram, h.bridgeIn); err != nil {
		h.t.Errorf("%s: WriteToUDP: %v", ctx, err)
		return
	}
}

// tickRetransmit drives one Tick of the bridge's outboundReliable
// tracker so any datagram whose MRP backoff has elapsed gets re-shipped
// synchronously. The harness does not run the bridge's ack-pump
// goroutine (it remains dormant when AttachAckTracker is called after
// Start), so scenarios that need to observe retransmits invoke this
// step explicitly. now is advanced beyond MRPBackoffBase so a single
// entry in the tracker will fire immediately.
func (h *scenarioHarness) tickRetransmit(ctx string, _ scenarioStep) {
	h.t.Helper()
	h.br.mu.RLock()
	tracker := h.br.outboundReliable
	h.br.mu.RUnlock()
	if tracker == nil {
		h.t.Errorf("%s: outboundReliable not engaged — AttachAckTracker missing in setup", ctx)
		return
	}
	h.br.tickOutboundReliable(tracker, time.Now().Add(2*time.Second))
}

// wait pauses scenario execution for TimeoutMillis (default 500 ms).
// Used to span MRP retransmit backoffs or engine-tick boundaries
// where the scenario needs deterministic timing without a tighter
// observable signal.
func (h *scenarioHarness) wait(ctx string, st scenarioStep) {
	h.t.Helper()
	d := time.Duration(st.TimeoutMillis) * time.Millisecond
	if d == 0 {
		d = 500 * time.Millisecond
	}
	time.Sleep(d)
	_ = ctx
}

func (h *scenarioHarness) resolveExchange(ref string) (uint16, bool) {
	if ref == "" {
		return 0, false
	}
	if ref[0] == '$' {
		v, ok := h.bindings[ref]
		if !ok {
			return 0, false
		}
		if e, ok := v.(uint16); ok {
			return e, true
		}
		return 0, false
	}
	var u uint16
	if err := json.Unmarshal([]byte(ref), &u); err != nil {
		return 0, false
	}
	return u, true
}

// scenarioStatusFromName maps the scenario file's symbolic status
// name to a Matter status code.
func scenarioStatusFromName(name string) (im.StatusCode, bool) {
	switch name {
	case "Success":
		return im.StatusSuccess, true
	case "Failure":
		return im.StatusFailure, true
	case "":
		return im.StatusSuccess, true
	default:
		return 0, false
	}
}

// scenarioLogRecord captures one slog record's queryable fields.
type scenarioLogRecord struct {
	level   slog.Level
	message string
	attrs   map[string]any
}

func (r scenarioLogRecord) uint16(key string) (uint16, bool) {
	v, ok := r.attrs[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case uint16:
		return t, true
	case int64:
		if t < 0 || t > 0xFFFF {
			return 0, false
		}
		return uint16(t), true
	case int:
		if t < 0 || t > 0xFFFF {
			return 0, false
		}
		return uint16(t), true
	}
	return 0, false
}

// scenarioLogCapture is an in-memory slog.Handler the harness uses to
// inspect Bridge log output. Concurrent-safe: the engine, receive
// pipeline, and main test goroutine all emit records simultaneously.
type scenarioLogCapture struct {
	mu      sync.Mutex
	records []scenarioLogRecord
}

func newScenarioLogCapture() *scenarioLogCapture { return &scenarioLogCapture{} }

func (c *scenarioLogCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *scenarioLogCapture) Handle(_ context.Context, r slog.Record) error {
	rec := scenarioLogRecord{level: r.Level, message: r.Message, attrs: map[string]any{}}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value.Any()
		return true
	})
	c.mu.Lock()
	c.records = append(c.records, rec)
	c.mu.Unlock()
	return nil
}

func (c *scenarioLogCapture) WithAttrs(attrs []slog.Attr) slog.Handler { _ = attrs; return c }
func (c *scenarioLogCapture) WithGroup(name string) slog.Handler       { _ = name; return c }

func (c *scenarioLogCapture) snapshot() []scenarioLogRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]scenarioLogRecord, len(c.records))
	copy(out, c.records)
	return out
}

func (c *scenarioLogCapture) dump() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b bytes.Buffer
	for _, r := range c.records {
		fmt.Fprintf(&b, "\n  [%s] %s %v", r.level, r.message, r.attrs)
	}
	return b.String()
}
