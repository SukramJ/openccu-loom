// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	ch.SetIseID(200)
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

	assignHubChannels(c, nil)

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

	assignHubChannels(c, nil)

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

	assignHubChannels(c, nil)

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

	assignHubChannels(c, nil)
	if fired != 1 {
		t.Fatalf("first call: expected 1 event, got %d", fired)
	}
	if got := sv.Channel(); got != ch.Address {
		t.Fatalf("Channel() = %q, want %q", got, ch.Address)
	}

	assignHubChannels(c, nil)
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

	assignHubChannels(c, nil)
	if got := sv.Channel(); got != ch.Address {
		t.Fatalf("Channel() after initial assignment = %q, want %q", got, ch.Address)
	}
	if fired != 1 {
		t.Fatalf("expected 1 event after initial assignment, got %d", fired)
	}

	if !c.ModelRegistry.Remove("0001ABCD") {
		t.Fatal("Remove: expected device to be present")
	}

	assignHubChannels(c, nil)

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

	assignHubChannels(nil, nil)

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	assignHubChannels(c, nil)
}

// TestAssignHubChannelsExplicitAssignmentWins verifies precedence rule (a):
// a sysvar whose name ALSO matches a device by name-lookup is nevertheless
// linked to the channel the operator explicitly assigned on the CCU
// ("Kanalzuordnung") when that channel is registered on this central.
func TestAssignHubChannelsExplicitAssignmentWins(t *testing.T) {
	t.Parallel()
	c, _, _ := hubChannelAssignmentFixture(t)

	// Second device provides the explicit target channel.
	other := device.New(device.Config{Address: "0002EFGH", IseID: 300})
	otherCh := other.AddChannel("0002EFGH:2", 2, "TYPE", hmenum.ParamsetKeyValues)
	otherCh.SetIseID(400)
	c.ModelRegistry.Put(other)

	// Name matches device 0001ABCD (ise_id 100); explicit points elsewhere.
	sv := hub.NewSysvar("ccu-01", "svEnergy 100", "", hmenum.HubValueTypeString, nil)
	sv.SetExplicitChannel("0002EFGH:2")
	c.HubModel.PutSysvar(sv)

	var fired int
	unsub := events.Subscribe(c.EventBus, func(hmevent.HubChannelsAssignedEvent) {
		fired++
	})
	defer unsub()

	assignHubChannels(c, nil)

	if got := sv.Channel(); got != "0002EFGH:2" {
		t.Fatalf("Channel() = %q, want explicit %q to beat the name match", got, "0002EFGH:2")
	}
	if got := sv.DeviceAddress(); got != "0002EFGH" {
		t.Fatalf("DeviceAddress() = %q, want %q", got, "0002EFGH")
	}
	if fired != 1 {
		t.Fatalf("expected exactly 1 HubChannelsAssignedEvent, got %d", fired)
	}
}

// TestAssignHubChannelsExplicitUnresolvableFallsBackToName verifies
// precedence rule (a)→(b): an explicit assignment referencing a channel this
// central never registered (device filtered out, on another central, or
// removed) must not be trusted — the pass falls through to name matching.
func TestAssignHubChannelsExplicitUnresolvableFallsBackToName(t *testing.T) {
	t.Parallel()
	c, _, ch := hubChannelAssignmentFixture(t)

	sv := hub.NewSysvar("ccu-01", "svEnergy 100", "", hmenum.HubValueTypeString, nil)
	sv.SetExplicitChannel("FFFF0000:9") // not registered anywhere
	c.HubModel.PutSysvar(sv)

	var fired int
	unsub := events.Subscribe(c.EventBus, func(hmevent.HubChannelsAssignedEvent) {
		fired++
	})
	defer unsub()

	assignHubChannels(c, nil)

	if got := sv.Channel(); got != ch.Address {
		t.Fatalf("Channel() = %q, want name-match fallback %q", got, ch.Address)
	}
	if fired != 1 {
		t.Fatalf("expected exactly 1 HubChannelsAssignedEvent, got %d", fired)
	}
}

// TestAssignHubChannelsExplicitChangeRepublishes verifies that a changed
// explicit assignment (e.g. the operator re-assigns the variable in the CCU
// WebUI and the next sysvar refresh carries the new address) moves the link
// and publishes a further HubChannelsAssignedEvent — while an unchanged
// explicit assignment stays silent (idempotency across refresh cycles).
func TestAssignHubChannelsExplicitChangeRepublishes(t *testing.T) {
	t.Parallel()
	c, _, ch := hubChannelAssignmentFixture(t)

	other := device.New(device.Config{Address: "0002EFGH", IseID: 300})
	otherCh := other.AddChannel("0002EFGH:2", 2, "TYPE", hmenum.ParamsetKeyValues)
	otherCh.SetIseID(400)
	c.ModelRegistry.Put(other)

	sv := hub.NewSysvar("ccu-01", "svNoNameHint", "", hmenum.HubValueTypeString, nil)
	sv.SetExplicitChannel(ch.Address)
	c.HubModel.PutSysvar(sv)

	var fired int
	unsub := events.Subscribe(c.EventBus, func(hmevent.HubChannelsAssignedEvent) {
		fired++
	})
	defer unsub()

	assignHubChannels(c, nil)
	if got := sv.Channel(); got != ch.Address {
		t.Fatalf("Channel() = %q, want %q", got, ch.Address)
	}
	if fired != 1 {
		t.Fatalf("expected 1 event after initial assignment, got %d", fired)
	}

	// Idempotent re-run: no change, no event.
	assignHubChannels(c, nil)
	if fired != 1 {
		t.Fatalf("no-op re-run must not publish another event, got %d", fired)
	}

	// Operator re-assigns on the CCU; the refresh stores the new address.
	sv.SetExplicitChannel("0002EFGH:2")
	assignHubChannels(c, nil)
	if got := sv.Channel(); got != "0002EFGH:2" {
		t.Fatalf("Channel() after re-assignment = %q, want %q", got, "0002EFGH:2")
	}
	if fired != 2 {
		t.Fatalf("expected a 2nd event once the explicit assignment moved, got %d", fired)
	}
}

// TestAssignHubChannelsEnergyCounterNameShape pins the real-world CCU
// auto-generated energy-counter name shape
// `svEnergyCounter_<channel_ise_id>_<CHANNELADDRESS>` (field example:
// `svEnergyCounter_14884_000858A994D482:7`). The link resolves via the
// address-suffix rule — the `_`-joined ise_id is deliberately NOT a
// standalone token (word characters bound it), so the suffix match is the
// rule that must hold. Also pins that the name family passes the sysvar
// exclusion filter: operators see these variables in HA today.
func TestAssignHubChannelsEnergyCounterNameShape(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	d := device.New(device.Config{Address: "000858A994D482", IseID: 14880})
	ch := d.AddChannel("000858A994D482:7", 7, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	ch.SetIseID(14884)
	c.ModelRegistry.Put(d)

	const name = "svEnergyCounter_14884_000858A994D482:7"
	if sysvarIsExcluded(name, "1234") {
		t.Fatalf("sysvarIsExcluded(%q) = true, want false — the energy-counter family is user-visible", name)
	}

	sv := hub.NewSysvar("ccu-01", name, "", hmenum.HubValueTypeFloat, nil)
	c.HubModel.PutSysvar(sv)

	assignHubChannels(c, nil)

	if got := sv.Channel(); got != "000858A994D482:7" {
		t.Fatalf("Channel() = %q, want address-suffix match %q", got, "000858A994D482:7")
	}
	if got := sv.DeviceAddress(); got != "000858A994D482" {
		t.Fatalf("DeviceAddress() = %q, want %q", got, "000858A994D482")
	}
}

// TestChannelRegistered exercises the explicit-assignment validation helper
// across the resolvable and unresolvable shapes assignHubChannels feeds it.
func TestChannelRegistered(t *testing.T) {
	t.Parallel()
	c, _, ch := hubChannelAssignmentFixture(t)

	cases := []struct {
		name    string
		address string
		want    bool
	}{
		{"registered channel", ch.Address, true},
		{"device-level address", "0001ABCD", true},
		{"unknown channel index", "0001ABCD:9", false},
		{"unknown device", "FFFF0000:1", false},
		{"empty address", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := channelRegistered(c.ModelRegistry, tc.address); got != tc.want {
				t.Fatalf("channelRegistered(%q) = %v, want %v", tc.address, got, tc.want)
			}
		})
	}
	if channelRegistered(nil, ch.Address) {
		t.Fatal("channelRegistered(nil, …) must be false")
	}
}
