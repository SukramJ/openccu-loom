// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

// Deep tests for DeviceCoordinator and DeviceRegistry covering the
// pipelines and diff cases not exercised by device_pull_test.go and
// coordinators_test.go.
//
// Existing test inventory (must not duplicate):
// device_pull_test.go — TestInitialPullCreatesDevicesAndChannels,
// TestInitialPullIsIdempotent,
// TestInitialPullDetectsModelChange,
// TestInitialPullEmitsRemovedForVanishedDevices,
// TestInitialPullSurfacesListerError,
// TestInitialPullNilLister,
// TestRefreshAfterPairReusesPullPath,
// TestRefreshAfterUnpairDropsDevice,
// TestSameDescriptionEdgeCases
// coordinators_test.go — TestDeviceCoordinatorChecksForNewDeviceAddresses,
// TestDeviceCoordinatorRegistersAndRemoves

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newDCFull builds a DeviceCoordinator and returns the underlying
// registries so tests can inspect state directly.
func newDCFull(t *testing.T) (*DeviceCoordinator, *events.Bus, *registry.DeviceRegistry, *registry.DeviceDescriptionRegistry, *registry.ParamsetRegistry) {
	t.Helper()
	bus := events.NewBus()
	devs := registry.NewDeviceRegistry()
	descs := registry.NewDeviceDescriptionRegistry()
	psets := registry.NewParamsetRegistry()
	dc := NewDeviceCoordinator("c1", bus, devs, descs, psets, nil)
	return dc, bus, devs, descs, psets
}

// device builds a top-level DeviceDescription (no Parent → IsDevice).
func device(addr, model, firmware string, children ...string) hmproto.DeviceDescription {
	return hmproto.DeviceDescription{
		Address:  addr,
		Type:     model,
		Firmware: firmware,
		Children: children,
	}
}

// channel builds a child DeviceDescription (Parent set → IsChannel).
func channel(addr, parent, model string) hmproto.DeviceDescription {
	return hmproto.DeviceDescription{
		Address: addr,
		Parent:  parent,
		Type:    model,
	}
}

// listerOf returns a stubLister with the provided snapshot.
func listerOf(descs ...hmproto.DeviceDescription) *stubLister {
	return &stubLister{snapshot: descs}
}

// collectCreated subscribes and collects DeviceCreatedEvents.
func collectCreated(bus *events.Bus) *[]hmevent.DeviceCreatedEvent {
	out := make([]hmevent.DeviceCreatedEvent, 0, 8)
	events.Subscribe(bus, func(e hmevent.DeviceCreatedEvent) { out = append(out, e) })
	return &out
}

// collectRemoved subscribes and collects DeviceRemovedEvents.
func collectRemoved(bus *events.Bus) *[]hmevent.DeviceRemovedEvent {
	out := make([]hmevent.DeviceRemovedEvent, 0, 8)
	events.Subscribe(bus, func(e hmevent.DeviceRemovedEvent) { out = append(out, e) })
	return &out
}

// sortedAddresses extracts addresses from a DeviceCreatedEvent slice,
// sorted for deterministic comparison.
func sortedCreatedAddrs(evs []hmevent.DeviceCreatedEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Address
	}
	sort.Strings(out)
	return out
}

// sortedRemovedAddrs does the same for DeviceRemovedEvent.
func sortedRemovedAddrs(evs []hmevent.DeviceRemovedEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Address
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// 1. DetectsAddedDevice: first pull [A]; second pull [A, B] → PullReport.Created has B.
// ---------------------------------------------------------------------------

func TestInitialPullDetectsAddedDevice(t *testing.T) {
	t.Parallel()
	dc, bus, devs, _, _ := newDCFull(t)
	created := collectCreated(bus)

	if _, err := dc.InitialPull(context.Background(), listerOf(
		device("AA", "HmIP-X", "1.0"),
	), hmenum.InterfaceHmIPRF); err != nil {
		t.Fatal(err)
	}
	if devs.Len() != 1 {
		t.Fatalf("after first pull devs=%d, want 1", devs.Len())
	}

	rep, err := dc.InitialPull(context.Background(), listerOf(
		device("AA", "HmIP-X", "1.0"),
		device("BB", "HmIP-Y", "2.0"),
	), hmenum.InterfaceHmIPRF)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Created != 1 || rep.Updated != 0 || rep.Removed != 0 {
		t.Fatalf("rep=%+v, want Created=1 Updated=0 Removed=0", rep)
	}
	if devs.Len() != 2 {
		t.Fatalf("after second pull devs=%d, want 2", devs.Len())
	}
	addrs := sortedCreatedAddrs(*created)
	if len(addrs) != 2 || addrs[0] != "AA" || addrs[1] != "BB" {
		t.Fatalf("created events addresses=%v, want [AA BB]", addrs)
	}
}

// ---------------------------------------------------------------------------
// 2. CombinedDiff: first pull [A, B, C]; second pull [A, B*, D].
// C removed, D added, B firmware-bumped → all three categories in one report.
// ---------------------------------------------------------------------------

func TestInitialPullCombinedDiff(t *testing.T) {
	t.Parallel()
	dc, _, devs, _, _ := newDCFull(t)

	if _, err := dc.InitialPull(context.Background(), listerOf(
		device("AA", "HmIP-X", "1.0"),
		device("BB", "HmIP-Y", "1.0"),
		device("CC", "HmIP-Z", "1.0"),
	), hmenum.InterfaceHmIPRF); err != nil {
		t.Fatal(err)
	}
	if devs.Len() != 3 {
		t.Fatalf("after first pull devs=%d, want 3", devs.Len())
	}

	// B firmware bump, C removed, D added.
	rep, err := dc.InitialPull(context.Background(), listerOf(
		device("AA", "HmIP-X", "1.0"),
		device("BB", "HmIP-Y", "2.0"), // firmware bumped
		device("DD", "HmIP-W", "1.0"), // new
	), hmenum.InterfaceHmIPRF)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Created != 1 {
		t.Errorf("Created=%d, want 1", rep.Created)
	}
	if rep.Updated != 1 {
		t.Errorf("Updated=%d, want 1", rep.Updated)
	}
	if rep.Removed != 1 {
		t.Errorf("Removed=%d, want 1", rep.Removed)
	}
	// Registry should contain AA, BB, DD (CC gone).
	if devs.Len() != 3 {
		t.Fatalf("devs=%d after combined diff, want 3", devs.Len())
	}
	if _, ok := devs.Get(hmenum.InterfaceHmIPRF, "CC"); ok {
		t.Fatal("CC should have been removed from DeviceRegistry")
	}
	if _, ok := devs.Get(hmenum.InterfaceHmIPRF, "DD"); !ok {
		t.Fatal("DD should be in DeviceRegistry after add")
	}
}

// ---------------------------------------------------------------------------
// 3. PullReport carries the correct interface field.
// ---------------------------------------------------------------------------

func TestInitialPullReportInterfaceField(t *testing.T) {
	t.Parallel()
	dc, _, _, _, _ := newDCFull(t)

	rep, err := dc.InitialPull(context.Background(), listerOf(
		device("AA", "HmIP-X", "1.0"),
	), hmenum.InterfaceBidCosRF)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Interface != hmenum.InterfaceBidCosRF {
		t.Fatalf("PullReport.Interface=%v, want %v", rep.Interface, hmenum.InterfaceBidCosRF)
	}
}

// ---------------------------------------------------------------------------
// 4. EmptyResponseClearsRegistry: registry has 5 devices; second pull returns
// []; PullReport.Removed=5; registry is empty.
// ---------------------------------------------------------------------------

func TestInitialPullEmptyResponseClearsRegistry(t *testing.T) {
	t.Parallel()
	dc, bus, devs, _, _ := newDCFull(t)
	removed := collectRemoved(bus)

	full := listerOf(
		device("A1", "HmIP-X", "1.0"),
		device("A2", "HmIP-X", "1.0"),
		device("A3", "HmIP-X", "1.0"),
		device("A4", "HmIP-X", "1.0"),
		device("A5", "HmIP-X", "1.0"),
	)
	if _, err := dc.InitialPull(context.Background(), full, hmenum.InterfaceHmIPRF); err != nil {
		t.Fatal(err)
	}
	if devs.Len() != 5 {
		t.Fatalf("seeded devs=%d, want 5", devs.Len())
	}

	rep, err := dc.InitialPull(context.Background(), listerOf(), hmenum.InterfaceHmIPRF)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Removed != 5 {
		t.Fatalf("Removed=%d, want 5", rep.Removed)
	}
	if rep.Created != 0 || rep.Updated != 0 {
		t.Fatalf("Created/Updated must be 0, got %+v", rep)
	}
	if devs.Len() != 0 {
		t.Fatalf("devs=%d after empty pull, want 0", devs.Len())
	}
	if len(*removed) != 5 {
		t.Fatalf("DeviceRemovedEvents=%d, want 5", len(*removed))
	}
}

// ---------------------------------------------------------------------------
// 5. Multi-interface isolation: pulls on different interfaces do not cross-contaminate.
// ---------------------------------------------------------------------------

func TestInitialPullMultiInterfaceIsolation(t *testing.T) {
	t.Parallel()
	dc, _, devs, _, _ := newDCFull(t)
	ctx := context.Background()

	// Seed HmIP-RF with device A.
	if _, err := dc.InitialPull(ctx, listerOf(
		device("AA", "HmIP-X", "1.0"),
	), hmenum.InterfaceHmIPRF); err != nil {
		t.Fatal(err)
	}
	// Seed BidCos-RF with device B.
	if _, err := dc.InitialPull(ctx, listerOf(
		device("BB", "HmIP-Y", "1.0"),
	), hmenum.InterfaceBidCosRF); err != nil {
		t.Fatal(err)
	}
	if devs.Len() != 2 {
		t.Fatalf("total devs=%d, want 2", devs.Len())
	}

	// Remove all from HmIP-RF: B on BidCos-RF must survive.
	rep, err := dc.InitialPull(ctx, listerOf(), hmenum.InterfaceHmIPRF)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Removed != 1 {
		t.Fatalf("Removed=%d after clearing HmIP-RF, want 1", rep.Removed)
	}
	if devs.Len() != 1 {
		t.Fatalf("devs=%d after HmIP-RF cleared, want 1 (BidCos-RF device)", devs.Len())
	}
	if _, ok := devs.Get(hmenum.InterfaceBidCosRF, "BB"); !ok {
		t.Fatal("BB on BidCos-RF must not be removed when HmIP-RF is cleared")
	}
}

// ---------------------------------------------------------------------------
// 6. RefreshAfterPair adds a new device alongside an existing one.
// ---------------------------------------------------------------------------

func TestRefreshAfterPairAddsSingleDevice(t *testing.T) {
	t.Parallel()
	dc, bus, devs, _, _ := newDCFull(t)
	created := collectCreated(bus)
	ctx := context.Background()

	// Seed the registry with one existing device.
	if _, err := dc.InitialPull(ctx, listerOf(
		device("OLD", "HmIP-X", "1.0"),
	), hmenum.InterfaceHmIPRF); err != nil {
		t.Fatal(err)
	}
	// RefreshAfterPair with lister that includes both old and new.
	rep, err := dc.RefreshAfterPair(ctx, listerOf(
		device("OLD", "HmIP-X", "1.0"),
		device("NEW", "HmIP-Y", "2.0"),
	), hmenum.InterfaceHmIPRF)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Created != 1 || rep.Updated != 0 || rep.Removed != 0 {
		t.Fatalf("rep=%+v, want Created=1 Updated=0 Removed=0", rep)
	}
	if devs.Len() != 2 {
		t.Fatalf("devs=%d after pair, want 2", devs.Len())
	}
	if _, ok := devs.Get(hmenum.InterfaceHmIPRF, "NEW"); !ok {
		t.Fatal("NEW device not in registry after RefreshAfterPair")
	}
	if _, ok := devs.Get(hmenum.InterfaceHmIPRF, "OLD"); !ok {
		t.Fatal("OLD device missing after RefreshAfterPair")
	}
	// Only NEW should fire a DeviceCreatedEvent (OLD was already known).
	createdAddrs := sortedCreatedAddrs(*created)
	if len(createdAddrs) != 2 || createdAddrs[0] != "NEW" || createdAddrs[1] != "OLD" {
		t.Fatalf("created events=%v, want [NEW OLD]", createdAddrs)
	}
}

// ---------------------------------------------------------------------------
// 7. RefreshAfterUnpair: lister still has the remaining device; target gone.
// ---------------------------------------------------------------------------

func TestRefreshAfterUnpairRemovesSingleDevice(t *testing.T) {
	t.Parallel()
	dc, bus, devs, descs, _ := newDCFull(t)
	removed := collectRemoved(bus)
	ctx := context.Background()

	if _, err := dc.InitialPull(ctx, listerOf(
		device("KEEP", "HmIP-X", "1.0"),
		device("DROP", "HmIP-Y", "1.0"),
	), hmenum.InterfaceHmIPRF); err != nil {
		t.Fatal(err)
	}
	if devs.Len() != 2 {
		t.Fatalf("seeded devs=%d, want 2", devs.Len())
	}

	wasRemoved := dc.RefreshAfterUnpair(ctx, hmenum.InterfaceHmIPRF, "DROP")
	if !wasRemoved {
		t.Fatal("RefreshAfterUnpair must return true for a known device")
	}
	if devs.Len() != 1 {
		t.Fatalf("devs=%d after unpair, want 1", devs.Len())
	}
	if _, ok := devs.Get(hmenum.InterfaceHmIPRF, "DROP"); ok {
		t.Fatal("DROP still in DeviceRegistry after unpair")
	}
	if _, ok := descs.Get(hmenum.InterfaceHmIPRF, "DROP"); ok {
		t.Fatal("DROP still in DeviceDescriptionRegistry after unpair")
	}
	if _, ok := devs.Get(hmenum.InterfaceHmIPRF, "KEEP"); !ok {
		t.Fatal("KEEP wrongly removed by RefreshAfterUnpair")
	}
	if len(*removed) != 1 || (*removed)[0].Address != "DROP" {
		t.Fatalf("removed events=%v, want single DROP", sortedRemovedAddrs(*removed))
	}
}

// ---------------------------------------------------------------------------
// 8. sameDescription: Type change triggers update.
// ---------------------------------------------------------------------------

func TestSameDescriptionDifferentTypeMeansUpdate(t *testing.T) {
	t.Parallel()
	a := hmproto.DeviceDescription{Address: "X", Type: "HmIP-A", Firmware: "1.0"}
	b := hmproto.DeviceDescription{Address: "X", Type: "HmIP-B", Firmware: "1.0"}
	if sameDescription(a, b) {
		t.Fatal("different Type must compare as not same")
	}

	dc, _, _, _, _ := newDCFull(t)
	if _, err := dc.InitialPull(context.Background(), listerOf(a), hmenum.InterfaceHmIPRF); err != nil {
		t.Fatal(err)
	}
	rep, err := dc.InitialPull(context.Background(), listerOf(b), hmenum.InterfaceHmIPRF)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Updated != 1 {
		t.Fatalf("Updated=%d for type change, want 1", rep.Updated)
	}
}

// ---------------------------------------------------------------------------
// 9. sameDescription: Parent change triggers update (channel side).
// ---------------------------------------------------------------------------

func TestSameDescriptionDifferentParentMeansUpdate(t *testing.T) {
	t.Parallel()
	// Two channels at same address, different parent (unusual but must still diff).
	a := hmproto.DeviceDescription{Address: "X:1", Parent: "X", Type: "CH", Firmware: "1.0"}
	b := hmproto.DeviceDescription{Address: "X:1", Parent: "Y", Type: "CH", Firmware: "1.0"}
	if sameDescription(a, b) {
		t.Fatal("different Parent must compare as not same")
	}
}

// ---------------------------------------------------------------------------
// 10. sameDescription: Different children order is treated as a change.
// (sameDescription does index-by-index comparison.)
// ---------------------------------------------------------------------------

func TestSameDescriptionDifferentChildrenOrderMeansUpdate(t *testing.T) {
	t.Parallel()
	a := hmproto.DeviceDescription{
		Address:  "X",
		Type:     "HmIP-A",
		Firmware: "1.0",
		Children: []string{"X:0", "X:1"},
	}
	b := hmproto.DeviceDescription{
		Address:  "X",
		Type:     "HmIP-A",
		Firmware: "1.0",
		Children: []string{"X:1", "X:0"}, // reversed
	}
	if sameDescription(a, b) {
		t.Fatal("different children order must compare as not same")
	}

	dc, _, _, _, _ := newDCFull(t)
	if _, err := dc.InitialPull(context.Background(), listerOf(a), hmenum.InterfaceHmIPRF); err != nil {
		t.Fatal(err)
	}
	rep, err := dc.InitialPull(context.Background(), listerOf(b), hmenum.InterfaceHmIPRF)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Updated != 1 {
		t.Fatalf("Updated=%d for reversed children, want 1", rep.Updated)
	}
}

// ---------------------------------------------------------------------------
// 11. Channel description changes are counted as Updated in PullReport
// even though channels do not appear in DeviceRegistry.
// ---------------------------------------------------------------------------

func TestInitialPullChannelDescUpdateCountedSeparately(t *testing.T) {
	t.Parallel()
	dc, _, devs, _, _ := newDCFull(t)
	ctx := context.Background()

	// Seed: parent device + child channel.
	if _, err := dc.InitialPull(ctx, listerOf(
		device("AA", "HmIP-X", "1.0", "AA:0"),
		channel("AA:0", "AA", "MAINTENANCE"),
	), hmenum.InterfaceHmIPRF); err != nil {
		t.Fatal(err)
	}
	if devs.Len() != 1 {
		t.Fatalf("only parent in DeviceRegistry, got %d", devs.Len())
	}

	// Second pull: channel type changed; device unchanged.
	rep, err := dc.InitialPull(ctx, listerOf(
		device("AA", "HmIP-X", "1.0", "AA:0"),
		channel("AA:0", "AA", "NEW_CHANNEL_TYPE"),
	), hmenum.InterfaceHmIPRF)
	if err != nil {
		t.Fatal(err)
	}
	// The channel update is counted in rep.Updated; device is unchanged.
	if rep.Updated != 1 {
		t.Fatalf("Updated=%d for channel type change, want 1", rep.Updated)
	}
	if rep.Created != 0 || rep.Removed != 0 {
		t.Fatalf("Created/Removed must be 0, got %+v", rep)
	}
}

// ---------------------------------------------------------------------------
// 12. HandleNewDevices: only top-level entries fire DeviceCreatedEvent;
// channels are stored in descs but not in DeviceRegistry.
// ---------------------------------------------------------------------------

func TestHandleNewDevicesTopLevelVsChannel(t *testing.T) {
	t.Parallel()
	dc, bus, devs, descs, _ := newDCFull(t)
	created := collectCreated(bus)

	dc.HandleNewDevices(context.Background(), hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		device("AA", "HmIP-X", "1.0", "AA:0", "AA:1"),
		channel("AA:0", "AA", "MAINTENANCE"),
		channel("AA:1", "AA", "CLIMATECONTROL"),
	})

	if devs.Len() != 1 {
		t.Fatalf("DeviceRegistry len=%d, want 1 (only top-level)", devs.Len())
	}
	if descs.Len() != 3 {
		t.Fatalf("DescriptionRegistry len=%d, want 3 (device + 2 channels)", descs.Len())
	}
	if len(*created) != 1 || (*created)[0].Address != "AA" {
		t.Fatalf("created events=%+v, want single AA event", *created)
	}
}

// ---------------------------------------------------------------------------
// 13. HandleDeleteDevices removes entries and fires events.
// ---------------------------------------------------------------------------

func TestHandleDeleteDevicesEmitsEvents(t *testing.T) {
	t.Parallel()
	dc, bus, devs, descs, _ := newDCFull(t)
	removed := collectRemoved(bus)
	ctx := context.Background()

	dc.HandleNewDevices(ctx, hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		device("AA", "HmIP-X", "1.0", "AA:0"),
		channel("AA:0", "AA", "MAINTENANCE"),
		device("BB", "HmIP-Y", "1.0"),
	})
	if devs.Len() != 2 {
		t.Fatalf("seeded devs=%d, want 2", devs.Len())
	}

	dc.HandleDeleteDevices(ctx, hmenum.InterfaceHmIPRF, []string{"AA", "AA:0"})

	if devs.Len() != 1 {
		t.Fatalf("devs=%d after delete AA, want 1 (BB remains)", devs.Len())
	}
	if _, ok := descs.Get(hmenum.InterfaceHmIPRF, "AA"); ok {
		t.Fatal("AA description should be deleted")
	}
	if _, ok := descs.Get(hmenum.InterfaceHmIPRF, "AA:0"); ok {
		t.Fatal("AA:0 description should be deleted")
	}
	// Only the top-level device emits a DeviceRemovedEvent; channel
	// address AA:0 is not in DeviceRegistry so Remove returns false.
	if len(*removed) != 1 || (*removed)[0].Address != "AA" {
		t.Fatalf("removed events=%v, want [AA]", sortedRemovedAddrs(*removed))
	}
}

// ---------------------------------------------------------------------------
// 14. HandleDeleteDevices is idempotent: deleting an unknown address is a no-op.
// ---------------------------------------------------------------------------

func TestHandleDeleteDevicesUnknownAddressIsNoop(t *testing.T) {
	t.Parallel()
	dc, bus, _, _, _ := newDCFull(t)
	var count atomic.Int32
	events.Subscribe(bus, func(_ hmevent.DeviceRemovedEvent) { count.Add(1) })

	dc.HandleDeleteDevices(context.Background(), hmenum.InterfaceHmIPRF, []string{"GHOST"})
	if count.Load() != 0 {
		t.Fatal("deleting unknown address must not emit DeviceRemovedEvent")
	}
}

// ---------------------------------------------------------------------------
// 15. CheckForNewDeviceAddresses: empty registry → entire snapshot is new.
// ---------------------------------------------------------------------------

func TestCheckForNewDeviceAddressesAllNewOnEmptyRegistry(t *testing.T) {
	t.Parallel()
	dc, _, _, _, _ := newDCFull(t)

	snapshot := []hmproto.DeviceDescription{
		device("AA", "HmIP-X", "1.0"),
		channel("AA:0", "AA", "MAINTENANCE"),
	}
	got := dc.CheckForNewDeviceAddresses(hmenum.InterfaceHmIPRF, snapshot)
	if len(got) != 2 {
		t.Fatalf("all-new snapshot: got %v, want [AA AA:0]", got)
	}
	if got[0] != "AA" || got[1] != "AA:0" {
		t.Fatalf("order mismatch: got %v", got)
	}
}

// ---------------------------------------------------------------------------
// 16. CheckForNewDeviceAddresses: all-known snapshot → empty result.
// ---------------------------------------------------------------------------

func TestCheckForNewDeviceAddressesAllKnownReturnsEmpty(t *testing.T) {
	t.Parallel()
	dc, _, _, _, _ := newDCFull(t)
	ctx := context.Background()

	if _, err := dc.InitialPull(ctx, listerOf(
		device("AA", "HmIP-X", "1.0"),
		channel("AA:0", "AA", "MAINTENANCE"),
	), hmenum.InterfaceHmIPRF); err != nil {
		t.Fatal(err)
	}
	snapshot := []hmproto.DeviceDescription{
		device("AA", "HmIP-X", "1.0"),
		channel("AA:0", "AA", "MAINTENANCE"),
	}
	got := dc.CheckForNewDeviceAddresses(hmenum.InterfaceHmIPRF, snapshot)
	if len(got) != 0 {
		t.Fatalf("all-known snapshot: got %v, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// 17. DeviceRegistry.Clear wipes all entries.
// ---------------------------------------------------------------------------

func TestDeviceRegistryClear(t *testing.T) {
	t.Parallel()
	r := registry.NewDeviceRegistry()
	r.Put(registry.DeviceEntry{Interface: hmenum.InterfaceHmIPRF, Address: "A"})
	r.Put(registry.DeviceEntry{Interface: hmenum.InterfaceHmIPRF, Address: "B"})
	r.Put(registry.DeviceEntry{Interface: hmenum.InterfaceBidCosRF, Address: "C"})
	if r.Len() != 3 {
		t.Fatalf("before Clear: len=%d want 3", r.Len())
	}
	r.Clear()
	if r.Len() != 0 {
		t.Fatalf("after Clear: len=%d want 0", r.Len())
	}
	if _, ok := r.Get(hmenum.InterfaceHmIPRF, "A"); ok {
		t.Fatal("entry A survived Clear")
	}
}

// ---------------------------------------------------------------------------
// 18. DeviceRegistry.Remove on missing address returns false (idempotent).
// ---------------------------------------------------------------------------

func TestDeviceRegistryRemoveIdempotent(t *testing.T) {
	t.Parallel()
	r := registry.NewDeviceRegistry()
	r.Put(registry.DeviceEntry{Interface: hmenum.InterfaceHmIPRF, Address: "A"})
	if !r.Remove(hmenum.InterfaceHmIPRF, "A") {
		t.Fatal("Remove existing must return true")
	}
	if r.Remove(hmenum.InterfaceHmIPRF, "A") {
		t.Fatal("Remove missing must return false")
	}
	if r.Remove(hmenum.InterfaceHmIPRF, "GHOST") {
		t.Fatal("Remove never-existing must return false")
	}
}

// ---------------------------------------------------------------------------
// 19. DeviceRegistry.List returns entries sorted by (interface, address).
// ---------------------------------------------------------------------------

func TestDeviceRegistryListSortOrder(t *testing.T) {
	t.Parallel()
	r := registry.NewDeviceRegistry()
	r.Put(registry.DeviceEntry{Interface: hmenum.InterfaceHmIPRF, Address: "ZZZ"})
	r.Put(registry.DeviceEntry{Interface: hmenum.InterfaceHmIPRF, Address: "AAA"})
	r.Put(registry.DeviceEntry{Interface: hmenum.InterfaceBidCosRF, Address: "MMM"})

	list := r.List()
	if len(list) != 3 {
		t.Fatalf("List len=%d", len(list))
	}
	// Confirm sorted: interface first, then address.
	for i := 1; i < len(list); i++ {
		prev, cur := list[i-1], list[i]
		if cur.Interface < prev.Interface {
			t.Fatalf("List not sorted by interface: %s > %s", prev.Interface, cur.Interface)
		}
		if cur.Interface == prev.Interface && cur.Address < prev.Address {
			t.Fatalf("List not sorted by address within interface: %s > %s", prev.Address, cur.Address)
		}
	}
}

// ---------------------------------------------------------------------------
// 20. Concurrent RefreshAfterUnpair calls on the same address: exactly one
// returns true; subsequent ones return false. No data race.
// ---------------------------------------------------------------------------

func TestConcurrentRefreshAfterUnpairExactlyOneWins(t *testing.T) {
	t.Parallel()
	dc, _, _, _, _ := newDCFull(t)
	ctx := context.Background()

	if _, err := dc.InitialPull(ctx, listerOf(
		device("AA", "HmIP-X", "1.0"),
	), hmenum.InterfaceHmIPRF); err != nil {
		t.Fatal(err)
	}

	const n = 10
	results := make([]bool, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		i := i
		go func() {
			defer wg.Done()
			results[i] = dc.RefreshAfterUnpair(ctx, hmenum.InterfaceHmIPRF, "AA")
		}()
	}
	wg.Wait()

	trueCount := 0
	for _, v := range results {
		if v {
			trueCount++
		}
	}
	if trueCount != 1 {
		t.Fatalf("exactly one concurrent RefreshAfterUnpair must return true, got %d", trueCount)
	}
}

// ---------------------------------------------------------------------------
// 21. PullReport stable under list-order variation: set of created addresses
// equals the input regardless of snapshot order.
// ---------------------------------------------------------------------------

func TestInitialPullPullReportStableUnderOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	makeDescs := func(order []string) []hmproto.DeviceDescription {
		out := make([]hmproto.DeviceDescription, len(order))
		for i, addr := range order {
			out[i] = device(addr, "HmIP-X", "1.0")
		}
		return out
	}

	run := func(order []string) map[string]bool {
		dc, bus, _, _, _ := newDCFull(t)
		created := collectCreated(bus)
		if _, err := dc.InitialPull(ctx, listerOf(makeDescs(order)...), hmenum.InterfaceHmIPRF); err != nil {
			t.Fatalf("pull err=%v", err)
		}
		set := make(map[string]bool, len(*created))
		for _, e := range *created {
			set[e.Address] = true
		}
		return set
	}

	forward := run([]string{"A1", "A2", "A3"})
	backward := run([]string{"A3", "A2", "A1"})

	for addr := range forward {
		if !backward[addr] {
			t.Errorf("address %q in forward run missing from backward run", addr)
		}
	}
	for addr := range backward {
		if !forward[addr] {
			t.Errorf("address %q in backward run missing from forward run", addr)
		}
	}
	if len(forward) != len(backward) {
		t.Fatalf("set sizes differ: forward=%d backward=%d", len(forward), len(backward))
	}
}

// ---------------------------------------------------------------------------
// 22. DeviceDescriptionRegistry: All() returns only the requested interface.
// ---------------------------------------------------------------------------

func TestDeviceDescriptionRegistryAllIsolation(t *testing.T) {
	t.Parallel()
	r := registry.NewDeviceDescriptionRegistry()
	r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "A"})
	r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "B"})
	r.Put(hmenum.InterfaceBidCosRF, hmproto.DeviceDescription{Address: "C"})

	hmip := r.All(hmenum.InterfaceHmIPRF)
	if len(hmip) != 2 {
		t.Fatalf("All(HmIP-RF)=%d, want 2", len(hmip))
	}
	bidcos := r.All(hmenum.InterfaceBidCosRF)
	if len(bidcos) != 1 || bidcos[0].Address != "C" {
		t.Fatalf("All(BidCos-RF)=%v, want [C]", bidcos)
	}
	// Unknown interface → empty.
	unknown := r.All(hmenum.InterfaceCUxD)
	if len(unknown) != 0 {
		t.Fatalf("All(CUxD)=%v, want empty", unknown)
	}
}

// ---------------------------------------------------------------------------
// HandleNewDevices emits DataFetchCompletedEvent for cache persistence.
// ---------------------------------------------------------------------------

func TestHandleNewDevicesEmitsDataFetchCompletedEvent(t *testing.T) {
	t.Parallel()
	dc, bus, _, _, _ := newDCFull(t)

	var fetched []hmevent.DataFetchCompletedEvent
	var mu sync.Mutex
	events.Subscribe(bus, func(e hmevent.DataFetchCompletedEvent) {
		mu.Lock()
		fetched = append(fetched, e)
		mu.Unlock()
	})

	dc.HandleNewDevices(context.Background(), hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		device("AA", "HmIP-X", "1.0", "AA:0"),
		channel("AA:0", "AA", "MAINTENANCE"),
	})

	mu.Lock()
	got := len(fetched)
	mu.Unlock()
	if got != 1 {
		t.Fatalf("DataFetchCompletedEvent count=%d, want 1", got)
	}
	ev := fetched[0]
	if !ev.Success {
		t.Fatalf("DataFetchCompletedEvent.Success=%v, want true", ev.Success)
	}
	if ev.Operation != "new_devices" {
		t.Fatalf("DataFetchCompletedEvent.Operation=%q, want %q", ev.Operation, "new_devices")
	}
	if ev.Count != 1 {
		t.Fatalf("DataFetchCompletedEvent.Count=%d, want 1 (only top-level devices)", ev.Count)
	}
	if ev.InterfaceID != string(hmenum.InterfaceHmIPRF) {
		t.Fatalf("DataFetchCompletedEvent.InterfaceID=%q, want %q", ev.InterfaceID, hmenum.InterfaceHmIPRF)
	}
	if ev.CentralName != "c1" {
		t.Fatalf("DataFetchCompletedEvent.CentralName=%q, want %q", ev.CentralName, "c1")
	}
}

// TestHandleNewDevicesNoEventOnEmptySlice verifies that an empty descriptions
// slice does not emit a DataFetchCompletedEvent (nothing was fetched).
func TestHandleNewDevicesNoEventOnEmptySlice(t *testing.T) {
	t.Parallel()
	dc, bus, _, _, _ := newDCFull(t)

	var count atomic.Int32
	events.Subscribe(bus, func(_ hmevent.DataFetchCompletedEvent) { count.Add(1) })

	dc.HandleNewDevices(context.Background(), hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{})
	if count.Load() != 0 {
		t.Fatal("empty descriptions must not emit DataFetchCompletedEvent")
	}
}

// TestHandleNewDevicesCacheWorkerMarksDirty wires the CacheCoordinator
// to the bus and verifies the full chain: HandleNewDevices → DataFetchCompletedEvent
// → CacheCoordinator marks dirty → SaveIfChanged calls the persister.
func TestHandleNewDevicesCacheWorkerMarksDirty(t *testing.T) {
	t.Parallel()
	dc, bus, _, _, _ := newDCFull(t)

	cache := NewCacheCoordinator()
	fp := &fakePersister{}
	cache.SetPersister(fp)
	// Seed one entry and save to clear the initial dirty flag.
	cache.Set(hmtypes.DataPointKey{ChannelAddress: "X:0", Parameter: "P"}, hmtypes.IntValue(1), "seed")
	if err := cache.SaveAll(context.Background()); err != nil {
		t.Fatalf("initial SaveAll: %v", err)
	}
	cache.SubscribeToBus(bus)
	defer cache.UnsubscribeAll()

	// Trigger HandleNewDevices — this should emit DataFetchCompletedEvent
	// which the CacheCoordinator receives and marks dirty.
	dc.HandleNewDevices(context.Background(), hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		device("BB", "HmIP-Y", "1.0"),
	})

	// SaveIfChanged must now persist because dirty was set by the event.
	if err := cache.SaveIfChanged(context.Background()); err != nil {
		t.Fatalf("SaveIfChanged: %v", err)
	}
	if fp.saves != 2 {
		t.Fatalf("persister saves=%d, want 2 (initial + after HandleNewDevices)", fp.saves)
	}
}
