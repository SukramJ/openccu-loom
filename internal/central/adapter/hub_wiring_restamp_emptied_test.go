// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/devicedetails"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestRestampDeviceDetailsDropsAnEmptiedAssignment guards the restamp
// against the one direction it used to skip: a device the operator removed
// from its last room or function. The refreshed cache carries no
// assignment for it, and the model must follow — otherwise the stale room
// survives every refresh and the daemon keeps publishing it north-bound.
//
// Negative control: a device whose cache state matches the model is not
// counted as changed, so a restamp that blindly re-set everything would
// fail here too.
func TestRestampDeviceDetailsDropsAnEmptiedAssignment(t *testing.T) {
	t.Parallel()

	reg := registry.NewModelRegistry()
	emptied := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "0001EMPTY", Model: "HmIP-STH", Name: "Flur",
	})
	emptied.SetRooms([]string{"Flur"})
	emptied.SetFunctions([]string{"Klima"})
	reg.Put(emptied)

	stable := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "0001STABL", Model: "HmIP-STH", Name: "Bad",
	})
	stable.SetRooms([]string{"Bad"})
	reg.Put(stable)

	cache := devicedetails.New()
	cache.AddName("0001EMPTY", "Flur")
	cache.AddName("0001STABL", "Bad")
	cache.AddChannelRoom("0001STABL:1", "Bad")

	unit := &central.Unit{ModelRegistry: reg, DeviceDetails: cache}
	changed := restampDeviceDetails(unit, slog.New(slog.DiscardHandler))

	if got := emptied.Rooms(); len(got) != 0 {
		t.Errorf("device removed from its last room still carries %v; the restamp skipped the emptied assignment", got)
	}
	if got := emptied.Functions(); len(got) != 0 {
		t.Errorf("device removed from its last function still carries %v", got)
	}
	if got := stable.Rooms(); len(got) != 1 || got[0] != "Bad" {
		t.Errorf("unchanged device's rooms = %v, want [Bad]", got)
	}
	if changed != 1 {
		t.Errorf("changed = %d, want 1 (only the emptied device)", changed)
	}
}
