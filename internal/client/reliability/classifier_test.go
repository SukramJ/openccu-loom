// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
		tc := tc
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
		tc := tc
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := tc.class.String(); got != tc.want {
				t.Errorf("RPCClass(%d).String() = %q, want %q", tc.class, got, tc.want)
			}
		})
	}
}
