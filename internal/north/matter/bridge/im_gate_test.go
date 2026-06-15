// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// TestClassifyIMOpcode is a table test over every IM opcode ×
// {unicast session, group session} → expected imGateAction.
// No Bridge construction is needed; classifyIMOpcode is a pure function.
func TestClassifyIMOpcode(t *testing.T) {
	t.Parallel()

	const (
		unicast = message.SessionUnsecured
		group   = message.SessionGroup
	)

	tests := []struct {
		name        string
		opcode      uint8
		sessionType message.SessionType
		want        imGateAction
	}{
		// StatusResponse is always absorbed regardless of session type.
		{"StatusResponse/unicast", im.OpcodeStatusResponse, unicast, imGateAbsorbStatusResp},
		{"StatusResponse/group", im.OpcodeStatusResponse, group, imGateAbsorbStatusResp},

		// Non-request response opcodes are always rejected as unsupported.
		{"ReportData/unicast", im.OpcodeReportData, unicast, imGateRejectUnsupported},
		{"ReportData/group", im.OpcodeReportData, group, imGateRejectUnsupported},
		{"WriteResponse/unicast", im.OpcodeWriteResponse, unicast, imGateRejectUnsupported},
		{"WriteResponse/group", im.OpcodeWriteResponse, group, imGateRejectUnsupported},
		{"InvokeResponse/unicast", im.OpcodeInvokeResponse, unicast, imGateRejectUnsupported},
		{"InvokeResponse/group", im.OpcodeInvokeResponse, group, imGateRejectUnsupported},
		{"SubscribeResponse/unicast", im.OpcodeSubscribeResponse, unicast, imGateRejectUnsupported},
		{"SubscribeResponse/group", im.OpcodeSubscribeResponse, group, imGateRejectUnsupported},

		// Unknown/invalid opcode is always rejected as unsupported.
		{"InvalidOpcode0x00/unicast", 0x00, unicast, imGateRejectUnsupported},
		{"InvalidOpcode0x00/group", 0x00, group, imGateRejectUnsupported},
		{"InvalidOpcode0xFF/unicast", 0xFF, unicast, imGateRejectUnsupported},
		{"InvalidOpcode0xFF/group", 0xFF, group, imGateRejectUnsupported},

		// Read/Subscribe/Timed are group-session-rejected; unicast proceeds.
		{"ReadRequest/unicast", im.OpcodeReadRequest, unicast, imGateProceed},
		{"ReadRequest/group", im.OpcodeReadRequest, group, imGateRejectGroupSession},
		{"SubscribeRequest/unicast", im.OpcodeSubscribeRequest, unicast, imGateProceed},
		{"SubscribeRequest/group", im.OpcodeSubscribeRequest, group, imGateRejectGroupSession},
		{"TimedRequest/unicast", im.OpcodeTimedRequest, unicast, imGateProceed},
		{"TimedRequest/group", im.OpcodeTimedRequest, group, imGateRejectGroupSession},

		// Write/Invoke are valid in both unicast and group sessions.
		{"WriteRequest/unicast", im.OpcodeWriteRequest, unicast, imGateProceed},
		{"WriteRequest/group", im.OpcodeWriteRequest, group, imGateProceed},
		{"InvokeRequest/unicast", im.OpcodeInvokeRequest, unicast, imGateProceed},
		{"InvokeRequest/group", im.OpcodeInvokeRequest, group, imGateProceed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyIMOpcode(tc.opcode, tc.sessionType)
			if got != tc.want {
				t.Errorf("classifyIMOpcode(0x%02X, %v) = %v, want %v",
					tc.opcode, tc.sessionType, got, tc.want)
			}
		})
	}
}
