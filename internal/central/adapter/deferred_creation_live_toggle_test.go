// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// managerHoldingOneDevice builds a bring-up handle whose central has
// GATE0001 accepted-but-withheld and the deferred-creation toggle on.
func managerHoldingOneDevice(t *testing.T) (*BringUpManager, *central.Unit, hmtypes.WireInterfaceID) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-toggle-live"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	ctx := context.Background()
	iface := hmtypes.ParseWireInterfaceID("ccu-toggle-live-HmIP-RF")

	c.Devices.SetPendingDeviceSink(ctx, newMemorySink())
	c.Devices.StoreDelayedDeviceDescriptions(ctx, iface, gateDescs()[:2])
	_ = c.Devices.TakeDelayedDeviceDescriptions(ctx, iface, "GATE0001")
	p := NewDevicePipeline(c)
	if err := p.Ingest(ctx, string(iface), hmenum.InterfaceHmIPRF, gateDescs()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if c.Devices.IsReleased(iface, "GATE0001") {
		t.Fatal("fixture is wrong: GATE0001 should be withheld")
	}

	h := NewCallbackHandlers(c, nil)
	t.Cleanup(h.Stop)
	h.SetDelayNewDeviceCreation(true)

	m := newBringUpManager()
	m.byCentral = map[string]*centralBringUp{
		"ccu-toggle-live": {
			cc:         config.CentralConfig{Name: "ccu-toggle-live"},
			unit:       c,
			cbHandlers: h,
			logger:     slog.New(slog.DiscardHandler),
		},
	}
	return m, c, iface
}

// TestTurningTheToggleOffAppliesWithoutARestart is the guard for a defect
// the config surface actively hid.
//
// `delay_new_device_creation` is not classified restart-required, so the
// UI reports the edit as applied. It was read at exactly two points, both
// during bring-up, so switching it off changed nothing until a restart —
// and every device already held stayed invisible to the ecosystems with
// no explanation available to the operator.
func TestTurningTheToggleOffAppliesWithoutARestart(t *testing.T) {
	t.Parallel()
	m, c, iface := managerHoldingOneDevice(t)

	var announced []string
	unsub := events.Subscribe(c.EventBus, func(e hmevent.DeviceReleasedEvent) {
		announced = append(announced, e.Address)
	})
	defer unsub()

	off := &config.Config{Centrals: []config.CentralConfig{{
		Name:     "ccu-toggle-live",
		Behavior: config.CentralBehavior{DelayNewDeviceCreation: boolPtr(false)},
	}}}
	if n := m.ApplyDeferredCreationBehavior(context.Background(), off); n != 1 {
		t.Fatalf("applied to %d central(s), want 1", n)
	}

	if !c.Devices.IsReleased(iface, "GATE0001") {
		t.Error("the held device is still withheld after the toggle went off")
	}
	// Announced, because the ecosystems and every connected consumer have
	// no other way to learn the hold ended. A silent release leaves them
	// withholding the device until the next restart — the same defect one
	// layer down.
	if len(announced) != 1 || announced[0] != "GATE0001" {
		t.Errorf("release events = %v, want exactly GATE0001", announced)
	}
}

// TestTurningTheToggleOnDoesNotReleaseAnything is the negative control:
// the apply must be a no-op in the other direction, and must not release
// on an unchanged config either. Without it the test above would pass on
// an implementation that released on every reload.
func TestTurningTheToggleOnDoesNotReleaseAnything(t *testing.T) {
	t.Parallel()
	m, c, iface := managerHoldingOneDevice(t)

	on := &config.Config{Centrals: []config.CentralConfig{{
		Name:     "ccu-toggle-live",
		Behavior: config.CentralBehavior{DelayNewDeviceCreation: boolPtr(true)},
	}}}
	if n := m.ApplyDeferredCreationBehavior(context.Background(), on); n != 0 {
		t.Errorf("applied to %d central(s) on an unchanged setting, want 0", n)
	}
	if c.Devices.IsReleased(iface, "GATE0001") {
		t.Error("an unchanged toggle released a held device")
	}
}

func boolPtr(b bool) *bool { return &b }
