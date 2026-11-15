// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// sysvarConfigRetractions counts empty publishes to a sysvar discovery
// config.
func sysvarConfigRetractions(pub *mqtt.NoopClient) int {
	n := 0
	for _, p := range pub.Published() {
		if len(p.Payload) == 0 && strings.HasPrefix(p.Topic, "homeassistant/") && strings.Contains(p.Topic, "sysvar") {
			n++
		}
	}
	return n
}

// restartWithoutTheSysvar declares one sysvar, holds the hub plane as a
// restart does, reloads the hub model without it and rewires the publisher
// against the completed model. observed says whether the reload's sysvar
// fetch succeeded.
func restartWithoutTheSysvar(t *testing.T, observed bool) *mqtt.NoopClient {
	t.Helper()
	c, pub, publisher := hubDiscoveryFixture(t)
	c.SetSystemInformation(central.SystemInfo{Model: "HomeMatic Central", Serial: "3014F711A0001F0123456789"})
	c.HubModel.PutSysvar(&hub.Sysvar{Name: "Anwesenheit", ValueType: hmenum.HubValueTypeLogic})
	c.MarkSouthboundReady()
	publisher.Start(context.Background())
	t.Cleanup(publisher.Stop)
	publisher.Flush()
	if discoveryConfigFor(pub, "homeassistant/", "sysvar") == nil {
		t.Fatalf("precondition: no sysvar config declared; topics=%v", publishedTopics(pub))
	}

	publisher.HoldCentral(c, nil, nil)
	publisher.Flush()
	c.BeginModelTeardown()
	c.HubModel.ClearSysvars()
	if observed {
		c.HubModel.MarkSysvarsObserved()
	}
	c.MarkSouthboundReady()
	publisher.Start(context.Background())
	publisher.Flush()
	return pub
}

// TestAHeldSysvarIsRetractedOnceTheReloadFetchedTheClassEmpty: every system
// variable was deleted on the CCU. The reload's fetch succeeded and found
// none, so the old entity goes — an empty class is a fact here.
func TestAHeldSysvarIsRetractedOnceTheReloadFetchedTheClassEmpty(t *testing.T) {
	t.Parallel()
	if n := sysvarConfigRetractions(restartWithoutTheSysvar(t, true)); n == 0 {
		t.Fatal("the sysvar the CCU no longer has kept its config after a successful empty fetch")
	}
}

// TestAHeldSysvarStaysWhenTheReloadFetchFailed: a failed sysvar load is only
// a warning and leaves the class empty; that proves nothing about the CCU,
// so the held entity stays.
func TestAHeldSysvarStaysWhenTheReloadFetchFailed(t *testing.T) {
	t.Parallel()
	if n := sysvarConfigRetractions(restartWithoutTheSysvar(t, false)); n != 0 {
		t.Fatalf("%d sysvar configs retracted after a failed fetch, want 0", n)
	}
}

// TestClearingTheSysvarsForgetsTheFetch: a cleared hub model has not been
// fetched, whatever the previous load did.
func TestClearingTheSysvarsForgetsTheFetch(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("ccu-01")
	h.MarkSysvarsObserved()
	h.MarkProgramsObserved()
	h.ClearSysvars()
	h.ClearPrograms()
	if h.SysvarsObserved() || h.ProgramsObserved() {
		t.Fatal("a clear kept the observed mark of the class it emptied")
	}
}
