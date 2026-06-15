// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// imGateAction is the pre-dispatch routing decision for an inbound IM
// message, computed from opcode + session type alone — no Bridge state,
// so it is table-testable in isolation.
type imGateAction int

const (
	imGateProceed            imGateAction = iota // decode + dispatch
	imGateAbsorbStatusResp                       // StatusResponse: MRP ack, no dispatch
	imGateRejectUnsupported                      // non-request opcode
	imGateRejectGroupSession                     // Read/Subscribe/Timed over a group session (§8.5.7)
)

// classifyIMOpcode returns the pre-dispatch routing decision for an
// inbound IM message based on the opcode and session type alone.
//
// Group-session reject guard. Matter §8.5.7: only Write- and
// Invoke-Requests are valid over Secure Group sessions; Read,
// Subscribe, and Timed requests are unicast-only and must be
// rejected with StatusResponse(InvalidAction).
//
// Mirrors matter.js packages/protocol/src/interaction/
// InteractionMessenger.ts:240,269,287 — ReadRequest /
// SubscribeRequest / TimedRequest each throw a
// StatusResponseError(InvalidAction, "<op> is not supported in
// group sessions"). Without this guard, a malicious group sender
// could enumerate the bridge's attribute tree via a single
// multicast Read.
func classifyIMOpcode(opcode uint8, sessionType message.SessionType) imGateAction {
	if opcode == im.OpcodeStatusResponse {
		return imGateAbsorbStatusResp
	}
	if !im.IsRequestOpcode(opcode) {
		return imGateRejectUnsupported
	}
	if sessionType == message.SessionGroup {
		switch opcode {
		case im.OpcodeReadRequest, im.OpcodeSubscribeRequest, im.OpcodeTimedRequest:
			return imGateRejectGroupSession
		}
	}
	return imGateProceed
}
