// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"net/http"
	"testing"

	"thingfabric.dev/go-fabric/node"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestWindowOnDemandOpensNoWindowAtStart drives
// north.matter.commissioning.window_on_demand through startMatterBridge,
// one case per value. Off, the uncommissioned node opens its own window at
// Start; on, it opens none, and the operator's request on the node page is
// the only thing that makes it commissionable.
func TestWindowOnDemandOpensNoWindowAtStart(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		onDemand bool
		wantOpen bool
	}{
		{name: "off", onDemand: false, wantOpen: true},
		{name: "on", onDemand: true, wantOpen: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bundle, opts := windowBundle(t, "ccu-on-demand-"+tc.name, func(cfg *config.Config) {
				cfg.North.Matter.Commissioning.Passcode = 20202021
				cfg.North.Matter.Commissioning.WindowOnDemand = tc.onDemand
			})
			if w := bundle.node.Window(); w.Open != tc.wantOpen {
				t.Fatalf("window open at start = %v (own=%v), want %v", w.Open, w.Own, tc.wantOpen)
			}
			rec := serveNodeAPI(t, opts, http.MethodPost, "/api/v1/node/pairing/window", `{"duration_seconds":180}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("operator open = %d %s, want 200", rec.Code, rec.Body.String())
			}
			if w := bundle.node.Window(); w.Kind != node.WindowOwn {
				t.Fatalf("after the operator's open: kind=%v, want the node's own window", w.Kind)
			}
		})
	}
}

// TestWindowOnDemandKeepsTheBridgeClosedAfterTheOperatorsWindow: without a
// configured passcode the operator's window runs on fresh credentials
// (node.OpenBasicWindow). When it is closed, a node without a fabric
// reopens its own window on credentials nobody was shown — unless
// window_on_demand leaves every window to the operator.
func TestWindowOnDemandKeepsTheBridgeClosedAfterTheOperatorsWindow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		onDemand       bool
		wantOpenOnStop bool
	}{
		{name: "off", onDemand: false, wantOpenOnStop: true},
		{name: "on", onDemand: true, wantOpenOnStop: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bundle, opts := windowBundle(t, "ccu-on-demand-basic-"+tc.name, func(cfg *config.Config) {
				cfg.North.Matter.Commissioning.WindowOnDemand = tc.onDemand
			})
			api := newNodeAPI(t, opts)
			if rec := serveNodeAPIOn(t, api, http.MethodPost, "/api/v1/node/pairing/window", `{"duration_seconds":180}`); rec.Code != http.StatusOK {
				t.Fatalf("operator open = %d %s, want 200", rec.Code, rec.Body.String())
			}
			if w := bundle.node.Window(); w.Kind != node.WindowBasic {
				t.Fatalf("operator's window kind = %v, want a basic window on fresh credentials", w.Kind)
			}
			if rec := serveNodeAPIOn(t, api, http.MethodPost, "/api/v1/node/pairing/window/close", ""); rec.Code != http.StatusNoContent {
				t.Fatalf("close = %d %s, want 204", rec.Code, rec.Body.String())
			}
			if w := bundle.node.Window(); w.Open != tc.wantOpenOnStop {
				t.Fatalf("after the close: open=%v kind=%v, want open=%v", w.Open, w.Kind, tc.wantOpenOnStop)
			}
		})
	}
}
