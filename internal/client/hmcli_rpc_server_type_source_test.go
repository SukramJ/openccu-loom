// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHmCliRPCServerTypeForInterfaceDerivesFromHmenum pins that this
// package's answer AGREES with the two pkg/hmenum halves for every interface
// hmenum declares, and for one it does not.
//
// It does not, and cannot, pin the absence of a local copy: the switch this
// function replaced returns the same value for all five interfaces and for an
// unknown one, so it passes with either implementation. That property is
// structural and belongs to [TestW2CliRPCServerTypeKeepsNoLocalCopy]; what
// this test adds is the value leg — a copy that has already diverged, or a
// derivation that stops matching hmenum after a row moves.
//
// The two halves of that datum both live in pkg/hmenum, and hmenum's own doc
// comment on InterfaceRPCServerType says so: the map answers
// [hmenum.RPCServerTypeNone] for CUxD because CUxD's callbacks are served by
// the BIN-RPC listener, which the map does not model, and callers are told to
// consult [hmenum.Interface.IsBINRPC] alongside it. A local switch restating
// the same five rows is a second home that drifts silently — nothing in the
// gate notices, because each copy is pinned by its own suite.
//
// The expectation below is computed from the hmenum sources rather than
// written out as a table, so a new interface (or a moved row) is covered the
// moment hmenum declares it.
func TestHmCliRPCServerTypeForInterfaceDerivesFromHmenum(t *testing.T) {
	t.Parallel()

	if len(hmenum.InterfacesSupportingRPCCallback) == 0 {
		t.Fatal("hmenum declares no callback interfaces — the guard would pass vacuously")
	}
	for iface := range hmenum.InterfacesSupportingRPCCallback {
		want := hmenum.InterfaceRPCServerType[iface]
		if iface.IsBINRPC() {
			want = hmenum.RPCServerTypeBINRPC
		}
		if got := RPCServerTypeForInterface(iface); got != want {
			t.Errorf("RPCServerTypeForInterface(%s) = %s, want %s derived from pkg/hmenum (InterfaceRPCServerType + IsBINRPC)", iface, got, want)
		}
	}

	// An interface hmenum does not classify at all must not acquire a
	// callback server by accident.
	if got := RPCServerTypeForInterface(hmenum.Interface("NoSuchInterface")); got != hmenum.RPCServerTypeNone {
		t.Errorf("RPCServerTypeForInterface(unknown) = %s, want %s", got, hmenum.RPCServerTypeNone)
	}
}
