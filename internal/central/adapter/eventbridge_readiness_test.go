// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestOnCentralReadinessNilWSHub verifies onCentralReadiness returns early
// without panicking when the bridge has no wsHub wired (mirrors
// TestOnCentralStateNilWSHub for the sibling handler).
func TestOnCentralReadinessNilWSHub(t *testing.T) {
	t.Parallel()
	b := &EventBridge{}
	e := hmevent.CentralReadinessChangedEvent{Base: hmevent.NewBase(), Phase: hmenum.ReadinessReady}
	b.onCentralReadiness("ccu-01", e)
}

// TestOnCentralReadinessPublishesReadyOnlyForReadyPhase verifies that
// onCentralReadiness forwards every phase to the WS hub, but the payload's
// `ready` flag is true only for hmenum.ReadinessReady — every other phase
// (including waiting_for_ccu, loading_hub, loading_devices) must publish
// ready==false so the SPA never shows "ready" mid bring-up.
func TestOnCentralReadinessPublishesReadyOnlyForReadyPhase(t *testing.T) {
	t.Parallel()

	cases := []struct {
		phase     hmenum.ReadinessPhase
		wantReady bool
	}{
		{hmenum.ReadinessUnknown, false},
		{hmenum.ReadinessWaitingForCCU, false},
		{hmenum.ReadinessLoadingHub, false},
		{hmenum.ReadinessLoadingDevices, false},
		{hmenum.ReadinessReady, true},
	}

	for _, c := range cases {
		hub := ws.NewHub()
		b := &EventBridge{wsHub: hub}

		when := time.Now()
		e := hmevent.CentralReadinessChangedEvent{
			Base:             hmevent.NewBaseAt(when),
			CentralName:      "ccu-01",
			Phase:            c.phase,
			InterfacesLoaded: 3,
			InterfacesTotal:  5,
		}
		b.onCentralReadiness("ccu-01", e)

		result := hub.Replay(0, nil)
		if len(result.Events) != 1 {
			t.Fatalf("phase %q: got %d buffered events, want 1", c.phase, len(result.Events))
		}
		ev := result.Events[0]

		wantTopic := ws.CentralReadinessTopic("ccu-01")
		if ev.Topic != wantTopic {
			t.Errorf("phase %q: Topic = %q, want %q", c.phase, ev.Topic, wantTopic)
		}
		if ev.Type != string(hmevent.EventTypeCentralReadinessChanged) {
			t.Errorf("phase %q: Type = %q, want %q", c.phase, ev.Type, hmevent.EventTypeCentralReadinessChanged)
		}

		pl, ok := ev.Payload.(ws.CentralReadinessChangedPayload)
		if !ok {
			t.Fatalf("phase %q: Payload type = %T, want ws.CentralReadinessChangedPayload", c.phase, ev.Payload)
		}
		if pl.Central != "ccu-01" {
			t.Errorf("phase %q: Payload.Central = %q, want %q", c.phase, pl.Central, "ccu-01")
		}
		if pl.Phase != string(c.phase) {
			t.Errorf("phase %q: Payload.Phase = %q, want %q", c.phase, pl.Phase, string(c.phase))
		}
		if pl.Ready != c.wantReady {
			t.Errorf("phase %q: Payload.Ready = %v, want %v", c.phase, pl.Ready, c.wantReady)
		}
		if pl.InterfacesLoaded != 3 || pl.InterfacesTotal != 5 {
			t.Errorf("phase %q: Payload counts = (%d, %d), want (3, 5)", c.phase, pl.InterfacesLoaded, pl.InterfacesTotal)
		}
	}
}

// TestCentralFeaturesChangePushesTheWholeSet pins the WS push through the
// bridge's production subscription list: a feature change on the central's
// bus reaches the hub as one central.features_changed event carrying every
// key, and an unchanged re-set pushes nothing.
func TestCentralFeaturesChangePushesTheWholeSet(t *testing.T) {
	t.Parallel()
	u, err := central.New(central.Config{Name: "ccu-feat"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	hub := ws.NewHub()
	b := &EventBridge{wsHub: hub}
	for _, unsub := range b.subscribeUnit(u) {
		t.Cleanup(unsub)
	}

	set := central.NewFeatures(hmenum.SystemTypeCCU, map[hmenum.Feature]central.FeatureState{
		hmenum.FeatureHubSysvars:   {Available: true},
		hmenum.FeatureTaxonomyTree: {Reason: hmenum.FeatureReasonNotSupported},
	})
	u.SetFeatures(set)
	u.SetFeatures(set)

	var pushes []ws.CentralFeaturesChangedPayload
	for _, ev := range hub.Replay(0, nil).Events {
		if ev.Type != string(hmevent.EventTypeCentralFeaturesChanged) {
			continue
		}
		if ev.Topic != ws.CentralFeaturesTopic("ccu-feat") {
			t.Errorf("topic = %q", ev.Topic)
		}
		pl, ok := ev.Payload.(ws.CentralFeaturesChangedPayload)
		if !ok {
			t.Fatalf("payload type %T", ev.Payload)
		}
		pushes = append(pushes, pl)
	}
	if len(pushes) != 1 {
		t.Fatalf("features pushes = %d, want exactly 1", len(pushes))
	}
	pl := pushes[0]
	if pl.Central != "ccu-feat" || pl.SystemType != "ccu" {
		t.Errorf("payload = %+v", pl)
	}
	if len(pl.Features) != len(hmenum.AllFeatures()) {
		t.Errorf("payload carries %d keys, want every key (%d)", len(pl.Features), len(hmenum.AllFeatures()))
	}
	if s := pl.Features["taxonomy.tree"]; s.Available || s.Reason != "not_supported_by_system" {
		t.Errorf("taxonomy.tree = %+v", s)
	}
	if !pl.Features["hub.sysvars"].Available {
		t.Error("hub.sysvars not available in the push")
	}
}
