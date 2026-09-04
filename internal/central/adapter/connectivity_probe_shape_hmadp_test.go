// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
)

// TestHmAdpProbeOnTheFirmwareResponseShapeReportsMembershipOnly feeds the
// response the firmware actually emits — exactly name, port and info per
// entry, per www/api/methods/interface/listinterfaces.tcl — rather than a
// `connected` member invented for the test.
//
// It pins the documented property: on that shape the probe reports every
// configured interface as reachable, so it measures membership and never
// liveness. A future change that makes this probe claim to answer reachability
// must either read a member the firmware sends or call a different method.
func TestHmAdpProbeOnTheFirmwareResponseShapeReportsMembershipOnly(t *testing.T) {
	t.Parallel()

	srv := newProbeServer(t, `{"result":[
		{"name":"BidCos-RF","port":2001,"info":"BidCos-RF"},
		{"name":"HmIP-RF","port":2010,"info":"HmIP-RF"},
		{"name":"VirtualDevices","port":9292,"info":"Virtual Devices"}
	]}`)
	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	got, err := NewJSONRPCConnectivityProbe(jc).Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(got), got)
	}
	for _, ir := range got {
		if !ir.Reachable {
			t.Fatalf("entry %q read as unreachable: the firmware response carries no liveness member, so every configured interface must read reachable here — %+v",
				ir.InterfaceID, ir)
		}
	}
}
