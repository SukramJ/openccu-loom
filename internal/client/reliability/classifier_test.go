// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
)

func TestClassifyMethod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		want   reliability.RPCClass
	}{
		// Read methods
		{name: "getValue", method: "getValue", want: reliability.RPCClassRead},
		{name: "getParamset", method: "getParamset", want: reliability.RPCClassRead},
		{name: "listDevices", method: "listDevices", want: reliability.RPCClassRead},
		{name: "Room.getAll", method: "Room.getAll", want: reliability.RPCClassRead},
		{name: "Subsection.getAll", method: "Subsection.getAll", want: reliability.RPCClassRead},
		// Write methods
		{name: "setValue", method: "setValue", want: reliability.RPCClassWrite},
		{name: "putParamset", method: "putParamset", want: reliability.RPCClassWrite},
		{name: "addLink", method: "addLink", want: reliability.RPCClassWrite},
		{name: "Program.execute", method: "Program.execute", want: reliability.RPCClassWrite},
		// Control methods
		{name: "init", method: "init", want: reliability.RPCClassControl},
		{name: "ping", method: "ping", want: reliability.RPCClassControl},
		{name: "system.listMethods", method: "system.listMethods", want: reliability.RPCClassControl},
		{name: "session.login", method: "session.login", want: reliability.RPCClassControl},
		// Unknown
		{name: "someUnknownMethod", method: "someUnknownMethod", want: reliability.RPCClassUnknown},
		{name: "empty string", method: "", want: reliability.RPCClassUnknown},
		// Case-insensitivity
		{name: "GETVALUE uppercase", method: "GETVALUE", want: reliability.RPCClassRead},
		{name: "setvalue lowercase", method: "setvalue", want: reliability.RPCClassWrite},
		// Whitespace tolerance
		{name: "init with whitespace", method: "  init  ", want: reliability.RPCClassControl},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := reliability.ClassifyMethod(tc.method)
			if got != tc.want {
				t.Errorf("ClassifyMethod(%q) = %v, want %v", tc.method, got, tc.want)
			}
		})
	}
}

func TestRPCClassString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		class reliability.RPCClass
		want  string
	}{
		{reliability.RPCClassRead, "read"},
		{reliability.RPCClassWrite, "write"},
		{reliability.RPCClassControl, "control"},
		{reliability.RPCClassUnknown, "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := tc.class.String(); got != tc.want {
				t.Errorf("RPCClass(%d).String() = %q, want %q", tc.class, got, tc.want)
			}
		})
	}
}

// TestReportValueUsageIsAWrite pins the classification of a method whose name
// reads like a query and whose effect is a device reconfiguration.
//
// The CCU documents it as telling the interface process how often a value is
// used so that it can establish or delete the connection to the component
// (src/rfd/XmlRpcMethods.cpp:700-723). It persists the per-channel usage
// record and it adds or removes the direct link peer between the channel and
// the central; its BidCos return value is a transmission verdict, not a query
// result — false means the device was unreachable, the change is queued, and
// CONFIG_PENDING is now set on its MAINTENANCE channel.
//
// The daemon uses it for exactly that: central_links.go calls it per channel
// with refCounter 1 to create a central link and 0 to tear one down. A read
// classification paces it on the read throttle and misreports what the daemon
// is doing to the CCU.
func TestReportValueUsageIsAWrite(t *testing.T) {
	t.Parallel()

	if got := reliability.ClassifyMethod("reportValueUsage"); got != reliability.RPCClassWrite {
		t.Errorf("ClassifyMethod(reportValueUsage) = %v, want %v", got, reliability.RPCClassWrite)
	}
}
