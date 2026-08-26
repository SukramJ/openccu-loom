// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// ping_pong_caller_id_contract_test.go locks the invariant that two OpenCCU-Loom
// daemons targeting the same CCU must produce distinguishable ping caller_id
// bases. The CCU broadcasts every PONG it receives to all registered clients, so
// a co-located daemon's PONGs arrive on OUR callback server. If both daemons key
// their pings on the bare interface name ("HmIP-RF"), each PONG is ambiguous and
// the receiving daemon files it as an unmatched "unknown" mismatch — degrading
// the health score until /health returns 503. Keying on the full
// <instance>-<central>-<interface> triple (via Config.InitInterfaceID → WireBoundaryID)
// makes PONGs attributable: each daemon rejects the other's PONGs instead of
// counting them as noise.
package contract

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// nopCaller is a no-op Caller for clients that only need to exist, not call.
var nopCaller = client.CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil })

// TestPingPongCallerIDDistinguishesDaemons asserts that two daemons with the
// same CentralName and Interface but different InitInterfaceID values produce
// distinct WireBoundaryID values, and that neither degenerates to the bare
// interface name. This prevents PONGs from a co-located daemon being filed as
// unmatched mismatches on our side.
func TestPingPongCallerIDDistinguishesDaemons(t *testing.T) {
	t.Parallel()

	a, err := client.New(client.Config{
		CentralName:     "OttoLoom",
		Interface:       hmenum.InterfaceHmIPRF,
		InitInterfaceID: "Otto-OttoLoom-HmIP-RF",
		Caller:          nopCaller,
	})
	if err != nil {
		t.Fatalf("client.New (a): %v", err)
	}

	b, err := client.New(client.Config{
		CentralName:     "OttoLoom",
		Interface:       hmenum.InterfaceHmIPRF,
		InitInterfaceID: "OtherLoom-OttoLoom-HmIP-RF",
		Caller:          nopCaller,
	})
	if err != nil {
		t.Fatalf("client.New (b): %v", err)
	}

	idA := a.WireBoundaryID()
	idB := b.WireBoundaryID()
	bare := string(hmenum.InterfaceHmIPRF)

	if idA == idB {
		t.Errorf("WireBoundaryID collision: both daemons return %q; PONGs would be indistinguishable", idA)
	}
	if idA == bare {
		t.Errorf("daemon A WireBoundaryID = %q equals bare interface name; must embed the instance triple", idA)
	}
	if idB == bare {
		t.Errorf("daemon B WireBoundaryID = %q equals bare interface name; must embed the instance triple", idB)
	}
}
