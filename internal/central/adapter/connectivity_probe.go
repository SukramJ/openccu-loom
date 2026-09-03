// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
)

// JSONRPCConnectivityProbe implements [coordinators.ConnectivityProbe]
// by calling the CCU's `Interface.listInterfaces` JSON-RPC method.
//
// It measures MEMBERSHIP, not liveness, and the distinction is load-bearing:
// `Interface.listInterfaces` is a configuration listing. The firmware script
// walks the interface list built from /etc/config/InterfacesList.xml through a
// /var/tmp cache and appends exactly three members per entry — name, port,
// info (www/api/methods/interface/listinterfaces.tcl, sourced via
// www/api/homematic.cgi and www/api/eq3/ipc.tcl). So an interface process that
// has died still appears, and no member reports whether it is serving.
//
// Consequence, stated plainly: on stock firmware every entry reads
// Reachable=true and this probe can never carry an unreachable transition on
// its own. A total interface outage looks fully reachable here. The `connected`
// decode below is defensive only — no firmware in the OpenCCU source or its
// distribution patch set emits that member, so it has never been observed to
// fire.
//
// What would actually settle reachability, none of which this probe issues:
// `Interface.listBidcosInterfaces` (which does report isConnected, per BidCoS
// radio gateway rather than per interface process),
// `Interface.getLGWConnectionStatus`, or a per-interface XML-RPC round trip.
// Wiring one of those is the fix for reachability; correcting this comment is
// not.
type JSONRPCConnectivityProbe struct {
	client *jsonrpc.Client
}

// NewJSONRPCConnectivityProbe wires the probe against an existing
// [jsonrpc.Client]. The client must already be logged in — the probe
// itself only issues the read-only `Interface.listInterfaces` call.
func NewJSONRPCConnectivityProbe(client *jsonrpc.Client) *JSONRPCConnectivityProbe {
	return &JSONRPCConnectivityProbe{client: client}
}

// Probe issues `Interface.listInterfaces` and decodes the response into
// a list of interface-reachability tuples. The firmware's response shape is
// `[{"name":…,"port":…,"info":…}, …]` — only `name` is inspected, plus a
// `connected` boolean no shipped firmware sends. The round-trip duration is
// measured and reported in [coordinators.InterfaceReachability.LatencyMs]
// so the Reconciler can forward it via [hmevent.ConnectivityChangedEvent].
//
// The duration is kept in fractional milliseconds. Truncating to whole
// milliseconds read as 0 for every answer a CCU on the same LAN returns in
// under a millisecond, which is indistinguishable from "never measured" and
// defeats the value-equality dedup on any aggregate that stores it.
func (p *JSONRPCConnectivityProbe) Probe(ctx context.Context) ([]coordinators.InterfaceReachability, error) {
	if p == nil || p.client == nil {
		return nil, errors.New("connectivity_probe: client is nil")
	}
	var raw []map[string]any
	start := time.Now()
	if err := p.client.Call(ctx, "Interface.listInterfaces", nil, &raw); err != nil {
		return nil, fmt.Errorf("connectivity_probe: %w", err)
	}
	latencyMs := float64(time.Since(start).Nanoseconds()) / float64(time.Millisecond)
	out := make([]coordinators.InterfaceReachability, 0, len(raw))
	for _, entry := range raw {
		name, _ := entry["name"].(string)
		if name == "" {
			continue
		}
		reachable := true
		if v, ok := entry["connected"]; ok {
			if b, ok := v.(bool); ok {
				reachable = b
			}
		}
		out = append(out, coordinators.InterfaceReachability{
			InterfaceID: name,
			Reachable:   reachable,
			LatencyMs:   latencyMs,
		})
	}
	return out, nil
}
