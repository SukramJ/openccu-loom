// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The daemon answers the CCU's listDevices with an empty array, so the CCU
// re-announces its whole inventory through newDevices after every reconnect.
// These guards pin the two things that makes HandleNewDevices responsible
// for: announcing what is actually new, and staying silent about the rest.

const dedupIface = "c1-HmIP-RF"

// newDevicesFixture is one device root plus one channel, the smallest
// announcement the CCU can send that carries both halves of an address
// family.
func newDevicesFixture() []hmproto.DeviceDescription {
	return []hmproto.DeviceDescription{
		{Address: "ABC0000001", Type: "HmIP-PS"},
		{Address: "ABC0000001:1", Type: "SWITCH_VIRTUAL_RECEIVER", Parent: "ABC0000001"},
	}
}

// TestHandleNewDevicesDedupsReAnnouncement is the reconnect guard: the same
// announcement twice must produce exactly one creation event.
//
// Without the dedup a reconnect published one DeviceCreatedEvent per device
// on the bus dispatch goroutine — the WebSocket lifecycle plane broadcast
// every one of them to every `device.*.lifecycle` subscriber, so a client
// could not tell a genuine pairing from the CCU repeating itself.
func TestHandleNewDevicesDedupsReAnnouncement(t *testing.T) {
	t.Parallel()
	dc, bus := newDC(t)
	got := collectCreated(bus)
	iface := hmtypes.ParseWireInterfaceID(dedupIface)

	dc.HandleNewDevices(context.Background(), iface, newDevicesFixture())
	if len(*got) != 1 {
		t.Fatalf("first announcement: got %d creation events, want 1", len(*got))
	}
	if s := (*got)[0].Source; s != hmenum.SourceOfDeviceCreationNew {
		t.Errorf("first announcement: source = %q, want %q — an address the registry does not hold is a genuine pairing",
			s, hmenum.SourceOfDeviceCreationNew)
	}

	*got = (*got)[:0]
	dc.HandleNewDevices(context.Background(), iface, newDevicesFixture())
	if len(*got) != 0 {
		t.Errorf("re-announcement: got %d creation events, want 0 (sources %v) — "+
			"the CCU repeating its inventory after a reconnect is not news",
			len(*got), sourcesOf(*got))
	}
}

// TestHandleNewDevicesFactoryResetRePairIsRefresh pins the one case in which a
// device the registry already holds is still announced: a factory-reset
// re-pair, where the device keeps its address but rebuilds its channels.
// Nothing else would tell a north-bound consumer that the device it holds is
// no longer the device on the CCU.
func TestHandleNewDevicesFactoryResetRePairIsRefresh(t *testing.T) {
	t.Parallel()
	dc, bus := newDC(t)
	got := collectCreated(bus)
	iface := hmtypes.ParseWireInterfaceID(dedupIface)

	dc.HandleNewDevices(context.Background(), iface, newDevicesFixture())
	*got = (*got)[:0]

	// Same root, a channel address the description cache has never seen.
	rePaired := append(newDevicesFixture(), hmproto.DeviceDescription{
		Address: "ABC0000001:4", Type: "SWITCH_VIRTUAL_RECEIVER", Parent: "ABC0000001",
	})
	dc.HandleNewDevices(context.Background(), iface, rePaired)

	if len(*got) != 1 {
		t.Fatalf("re-pair: got %d creation events, want 1 (sources %v)", len(*got), sourcesOf(*got))
	}
	if s := (*got)[0].Source; s != hmenum.SourceOfDeviceCreationRefresh {
		t.Errorf("re-pair: source = %q, want %q", s, hmenum.SourceOfDeviceCreationRefresh)
	}
}

// TestHandleNewDevicesStoresDescriptionsItDoesNotAnnounce guards the half of
// the dedup that is easy to lose: suppressing the event must not suppress the
// write. A re-announcement can carry an updated description for a device the
// registry already holds, and dropping it would leave the daemon serving a
// stale model until the next restart.
func TestHandleNewDevicesStoresDescriptionsItDoesNotAnnounce(t *testing.T) {
	t.Parallel()
	dc, bus := newDC(t)
	got := collectCreated(bus)
	iface := hmtypes.ParseWireInterfaceID(dedupIface)

	dc.HandleNewDevices(context.Background(), iface, newDevicesFixture())
	*got = (*got)[:0]

	updated := newDevicesFixture()
	updated[0].Firmware = "2.4.8"
	dc.HandleNewDevices(context.Background(), iface, updated)

	if len(*got) != 0 {
		t.Fatalf("got %d creation events, want 0", len(*got))
	}
	desc, ok := dc.descs.Get(iface, "ABC0000001")
	if !ok {
		t.Fatal("description registry lost the device the re-announcement carried")
	}
	if desc.Firmware != "2.4.8" {
		t.Errorf("firmware = %q, want %q — the suppressed event must not suppress the description write",
			desc.Firmware, "2.4.8")
	}
}

// TestHandleAcceptedDevicesAlwaysAnnounces pins the deferred-creation path
// against the dedup: an operator accepting a device out of the inbox is an
// announcement even when the registry already holds the address, because the
// event is the only thing that tells the north-bound surfaces it exists now.
func TestHandleAcceptedDevicesAlwaysAnnounces(t *testing.T) {
	t.Parallel()
	dc, bus := newDC(t)
	got := collectCreated(bus)
	iface := hmtypes.ParseWireInterfaceID(dedupIface)

	dc.HandleAcceptedDevices(iface, newDevicesFixture())
	dc.HandleAcceptedDevices(iface, newDevicesFixture())

	if len(*got) != 2 {
		t.Fatalf("got %d creation events, want 2 (sources %v)", len(*got), sourcesOf(*got))
	}
	for i, e := range *got {
		if e.Source != hmenum.SourceOfDeviceCreationManual {
			t.Errorf("event %d: source = %q, want %q", i, e.Source, hmenum.SourceOfDeviceCreationManual)
		}
	}
}

func sourcesOf(events []hmevent.DeviceCreatedEvent) []hmenum.SourceOfDeviceCreation {
	out := make([]hmenum.SourceOfDeviceCreation, 0, len(events))
	for _, e := range events {
		out = append(out, e.Source)
	}
	return out
}
