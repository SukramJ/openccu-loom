// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
// — the CCU returns one entry per *currently active* interface, so
// every entry is reported as Reachable=true unless the firmware adds an
// explicit `connected: false`. Interfaces the CCU is no longer serving
// simply do not appear, and nothing downstream turns that absence into
// Reachable=false: [coordinators.Reconciler] iterates the probed
// entries only, so an interface that vanishes keeps its last reported
// state until it comes back. A firmware without the `connected` field
// therefore never produces an unreachable transition through this
// probe.
//
// Some CCU firmwares add extra fields like `info` or `connected` to
// each entry. If a `connected` boolean is present it is honored;
// otherwise the entry counts as reachable.
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
// a list of interface-reachability tuples. The CCU's response shape
// is `[{"name": "BidCos-RF", ...}, ...]` — only the `name` and the
// optional `connected` field are inspected. The round-trip duration is
// measured and reported in [coordinators.InterfaceReachability.LatencyMs]
// so the Reconciler can forward it via [hmevent.ConnectivityChangedEvent]
// to the MQTT bridge for the per-interface latency sensor.
func (p *JSONRPCConnectivityProbe) Probe(ctx context.Context) ([]coordinators.InterfaceReachability, error) {
	if p == nil || p.client == nil {
		return nil, errors.New("connectivity_probe: client is nil")
	}
	var raw []map[string]any
	start := time.Now()
	if err := p.client.Call(ctx, "Interface.listInterfaces", nil, &raw); err != nil {
		return nil, fmt.Errorf("connectivity_probe: %w", err)
	}
	latencyMs := float64(time.Since(start).Milliseconds())
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
