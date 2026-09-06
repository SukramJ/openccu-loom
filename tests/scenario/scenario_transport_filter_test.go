// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package scenario

import (
	"testing"

	"github.com/SukramJ/go-fabric/im"
	"github.com/SukramJ/go-fabric/transport/message"
	"github.com/SukramJ/go-fabric/transport/mrp"
)

// newFilterHarness builds the least harness transportNoise needs. The
// classifier reads only the counter table, so nothing else has to exist
// for these cases — and constructing more would obscure which state the
// answer actually depends on.
func newFilterHarness() *scenarioHarness {
	return &scenarioHarness{seenCounters: make(map[uint16]map[uint32]struct{})}
}

func datagram(sessionID uint16, counter uint32, protocolID uint16, opcode uint8) outboundDatagram {
	return outboundDatagram{
		hdr:   message.Header{SessionID: sessionID, MessageCounter: counter},
		proto: message.ProtocolHeader{ProtocolID: protocolID, Opcode: opcode},
	}
}

func reportData(sessionID uint16, counter uint32) outboundDatagram {
	return datagram(sessionID, counter, im.InteractionModelProtocolID, im.OpcodeReportData)
}

func standaloneAck(sessionID uint16, counter uint32) outboundDatagram {
	return datagram(sessionID, counter, mrp.SecureChannelProtocolID, mrp.StandaloneAckOpcode)
}

// TestTransportNoiseSkipsStandaloneAcks pins the first of the two things
// the bridge may send at a moment no scenario controls. The ack is owed
// for traffic the scenario itself produced and fires on the ack timer
// (~200 ms after receipt), so which step is running when it lands is a
// matter of machine load.
func TestTransportNoiseSkipsStandaloneAcks(t *testing.T) {
	t.Parallel()
	h := newFilterHarness()

	kind, noise := h.transportNoise(standaloneAck(139, 500))
	if !noise {
		t.Fatal("a standalone ack was not classified as transport noise")
	}
	if kind != "standalone ack" {
		t.Errorf("kind = %q, want %q", kind, "standalone ack")
	}
	if h.skippedAcks != 1 {
		t.Errorf("skippedAcks = %d, want 1 — the counter is the only evidence the filter ran", h.skippedAcks)
	}
}

// TestTransportNoiseSkipsRetransmissions is the dataversion regression:
// MRP re-ships an unacknowledged message, and a peer that does not drop
// the duplicate hands a step the message it already consumed. The
// scenario then compared a DataVersion against itself and read the
// equality as a monotonicity violation in the bridge.
func TestTransportNoiseSkipsRetransmissions(t *testing.T) {
	t.Parallel()
	h := newFilterHarness()

	if _, noise := h.transportNoise(reportData(139, 500)); noise {
		t.Fatal("the first delivery of a counter was classified as noise")
	}
	kind, noise := h.transportNoise(reportData(139, 500))
	if !noise {
		t.Fatal("a repeated counter was not classified as a retransmission")
	}
	if kind != "retransmission" {
		t.Errorf("kind = %q, want %q", kind, "retransmission")
	}
	if h.skippedDupes != 1 {
		t.Errorf("skippedDupes = %d, want 1", h.skippedDupes)
	}
}

// TestTransportNoiseIsPerSession keeps the counter table from collapsing
// multi-subscription scenarios into one another: each session runs its
// own MRP counter, so the same value on two sessions is two distinct
// messages, not a duplicate.
func TestTransportNoiseIsPerSession(t *testing.T) {
	t.Parallel()
	h := newFilterHarness()

	if _, noise := h.transportNoise(reportData(139, 500)); noise {
		t.Fatal("first session, first delivery classified as noise")
	}
	if _, noise := h.transportNoise(reportData(140, 500)); noise {
		t.Error("the same counter on a different session was treated as a duplicate")
	}
}

// TestTransportNoiseAdmitsARetransmitAfterADrop covers the one scenario
// that wants the duplicate: mrp__retransmit_on_lost_report drops a
// datagram to assert the bridge re-ships it. A peer pretending the
// message never arrived must not remember it, or the filter would
// swallow the very retransmit under test and the step would time out
// against correct behaviour.
func TestTransportNoiseAdmitsARetransmitAfterADrop(t *testing.T) {
	t.Parallel()
	h := newFilterHarness()

	dg := reportData(139, 500)
	if _, noise := h.transportNoise(dg); noise {
		t.Fatal("first delivery classified as noise")
	}
	// What dropNextTX does after reading the datagram it discards.
	delete(h.seenCounters[dg.hdr.SessionID], dg.hdr.MessageCounter)

	if _, noise := h.transportNoise(dg); noise {
		t.Error("the retransmit of a dropped datagram was filtered as a duplicate — " +
			"mrp__retransmit_on_lost_report cannot observe the resend")
	}
}

// TestTransportNoiseAdmitsApplicationMessages is the negative control on
// the whole filter: if it classified ordinary IM traffic as noise, every
// scenario would pass by never seeing anything, and the suite would go
// green measuring nothing.
func TestTransportNoiseAdmitsApplicationMessages(t *testing.T) {
	t.Parallel()
	h := newFilterHarness()

	for _, tc := range []struct {
		name   string
		opcode uint8
	}{
		{"ReportData", im.OpcodeReportData},
		{"SubscribeResponse", im.OpcodeSubscribeResponse},
		{"WriteResponse", im.OpcodeWriteResponse},
		{"InvokeResponse", im.OpcodeInvokeResponse},
		{"StatusResponse", im.OpcodeStatusResponse},
	} {
		if _, noise := h.transportNoise(datagram(139, uint32(tc.opcode)+1000, im.InteractionModelProtocolID, tc.opcode)); noise {
			t.Errorf("%s was classified as transport noise", tc.name)
		}
	}
	if h.skippedAcks != 0 || h.skippedDupes != 0 {
		t.Errorf("filter counted %d acks / %d dupes on application traffic, want 0/0",
			h.skippedAcks, h.skippedDupes)
	}
}
