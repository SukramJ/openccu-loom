// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
)

// TestIsRequestOpcode_Table verifies IsRequestOpcode against the full set
// of known IM opcodes and one unknown value.
func TestIsRequestOpcode_Table(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		opcode uint8
		want   bool
	}{
		// Request opcodes — must return true.
		{"ReadRequest", im.OpcodeReadRequest, true},
		{"WriteRequest", im.OpcodeWriteRequest, true},
		{"InvokeRequest", im.OpcodeInvokeRequest, true},
		{"SubscribeRequest", im.OpcodeSubscribeRequest, true},
		{"TimedRequest", im.OpcodeTimedRequest, true},
		// Response opcodes — must return false.
		{"StatusResponse", im.OpcodeStatusResponse, false},
		{"ReportData", im.OpcodeReportData, false},
		{"WriteResponse", im.OpcodeWriteResponse, false},
		{"InvokeResponse", im.OpcodeInvokeResponse, false},
		{"SubscribeResponse", im.OpcodeSubscribeResponse, false},
		// Unknown opcode — must return false.
		{"Unknown_0xFF", 0xFF, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := im.IsRequestOpcode(tc.opcode)
			if got != tc.want {
				t.Errorf("IsRequestOpcode(0x%02X) = %v, want %v", tc.opcode, got, tc.want)
			}
		})
	}
}
