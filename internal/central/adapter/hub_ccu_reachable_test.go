// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestCCUReachableFoldsInterfacesWithTheDisjunction pins the fold that
// decides what "this CCU is reachable" means when its interfaces disagree.
//
// The mixed row is the whole test. Under the conjunction it would be false,
// and every sysvar, program and system score of a CCU whose CUxD happened to
// crash would go unavailable in Home Assistant for a fault that says nothing
// about whether ReGa is answering.
func TestCCUReachableFoldsInterfacesWithTheDisjunction(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		states map[string]bool
		want   bool
	}{
		{"no interface observed yet", nil, true},
		{"every interface up", map[string]bool{"HmIP-RF": true, "BidCos-RF": true}, true},
		{"one of two down", map[string]bool{"HmIP-RF": true, "CUxD": false}, true},
		{"one of two up", map[string]bool{"HmIP-RF": false, "CUxD": true}, true},
		{"every interface down", map[string]bool{"HmIP-RF": false, "BidCos-RF": false}, false},
		{"the only interface down", map[string]bool{"HmIP-RF": false}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			conn := hub.NewConnectivity()
			for iface, up := range tc.states {
				conn.OnState(iface, up)
			}
			hubModel := hub.NewHub("ccu-01").SetConnectivity(conn)
			// regaLivenessServing pins the OTHER half of the conjunction so
			// this table keeps testing the interface fold alone.
			if got := ccuReachable(hubModel, regaLivenessServing); got != tc.want {
				t.Fatalf("ccuReachable = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCCUReachableTreatsAnUnobservedTrackerAsReachable states the seed
// choice on its own, because it is the one that is not forced by the fold.
//
// Nothing in the hub plane is published before the CCU's serial has been
// read off it, so "no interface state yet" means the daemon has just
// demonstrated it can talk to the CCU. Folding that to `offline` would
// publish a retained `offline` and grey out every hub entity of a healthy
// CCU on every daemon start, until the first reachability change happened to
// arrive — which on a stable CCU may be never.
func TestCCUReachableTreatsAnUnobservedTrackerAsReachable(t *testing.T) {
	t.Parallel()
	if !ccuReachable(hub.NewHub("ccu-01").SetConnectivity(hub.NewConnectivity()), regaLivenessServing) {
		t.Fatal("an unobserved tracker folded to unreachable")
	}
}

// TestTheCCUGateIsSeededBeforeTheHubDiscoveryConfigsThatNameIt is an
// ordering pin, and the thing it prevents is silent.
//
// Home Assistant holds an entity unavailable until every topic in its
// `availability` list has reported. Every hub discovery config queued in
// wireOneCentral now names `<base>/<central>/hub/status`, so a config that
// reaches the broker before the gate's first retained byte leaves the whole
// hub plane greyed out — until the next reachability change, which on a
// stable CCU may never come. Nothing logs an error; the entities are simply
// there and unavailable.
//
// The fan-out worker is FIFO, so the pin is on enqueue order: the gate's
// publish must appear on the wire before the first `/config`.
func TestTheCCUGateIsSeededBeforeTheHubDiscoveryConfigsThatNameIt(t *testing.T) {
	t.Parallel()
	c, pub, publisher := hubDiscoveryFixture(t)
	c.SetSystemInformation(central.SystemInfo{
		Model:   "HomeMatic Central",
		Version: "3.79.6",
		Serial:  "3014F711A0001F0123456789",
	})
	sv := &hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "Anwesenheit"}, ValueType: hmenum.HubValueTypeLogic}
	sv.OnValue(hmtypes.BoolValue(true))
	c.HubModel.PutSysvar(sv)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	gate, firstConfig := -1, -1
	for i, p := range pub.Published() {
		switch {
		case p.Topic == "openccu-loom/ccu-01/hub/status":
			if gate < 0 {
				gate = i
			}
		case strings.HasSuffix(p.Topic, "/config") && firstConfig < 0:
			firstConfig = i
		}
	}
	if gate < 0 {
		t.Fatalf("the per-CCU gate was never published; topics=%v", publishedTopics(pub))
	}
	if firstConfig < 0 {
		t.Fatal("no discovery config was published — the fixture proves nothing")
	}
	if gate > firstConfig {
		t.Errorf("first discovery config at %d, gate seeded at %d: every entity declared by "+
			"that config stays unavailable in Home Assistant until the gate's first byte "+
			"arrives", firstConfig, gate)
	}
}

// TestTheCCUGateIsNotSeededBeforeTheSerialResolves pins the other half of
// the seed's ordering, and it is a claim about what is NOT on the wire.
//
// [ccuReachable] folds an unobserved connectivity tracker to REACHABLE, and
// the only thing that makes that defensible is that the daemon has already
// talked to this CCU — it reads the serial off it. The seed used to be
// gated on nothing but the raw plane being enabled, and wireOneCentral runs
// before the serial resolves, so a CCU that was merely CONFIGURED — one that
// may have been unreachable since boot — got a retained `online` written for
// it at startup. Nothing consumes the gate that early either: no hub entity
// of this central exists until the serial stamps its unique ids.
//
// Falsifiability: drop the `hi.Serial != ""` guard from the seed in
// wireOneCentral and this test finds the retained `online`.
func TestTheCCUGateIsNotSeededBeforeTheSerialResolves(t *testing.T) {
	t.Parallel()
	_, pub, publisher := hubDiscoveryFixture(t)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	for _, p := range pub.Published() {
		if p.Topic == "openccu-loom/ccu-01/hub/status" {
			t.Fatalf("the reachability gate was seeded with %q before the CCU's serial was "+
				"read off it: a configured-but-unreachable CCU is claimed reachable, and "+
				"nothing reads the gate this early anyway", string(p.Payload))
		}
	}
}
