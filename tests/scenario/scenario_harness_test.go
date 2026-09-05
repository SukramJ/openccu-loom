// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package scenario

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

	"github.com/SukramJ/go-fabric/bridge"
	matterendpoint "github.com/SukramJ/go-fabric/endpoint"
	"github.com/SukramJ/go-fabric/im"
	"github.com/SukramJ/go-fabric/im/subscription"
	"github.com/SukramJ/go-fabric/mdns"
	"github.com/SukramJ/go-fabric/secure/channel"
	"github.com/SukramJ/go-fabric/tlv"
	"github.com/SukramJ/go-fabric/transport/message"
	"github.com/SukramJ/go-fabric/transport/mrp"
)

// scenarioHarness drives a single scenario file end-to-end against a
// real Bridge + real CASE session pair on UDP loopback. The bridge is
// driven exclusively through go-fabric's published API: everything the
// harness needs to observe or provoke either travels over the wire or
// goes through an exported method, which makes the corpus an exercise
// of that API as much as of the bridge behind it.
type scenarioHarness struct {
	t  *testing.T
	s  *scenario
	br *bridge.Bridge

	peerConn *net.UDPConn
	peerAddr *net.UDPAddr
	bridgeIn *net.UDPAddr

	bridgeNodeID uint64
	peerNodeID   uint64

	subMgr *subscription.Manager

	// Per-subscription state, indexed in scenario order. Each
	// subscription has its own (bridgeSess, peerSess) pair and its
	// own *subscription.Subscription. peerSessionByID is the
	// reverse-lookup for inbound peer datagrams (the harness sees
	// only the session id in the header).
	subs            []*subscription.Subscription
	peerSessions    []*channel.Session
	peerSessionByID map[uint16]*channel.Session

	topology *scenarioTopology

	// t0 anchors deterministic engine ticks; captured immediately
	// before the subscriptions are established, so engine_tick_at
	// offsets relate to subscription admission time.
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

// scenarioReadDeadline bounds every blocking read on the peer socket.
// Long enough to absorb the engine's MinIntervalFloor gate (1 s in the
// harness default) plus one MRP backoff.
const scenarioReadDeadline = 2 * time.Second

// newScenarioHarness fully wires a harness for s:
//   - Bridge built with a captured logger so log-record assertions work.
//   - CASE session pair per subscription, injected via AttachSessionLookup.
//   - Real subscription.Manager attached, wired to the bridge's own
//     Reporter, so the engine ships through the production path.
//   - Every subscription the scenario declares (unless it opts out with
//     skip_auto_subscribe) established by an actual SubscribeRequest
//     from the peer socket. The bridge only learns where to ship a
//     subscription's reports from that request, so establishing them
//     any other way would leave the engine with nowhere to send.
func newScenarioHarness(t *testing.T, s *scenario) *scenarioHarness {
	t.Helper()

	logCap := newScenarioLogCapture()
	logger := slog.New(logCap)

	topology, err := resolveTopology(s.Given.Topology)
	if err != nil {
		t.Fatalf("scenario: topology: %v", err)
	}
	// Each scenario topology carries its own assembler and store; the
	// empty fixture gets one built here so both halves still share a
	// single endpoint-id space, exactly as the daemon wires them.
	var (
		snapshotter bridge.Snapshotter
		epStore     *scenarioStore
	)
	if topology != nil {
		snapshotter, epStore = topology.snapshotter, topology.store
	} else {
		snapshotter, epStore, err = newScenarioSnapshotter(nil)
		if err != nil {
			t.Fatalf("scenario: empty topology: %v", err)
		}
	}

	br, err := bridge.New(
		epStore,
		snapshotter,
		mdns.NewNoop(),
		bridge.Config{
			Listen:    "127.0.0.1:0",
			VendorID:  0x1234,
			ProductID: 0x5678,
			NodeLabel: "scenario-harness",
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
	br.AttachACLLister(matterendpoint.UnenforcedACL{})

	// Engage MRP before Start, which is the order the daemon uses and the
	// only order that spawns the bridge's ack pump. The pump both answers
	// the peer's owed acks and retransmits un-acked reliable output, so
	// the retransmit scenario needs it running: go-fabric exports no
	// single-shot tick for the outbound half.
	br.AttachAckTracker(mrp.NewAckTracker(50 * time.Millisecond))

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

	bridgeIn, err := net.ResolveUDPAddr("udp", br.LocalAddr())
	if err != nil {
		t.Fatalf("scenario: resolve bridge addr %q: %v", br.LocalAddr(), err)
	}

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
	peerSessions := make([]*channel.Session, len(s.Given.Subscriptions))

	for i, spec := range s.Given.Subscriptions {
		bridgeKey := bytes.Repeat([]byte{0xA0 | byte(i)}, 16)
		peerKey := bytes.Repeat([]byte{0xB0 | byte(i)}, 16)
		bSess, err := channel.New(channel.Config{
			EncryptKey:     bridgeKey,
			DecryptKey:     peerKey,
			LocalNodeID:    scenarioBridgeNodeID,
			PeerNodeID:     scenarioPeerNodeID,
			InitialCounter: 100 + uint32(i)*1000, //nolint:gosec // G115: i is bounded by the subscription count
		})
		if err != nil {
			t.Fatalf("scenario: bridge session[%d]: %v", i, err)
		}
		pSess, err := channel.New(channel.Config{
			EncryptKey:     peerKey,
			DecryptKey:     bridgeKey,
			LocalNodeID:    scenarioPeerNodeID,
			PeerNodeID:     scenarioBridgeNodeID,
			InitialCounter: 200 + uint32(i)*1000, //nolint:gosec // G115: i is bounded by the subscription count
		})
		if err != nil {
			t.Fatalf("scenario: peer session[%d]: %v", i, err)
		}
		if _, dupe := bridgeSessionByID[spec.SessionID]; dupe {
			t.Fatalf("scenario: subscriptions[%d]: session_id %d already used by an earlier subscription", i, spec.SessionID)
		}
		bridgeSessionByID[spec.SessionID] = bSess
		peerSessionByID[spec.SessionID] = pSess
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
	// the production ship path.
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

	h := &scenarioHarness{
		t:               t,
		s:               s,
		br:              br,
		peerConn:        peerConn,
		peerAddr:        peerAddr,
		bridgeIn:        bridgeIn,
		bridgeNodeID:    scenarioBridgeNodeID,
		peerNodeID:      scenarioPeerNodeID,
		subMgr:          subMgr,
		subs:            make([]*subscription.Subscription, len(s.Given.Subscriptions)),
		peerSessions:    peerSessions,
		peerSessionByID: peerSessionByID,
		topology:        topology,
		bindings:        make(map[string]any),
		logCapture:      logCap,
	}

	h.t0 = time.Now()
	for i, spec := range s.Given.Subscriptions {
		if spec.SkipAutoSubscribe {
			continue
		}
		h.establishSubscription(i, spec)
	}
	return h
}

// establishSubscription drives one SubscribeRequest from the peer and
// consumes the reply chain (initial ReportData chunks, each acked,
// then the SubscribeResponse). This is the only route by which the
// bridge learns a subscription's reply target, so it is also what makes
// every later engine tick shippable.
func (h *scenarioHarness) establishSubscription(idx int, spec scenarioSubSpec) {
	h.t.Helper()

	minFloor := spec.MinIntervalFloorSeconds
	if minFloor == 0 && spec.MaxIntervalCeilingSeconds == 0 {
		minFloor = 1
	}
	maxCeil := spec.MaxIntervalCeilingSeconds
	if maxCeil == 0 {
		maxCeil = 60
	}
	req := im.SubscribeRequest{
		KeepSubscriptions:  true,
		MinIntervalFloor:   minFloor,
		MaxIntervalCeiling: maxCeil,
		AttributeRequests:  h.pathsOf(spec, false),
	}
	enc := tlv.NewEncoder()
	req.MarshalTLV(enc)
	body, err := enc.Bytes()
	if err != nil {
		h.t.Fatalf("scenario: encode setup SubscribeRequest[%d]: %v", idx, err)
	}
	if !h.sendFromPeer(spec, im.OpcodeSubscribeRequest, spec.PeerSubscribeExchangeID, body, true, true) {
		return
	}

	deadline := time.Now().Add(5 * time.Second)
	chunks := 0
	for time.Now().Before(deadline) {
		hdr, proto, imBody, ok := h.readOutbound(fmt.Sprintf("setup subscribe[%d]", idx))
		if !ok {
			return
		}
		h.ackIfNeeded(spec, hdr, proto)
		switch proto.Opcode {
		case im.OpcodeReportData:
			chunks++
			h.sendStatusResponseOn(spec, proto.ExchangeID, im.StatusSuccess, true)
		case im.OpcodeSubscribeResponse:
			subID, err := tlvSubscribeResponseSubscriptionID(imBody)
			if err != nil {
				h.t.Errorf("scenario: setup subscribe[%d]: read SubscriptionID: %v", idx, err)
				return
			}
			sub, err := h.subMgr.Get(subID)
			if err != nil {
				h.t.Errorf("scenario: setup subscribe[%d]: manager has no subscription %d: %v", idx, subID, err)
				return
			}
			h.subs[idx] = sub
			return
		case im.OpcodeStatusResponse:
			h.t.Errorf("scenario: setup subscribe[%d]: bridge rejected the subscription with a StatusResponse after %d chunk(s); captured log: %s",
				idx, chunks, h.logCapture.dump())
			return
		default:
			h.t.Errorf("scenario: setup subscribe[%d]: unexpected opcode 0x%02X", idx, proto.Opcode)
			return
		}
	}
	h.t.Errorf("scenario: setup subscribe[%d]: no SubscribeResponse after %d chunk(s)", idx, chunks)
}

// pathsOf converts a spec's attribute paths into the concrete form the
// IM codec takes. wildcard replaces the set with a single all-wildcard
// path so the dispatcher's expansion branch is driven instead.
func (h *scenarioHarness) pathsOf(spec scenarioSubSpec, wildcard bool) []im.ConcreteAttributePath {
	if wildcard {
		return []im.ConcreteAttributePath{{}}
	}
	eff := spec.effectivePaths()
	paths := make([]im.ConcreteAttributePath, 0, len(eff))
	for _, p := range eff {
		paths = append(paths, im.ConcreteAttributePath{
			Endpoint:     p.Endpoint,
			Cluster:      p.Cluster,
			Attribute:    p.Attribute,
			HasEndpoint:  true,
			HasCluster:   true,
			HasAttribute: true,
		})
	}
	return paths
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
// flow through the bridge's session-fabric path and stamp the
// FabricFiltered dispatch context.
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

// securityFlagsByte reconstructs the Security-Flags byte associated
// with hdr from the typed fields the message package exposes. The
// encrypt / decrypt pair takes it as additional authenticated data and
// go-fabric derives it internally from the same fields, so a
// divergence here shows up as an authentication failure rather than as
// a wrong assertion.
func securityFlagsByte(hdr *message.Header) uint8 {
	var b uint8
	b |= uint8(hdr.SessionType&0xFF) & 0x1F
	if hdr.Privacy {
		b |= 0x80
	}
	if hdr.Control {
		b |= 0x40
	}
	if hdr.HasExtension {
		b |= 0x20
	}
	return b
}

// run walks every step in order. Any per-step failure is reported via
// t.Errorf and the run continues so the test surfaces every problem
// from one scenario run rather than just the first.
func (h *scenarioHarness) run() {
	h.t.Helper()
	for i := range h.s.Steps {
		st := h.s.Steps[i] // local copy avoids a large rangeValCopy on the range variable
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
			h.sendStatusResponse(stepCtx, st)
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

// fireAttributeChange simulates a CCU echo by handing the bridge's own
// subscription Reporter the dirty path directly — the same closure the
// engine calls on a tick, minus the engine's cadence gate.
func (h *scenarioHarness) fireAttributeChange(ctx string, st scenarioStep) {
	h.t.Helper()
	sub, _, spec := h.activeSub(st.SubscriptionIdx)
	if sub == nil {
		h.t.Errorf("%s: no established subscription at index %d", ctx, st.SubscriptionIdx)
		return
	}
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
	h.br.SubscriptionReporter()(context.Background(), sub, paths)
}

// fireViaEngine drives the bridge through the dirty-mark → engine
// tick → report path that production uses for every CCU echo. The
// subscription manager was started during setup with the bridge's real
// Reporter, so OnAttributeChanged enqueues a dirty mark and the next
// tick drains it.
//
// The tick is bounded below by the subscription's MinIntervalFloor
// (1 s by harness default); expect_tx's read deadline absorbs that.
func (h *scenarioHarness) fireViaEngine(ctx string, st scenarioStep) {
	h.t.Helper()
	_, _, spec := h.activeSub(st.SubscriptionIdx)
	p := spec.effectivePaths()[0]
	h.subMgr.OnAttributeChanged(im.ConcreteAttributePath{
		Endpoint:     p.Endpoint,
		Cluster:      p.Cluster,
		Attribute:    p.Attribute,
		HasEndpoint:  true,
		HasCluster:   true,
		HasAttribute: true,
	})
	_ = ctx
}

// fireNotifierSource drives the bridge through the full change-notifier
// callback chain: it looks up a fake source in the topology recipe and
// calls its fire(). The listener the bridge registered at assembly time
// then dirty-marks the source's own cluster paths, and the engine
// drains on the next tick.
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

// readOutbound pulls the next datagram off the peer socket and decodes
// its two headers. The IM body is returned alongside so callers can
// assert on it without decrypting twice.
func (h *scenarioHarness) readOutbound(ctx string) (message.Header, message.ProtocolHeader, []byte, bool) {
	h.t.Helper()
	_ = h.peerConn.SetReadDeadline(time.Now().Add(scenarioReadDeadline))
	buf := make([]byte, 1500)
	n, _, err := h.peerConn.ReadFromUDP(buf)
	if err != nil {
		h.t.Errorf("%s: peer ReadFromUDP: %v", ctx, err)
		return message.Header{}, message.ProtocolHeader{}, nil, false
	}
	got := buf[:n]

	hdr, hdrLen, err := message.UnmarshalHeader(got)
	if err != nil {
		h.t.Errorf("%s: UnmarshalHeader: %v", ctx, err)
		return message.Header{}, message.ProtocolHeader{}, nil, false
	}
	peerSess, ok := h.peerSessionByID[hdr.SessionID]
	if !ok {
		h.t.Errorf("%s: no peer session for header.SessionID=%d (have %v)", ctx, hdr.SessionID, sortedSessionIDs(h.peerSessionByID))
		return message.Header{}, message.ProtocolHeader{}, nil, false
	}
	plain, _, err := peerSess.Decrypt(&hdr, securityFlagsByte(&hdr), got[hdrLen:])
	if err != nil {
		h.t.Errorf("%s: decrypt: %v", ctx, err)
		return message.Header{}, message.ProtocolHeader{}, nil, false
	}
	proto, protoLen, err := message.UnmarshalProtocolHeader(plain)
	if err != nil {
		h.t.Errorf("%s: UnmarshalProtocolHeader: %v", ctx, err)
		return message.Header{}, message.ProtocolHeader{}, nil, false
	}
	return hdr, proto, plain[protoLen:], true
}

// expectTX captures the next outbound datagram on peerConn, decrypts
// it against the matching peer session, decodes the protocol header,
// and asserts the fields the step specifies. Bindings declared via
// bind_*_to enter the harness's variable table for later steps.
func (h *scenarioHarness) expectTX(ctx string, st scenarioStep) {
	h.t.Helper()
	hdr, proto, imBody, ok := h.readOutbound(ctx)
	if !ok {
		return
	}
	_, _, spec := h.activeSub(st.SubscriptionIdx)
	h.ackIfNeeded(spec, hdr, proto)

	switch st.Opcode {
	case "ReportData":
		h.expectOpcode(ctx, proto.Opcode, im.OpcodeReportData, st.Opcode)
	case "WriteResponse":
		h.expectOpcode(ctx, proto.Opcode, im.OpcodeWriteResponse, st.Opcode)
	case "StatusResponse":
		h.expectOpcode(ctx, proto.Opcode, im.OpcodeStatusResponse, st.Opcode)
	case "SubscribeResponse":
		h.expectOpcode(ctx, proto.Opcode, im.OpcodeSubscribeResponse, st.Opcode)
	case "InvokeResponse":
		h.expectOpcode(ctx, proto.Opcode, im.OpcodeInvokeResponse, st.Opcode)
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
		for _, sp := range h.s.Given.Subscriptions {
			if proto.ExchangeID == sp.PeerSubscribeExchangeID {
				h.t.Errorf("%s: proto.ExchangeID = %d equals subscription[session=%d] subscribe-exchange — an ongoing report reused the peer-opened exchange",
					ctx, proto.ExchangeID, sp.SessionID)
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
			h.t.Errorf("%s: attribute_reports count = %d, want %d — the notifier emitted reports for paths outside its own cluster",
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

func (h *scenarioHarness) expectOpcode(ctx string, got, want uint8, name string) {
	h.t.Helper()
	if got != want {
		h.t.Errorf("%s: proto.Opcode = %#x, want %s (%#x)", ctx, got, name, want)
	}
}

// sendFromPeer seals one IM payload under the spec's peer session and
// ships it to the bridge's listener.
func (h *scenarioHarness) sendFromPeer(spec scenarioSubSpec, opcode uint8, exchangeID uint16, body []byte, initiator, needsAck bool) bool {
	h.t.Helper()
	peerSess, ok := h.peerSessionByID[spec.SessionID]
	if !ok {
		h.t.Errorf("scenario: no peer session for session_id %d", spec.SessionID)
		return false
	}
	proto := message.ProtocolHeader{
		Initiator:  initiator,
		NeedsAck:   needsAck,
		Opcode:     opcode,
		ExchangeID: exchangeID,
		ProtocolID: im.InteractionModelProtocolID,
	}
	payload := append(proto.Marshal(), body...) //nolint:gocritic // appendAssign: the header is the prefix of a fresh datagram
	hdr := message.Header{
		SessionID:  spec.SessionID,
		DestSize:   message.DestNodeID,
		DestNodeID: h.bridgeNodeID,
	}
	enc, err := peerSess.Encrypt(&hdr, securityFlagsByte(&hdr), payload)
	if err != nil {
		h.t.Errorf("scenario: peer.Encrypt (opcode 0x%02X): %v", opcode, err)
		return false
	}
	datagram := append(hdr.Marshal(), enc.Ciphertext...)
	if _, err := h.peerConn.WriteToUDP(datagram, h.bridgeIn); err != nil {
		h.t.Errorf("scenario: WriteToUDP (opcode 0x%02X): %v", opcode, err)
		return false
	}
	return true
}

// ackIfNeeded answers a reliable outbound message with a Secure-Channel
// StandaloneAck, which is what a real controller does when it has no
// reply to piggyback on. The bridge's ack pump is live in this harness,
// so an un-acked datagram would be retransmitted into the next step's
// assertion window.
func (h *scenarioHarness) ackIfNeeded(spec scenarioSubSpec, hdr message.Header, proto message.ProtocolHeader) {
	h.t.Helper()
	if !proto.NeedsAck {
		return
	}
	peerSess, ok := h.peerSessionByID[hdr.SessionID]
	if !ok {
		peerSess, ok = h.peerSessionByID[spec.SessionID]
		if !ok {
			return
		}
	}
	// Matter §4.4.3.1: the I flag says whether the SENDER opened the
	// exchange, so the acknowledging side inverts what it received.
	ackProto := message.ProtocolHeader{
		Initiator:  !proto.Initiator,
		HasAck:     true,
		AckCounter: hdr.MessageCounter,
		Opcode:     mrp.StandaloneAckOpcode,
		ExchangeID: proto.ExchangeID,
		ProtocolID: mrp.SecureChannelProtocolID,
	}
	ackHdr := message.Header{
		SessionID:  hdr.SessionID,
		DestSize:   message.DestNodeID,
		DestNodeID: h.bridgeNodeID,
	}
	enc, err := peerSess.Encrypt(&ackHdr, securityFlagsByte(&ackHdr), ackProto.Marshal())
	if err != nil {
		h.t.Errorf("scenario: peer.Encrypt (standalone ack): %v", err)
		return
	}
	datagram := append(ackHdr.Marshal(), enc.Ciphertext...)
	if _, err := h.peerConn.WriteToUDP(datagram, h.bridgeIn); err != nil {
		h.t.Errorf("scenario: WriteToUDP (standalone ack): %v", err)
	}
}

// sendStatusResponse ships an IM:StatusResponse from the peer to the
// bridge on the specified exchange — the action a controller performs
// after consuming a ReportData. The bridge's receive pipeline then
// emits the matter.rx.im.status_ack debug log, which is the observable
// signal downstream steps assert.
func (h *scenarioHarness) sendStatusResponse(ctx string, st scenarioStep) {
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
	_, _, spec := h.activeSub(st.SubscriptionIdx)
	// A StatusResponse on a bridge-opened exchange (an ongoing report)
	// carries I=0; the peer only keeps I=1 on exchanges it opened
	// itself, which is the drain case handled by sendStatusResponseOn.
	h.sendStatusResponseOn(spec, exch, status, false)
}

// sendStatusResponseOn is the exchange-explicit form used by both the
// scenario step and the chunk drain.
func (h *scenarioHarness) sendStatusResponseOn(spec scenarioSubSpec, exchangeID uint16, status im.StatusCode, peerOpenedExchange bool) {
	h.t.Helper()
	body, err := bridge.EncodeStatusResponse(im.StatusResponse{Status: status})
	if err != nil {
		h.t.Errorf("scenario: EncodeStatusResponse: %v", err)
		return
	}
	h.sendFromPeer(spec, im.OpcodeStatusResponse, exchangeID, body, peerOpenedExchange, false)
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
	deadline := time.Now().Add(scenarioReadDeadline)
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

// closeSession drives the session-teardown cascade: CloseSession evicts
// every subscription on the session, mirroring the daemon's own hook on
// CASE teardown. Any subsequent fire on the same path is then expected
// to be a no-op, which the scenario asserts via expect_no_tx.
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
	_ = h.peerConn.SetReadDeadline(time.Now().Add(scenarioReadDeadline))
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

// drainSubscribeChunks reads ReportData chunks from the peer socket,
// ACKs each with a StatusResponse on the same exchange, and stops once
// the bridge ships a SubscribeResponse. Locks the per-chunk handshake:
// without it the negotiation deadlocks between chunk N and chunk N+1,
// because a controller stays in the per-chunk read state until each
// StatusResponse round-trips.
//
// Binds the SubscribeResponse's exchange ID into the step's
// bind_exchange_id_to slot. The intermediate ReportData chunks all
// share the peer-opened Subscribe exchange.
func (h *scenarioHarness) drainSubscribeChunks(ctx string, st scenarioStep) {
	h.t.Helper()
	_, _, spec := h.activeSub(st.SubscriptionIdx)
	deadline := time.Now().Add(5 * time.Second)
	chunks := 0
	for time.Now().Before(deadline) {
		hdr, proto, _, ok := h.readOutbound(ctx)
		if !ok {
			return
		}
		h.ackIfNeeded(spec, hdr, proto)
		switch proto.Opcode {
		case im.OpcodeReportData:
			chunks++
			// The chunks ride the exchange the peer opened with its
			// SubscribeRequest, so its ack keeps I=1.
			h.sendStatusResponseOn(spec, proto.ExchangeID, im.StatusSuccess, true)
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

// sendInvokeMoveToLevel ships an IM:InvokeRequestMessage carrying a
// LevelControl.MoveToLevel command (cluster 0x0008, command 0x00) from
// the peer. The command-fields struct contains tag 0 Level (uint8)
// only, which pins that a command carrying a field payload reaches the
// cluster server with that payload intact.
func (h *scenarioHarness) sendInvokeMoveToLevel(ctx string, st scenarioStep) {
	h.t.Helper()
	_, _, spec := h.activeSub(st.SubscriptionIdx)
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
	h.sendFromPeer(spec, im.OpcodeInvokeRequest, exch, body, true, true)
}

// sendReadRequest ships an IM:ReadRequestMessage from the peer to the
// bridge. The bridge resolves the requested paths through the
// dispatcher (with FabricFiltered scoping when the request flag is
// set, Matter §10.6.3), assembles the ReportData, and ships it back on
// the same exchange.
func (h *scenarioHarness) sendReadRequest(ctx string, st scenarioStep) {
	h.t.Helper()
	_, _, spec := h.activeSub(st.SubscriptionIdx)

	req := im.ReadRequest{
		AttributeRequests: h.pathsOf(spec, st.Wildcard),
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
	h.sendFromPeer(spec, im.OpcodeReadRequest, exch, body, true, true)
}

// sendSubscribeRequest ships an IM:SubscribeRequestMessage from the
// peer to the bridge — drives the full Subscribe-negotiation pipeline.
// The bridge replies with the initial ReportData stream (one or more
// chunks, each acked via per-chunk StatusResponse), then a
// SubscribeResponse echoing the negotiated subscriptionId and
// MaxInterval.
func (h *scenarioHarness) sendSubscribeRequest(ctx string, st scenarioStep) {
	h.t.Helper()
	_, _, spec := h.activeSub(st.SubscriptionIdx)

	minFloor := spec.MinIntervalFloorSeconds
	if minFloor == 0 && spec.MaxIntervalCeilingSeconds == 0 {
		minFloor = 1
	}
	maxCeil := spec.MaxIntervalCeilingSeconds
	if maxCeil == 0 {
		maxCeil = 60
	}
	req := im.SubscribeRequest{
		KeepSubscriptions:  true,
		MinIntervalFloor:   minFloor,
		MaxIntervalCeiling: maxCeil,
		AttributeRequests:  h.pathsOf(spec, st.Wildcard),
	}
	enc := tlv.NewEncoder()
	req.MarshalTLV(enc)
	body, err := enc.Bytes()
	if err != nil {
		h.t.Errorf("%s: encode SubscribeRequest: %v", ctx, err)
		return
	}
	h.sendFromPeer(spec, im.OpcodeSubscribeRequest, spec.PeerSubscribeExchangeID, body, true, true)
}

// sendWriteRequest ships an IM:WriteRequestMessage from the peer to the
// bridge. The bridge's dispatcher resolves the path, calls the matching
// cluster server (or surfaces a Status when the path isn't resolvable),
// and replies with a WriteResponse on the same exchange.
func (h *scenarioHarness) sendWriteRequest(ctx string, st scenarioStep) {
	h.t.Helper()
	_, _, spec := h.activeSub(st.SubscriptionIdx)

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
	h.sendFromPeer(spec, im.OpcodeWriteRequest, exch, body, true, true)
}

// tickRetransmit yields to the bridge's own ack pump, which owns the
// outbound retransmit half. go-fabric exports no single-shot tick for
// it, so the harness runs the pump for real (attached before Start) and
// waits out one §4.12.6 backoff here; the following expect_tx's read
// deadline covers the rest.
func (h *scenarioHarness) tickRetransmit(ctx string, _ scenarioStep) {
	h.t.Helper()
	time.Sleep(mrp.MRPBackoffBase)
	_ = ctx
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
