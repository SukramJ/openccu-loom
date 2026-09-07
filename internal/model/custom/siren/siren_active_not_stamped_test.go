// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSirenTurnOnDoesNotStampTheActiveSensors pins that ACOUSTIC_ALARM_ACTIVE
// and OPTICAL_ALARM_ACTIVE only ever carry what the device reported. The
// reference derives is_on solely from those sensors and never writes them
// (siren.py CustomDpIpSiren.is_on / turn_on). A selection-less TurnOn on a
// never-commanded HmIP-ASIR resolves to the declared DISABLE_* default, so
// the device stays silent and sends no event; a locally stamped true would
// report a silent siren as on until the next command.
func TestSirenTurnOnDoesNotStampTheActiveSensors(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	s, ch := newWireRig(t, w, custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true})

	if err := s.TurnOn(context.Background(), OnConfig{}, hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("TurnOn: %v", err)
	}
	if active, observed := s.IsActive(); active || observed {
		t.Fatalf("IsActive() after a selection-less TurnOn = (%v, %v); want (false, false) — no device event arrived", active, observed)
	}
	tone := "FREQUENCY_RISING"
	if err := s.TurnOn(context.Background(), OnConfig{AcousticSelection: &tone}, hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("TurnOn(explicit): %v", err)
	}
	if active, observed := s.IsActive(); active || observed {
		t.Fatalf("IsActive() after an explicit TurnOn = (%v, %v); want (false, false) until the device reports", active, observed)
	}

	// Negative control: the device's own event is what flips the state.
	dp, ok := ch.Parameter(hmenum.ParameterAcousticAlarmActive).(*generic.BinarySensor)
	if !ok {
		t.Fatal("rig has no ACOUSTIC_ALARM_ACTIVE binary sensor")
	}
	dp.OnEvent(true)
	if active, observed := s.IsActive(); !active || !observed {
		t.Fatalf("IsActive() after the device reported ACOUSTIC_ALARM_ACTIVE=true = (%v, %v); want (true, true)", active, observed)
	}
}

// TestSirenTurnOffDoesNotStampTheActiveSensors is the TurnOff half of the
// same rule: a stop that the device did not confirm must keep reading as
// sounding — the alarm engine's stop-verify reads IsActive to decide
// whether to escalate, and an optimistic "off" hid a siren that failed to
// silence.
func TestSirenTurnOffDoesNotStampTheActiveSensors(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	s, ch := newWireRig(t, w, custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true})
	dp, ok := ch.Parameter(hmenum.ParameterAcousticAlarmActive).(*generic.BinarySensor)
	if !ok {
		t.Fatal("rig has no ACOUSTIC_ALARM_ACTIVE binary sensor")
	}
	dp.OnEvent(true)
	if err := s.TurnOff(context.Background(), hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("TurnOff: %v", err)
	}
	if active, observed := s.IsActive(); !active || !observed {
		t.Fatalf("IsActive() after TurnOff with no device report = (%v, %v); want (true, true) — the siren has not confirmed it stopped", active, observed)
	}
	dp.OnEvent(false)
	if active, _ := s.IsActive(); active {
		t.Fatal("IsActive() stays true after the device reported ACOUSTIC_ALARM_ACTIVE=false")
	}
}
