// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

import "testing"

func TestRPCServerTypeValues(t *testing.T) {
	// RPCServerType must include XML_RPC, BIN_RPC, and None.
	// OpenCCU-Loom adds BIN_RPC for CUxD (
	// workaround instead).
	want := map[RPCServerType]string{
		RPCServerTypeXMLRPC: "xml_rpc",
		RPCServerTypeBINRPC: "bin_rpc",
		RPCServerTypeNone:   "none",
	}
	for v, s := range want {
		if v.String() != s {
			t.Errorf("RPCServerType %v: String()=%q, want %q", v, v.String(), s)
		}
	}
}

func TestRPCTypeValues(t *testing.T) {
	cases := map[RPCType]string{
		RPCTypeXMLRPC:  "xmlrpc",
		RPCTypeJSONRPC: "jsonrpc",
	}
	for v, s := range cases {
		if v.String() != s {
			t.Errorf("RPCType %v: String()=%q, want %q", v, v.String(), s)
		}
	}
}
