// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// hubChannelAssignmentFixture builds a fresh central.Unit with a single
// registered device ("0001ABCD", ise_id 100) carrying one channel
// ("0001ABCD:1", ise_id 200), ready for [assignHubChannels] test cases.
// Each caller gets its own Unit/EventBus so parallel sub-tests never share
// event-count state.
func hubChannelAssignmentFixture(t *testing.T) (c *central.Unit, d *device.Device, ch *device.Channel) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	d = device.New(device.Config{Address: "0001ABCD", IseID: 100})
	ch = d.AddChannel("0001ABCD:1", 1, "TYPE", hmenum.ParamsetKeyValues)
	ch.IseID = 200
	c.ModelRegistry.Put(d)
	return c, d, ch
}

// TestAssignHubChannelsSysvarDeviceIseIDMatch verifies that a sysvar whose
// legacy_name carries the owning device's ise_id as a standalone token gets
// linked to that device's (lowest-addressed) channel, and that exactly one
// HubChannelsAssignedEvent is published for the change.
func TestAssignHubChannelsSysvarDeviceIseIDMatch(t *testing.T) {
	t.Parallel()
	c, _, ch := hubChannelAssignmentFixture(t)

	sv := hub.NewSysvar("ccu-01", "svEnergy 100", "", hmenum.HubValueTypeString, nil)
	c.HubModel.PutSysvar(sv)

	var fired int
	var gotCentral string
	unsub := events.Subscribe(c.EventBus, func(e hmevent.HubChannelsAssignedEvent) {
		fired++
		gotCentral = e.CentralName
	})
	defer unsub()

	assignHubChannels(c)

	if got := sv.Channel(); got != ch.Address {
		t.Fatalf("Channel() = %q, want %q", got, ch.Address)
	}
	if got := sv.DeviceAddress(); got != "0001ABCD" {
		t.Fatalf("DeviceAddress() = %q, want %q", got, "0001ABCD")
	}
	if fired != 1 {
		t.Fatalf("expected exactly 1 HubChannelsAssignedEvent, got %d", fired)
	}
	if gotCentral != "ccu-01" {
		t.Fatalf("CentralName = %q, want ccu-01", gotCentral)
	}
}

// TestAssignHubChannelsSysvarNoMatch verifies that a sysvar whose
// legacy_name carries no device/channel identifier is left unassociated
// (Channel() stays empty) and that no event fires — there is no change to
// report.
func TestAssignHubChannelsSysvarNoMatch(t *testing.T) {
	t.Parallel()
	c, _, _ := hubChannelAssignmentFixture(t)

	sv := hub.NewSysvar("ccu-01", "svRandomWithoutAnyDeviceHint", "", hmenum.HubValueTypeString, nil)
	c.HubModel.PutSysvar(sv)

	var fired int
	unsub := events.Subscribe(c.EventBus, func(hmevent.HubChannelsAssignedEvent) {
		fired++
	})
	defer unsub()

	assignHubChannels(c)

	if got := sv.Channel(); got != "" {
		t.Fatalf("Channel() = %q, want empty (no match)", got)
	}
	if fired != 0 {
		t.Fatalf("expected no event when nothing changed, got %d", fired)
	}
}

// TestAssignHubChannelsProgramDeviceIseIDMatch mirrors
// TestAssignHubChannelsSysvarDeviceIseIDMatch for programs — the assignment
// pass treats sysvars and programs identically.
func TestAssignHubChannelsProgramDeviceIseIDMatch(t *testing.T) {
	t.Parallel()
	c, _, ch := hubChannelAssignmentFixture(t)

	prog := hub.NewProgram("ccu-01", "PRG_1", "progRun 100", "", false, nil)
	c.HubModel.PutProgram(prog)

	var fired int
	unsub := events.Subscribe(c.EventBus, func(hmevent.HubChannelsAssignedEvent) {
		fired++
	})
	defer unsub()

	assignHubChannels(c)

	if got := prog.Channel(); got != ch.Address {
		t.Fatalf("Channel() = %q, want %q", got, ch.Address)
	}
	if got := prog.DeviceAddress(); got != "0001ABCD" {
		t.Fatalf("DeviceAddress() = %q, want %q", got, "0001ABCD")
	}
	if fired != 1 {
		t.Fatalf("expected exactly 1 HubChannelsAssignedEvent, got %d", fired)
	}
}

// TestAssignHubChannelsIdempotent verifies that a second, no-op invocation
// of assignHubChannels does not publish a further event: the pass is meant
// to be re-run after every device ingest and periodic hub refresh without
// spamming north-bound adapters.
func TestAssignHubChannelsIdempotent(t *testing.T) {
	t.Parallel()
	c, _, ch := hubChannelAssignmentFixture(t)

	sv := hub.NewSysvar("ccu-01", "svEnergy 100", "", hmenum.HubValueTypeString, nil)
	c.HubModel.PutSysvar(sv)

	var fired int
	unsub := events.Subscribe(c.EventBus, func(hmevent.HubChannelsAssignedEvent) {
		fired++
	})
	defer unsub()

	assignHubChannels(c)
	if fired != 1 {
		t.Fatalf("first call: expected 1 event, got %d", fired)
	}
	if got := sv.Channel(); got != ch.Address {
		t.Fatalf("Channel() = %q, want %q", got, ch.Address)
	}

	assignHubChannels(c)
	if fired != 1 {
		t.Fatalf("second (no-op) call must not publish another event, got %d", fired)
	}
	if got := sv.Channel(); got != ch.Address {
		t.Fatalf("Channel() after 2nd call = %q, want %q (must stay stable)", got, ch.Address)
	}
}

// TestAssignHubChannelsClearsOnDeviceRemoval verifies the self-correcting
// half of the assignment pass: once the owning device is removed from the
// registry, a subsequent assignHubChannels pass clears the stale
// association back to "" and publishes a second event for that change.
func TestAssignHubChannelsClearsOnDeviceRemoval(t *testing.T) {
	t.Parallel()
	c, _, ch := hubChannelAssignmentFixture(t)

	sv := hub.NewSysvar("ccu-01", "svEnergy 100", "", hmenum.HubValueTypeString, nil)
	c.HubModel.PutSysvar(sv)

	var fired int
	unsub := events.Subscribe(c.EventBus, func(hmevent.HubChannelsAssignedEvent) {
		fired++
	})
	defer unsub()

	assignHubChannels(c)
	if got := sv.Channel(); got != ch.Address {
		t.Fatalf("Channel() after initial assignment = %q, want %q", got, ch.Address)
	}
	if fired != 1 {
		t.Fatalf("expected 1 event after initial assignment, got %d", fired)
	}

	if !c.ModelRegistry.Remove("0001ABCD") {
		t.Fatal("Remove: expected device to be present")
	}

	assignHubChannels(c)

	if got := sv.Channel(); got != "" {
		t.Fatalf("Channel() after device removal = %q, want empty", got)
	}
	if fired != 2 {
		t.Fatalf("expected a 2nd event once the association clears, got %d", fired)
	}
}

// TestAssignHubChannelsNilSafety verifies that assignHubChannels tolerates a
// nil unit and a unit with no devices/sysvars/programs without panicking —
// the guard clause at the top of the function must catch every partially
// initialised shape it is invoked with (e.g. before HubModel/ModelRegistry
// are wired).
func TestAssignHubChannelsNilSafety(t *testing.T) {
	t.Parallel()

	assignHubChannels(nil)

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	assignHubChannels(c)
}
