// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestIncidentOpenedTheSameWayByEveryEntryPath pins the incident-opening
// rule across all three of its entry paths: an intrusion trigger, the
// adoption of an already-sounding siren, and an always-on (hazard/panic)
// activation. Each must record the cause and derive the same bounded
// trigger deadline (now + the mode's trigger duration) — the restore
// path reads that deadline back as authoritative, so a drift changes how
// long a restored alarm sounds.
func TestIncidentOpenedTheSameWayByEveryEntryPath(t *testing.T) {
	// The zone's full mode bounds a trigger at 60s.
	const triggerWindow = 60 * time.Second

	cases := []struct {
		name string
		open func(h *harness)
	}{
		{
			name: "intrusion trigger",
			open: func(h *harness) {
				h.eng.HandleSensorEvent(h.ctx, "window", true)
			},
		},
		{
			name: "adopted sounding siren",
			open: func(h *harness) {
				if _, err := h.eng.AdoptSounding(h.ctx, "eg", []string{"sir1"}); err != nil {
					t.Fatalf("AdoptSounding: %v", err)
				}
			},
		},
		{
			name: "always-on panic",
			open: func(h *harness) {
				if err := h.eng.PanicTrigger(h.ctx, "eg", false, "tester", "test"); err != nil {
					t.Fatalf("PanicTrigger: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.seedZone("eg", "Erdgeschoss", defaultZoneConfig())
			h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
				Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
			})
			h.start()
			h.armFull()
			openedAt := h.clk.Now()

			tc.open(h)
			h.wantState("eg", hmenum.AlarmZoneStateTriggered)

			inc, ok := h.openIncident("eg")
			if !ok {
				t.Fatal("no open incident after the trigger")
			}
			if inc.CauseJSON == "" {
				t.Error("incident carries no cause document")
			}
			want := openedAt.Add(triggerWindow).UnixMilli()
			if inc.TriggerDeadlineMS != want {
				t.Errorf("trigger deadline = %d, want %d (opened_at + the mode's %s trigger window)",
					inc.TriggerDeadlineMS, want, triggerWindow)
			}
		})
	}
}
