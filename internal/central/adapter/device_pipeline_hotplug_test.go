// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// hotplugFakeOps is a paramsetFakeOps specialisation that records, per
// address, how many times GetParamsetDescription / GetParamset were
// called. Tests use the per-address counters to prove that a hot-plug
// ingest touches only the newly materialised device roots and never
// re-reads an already-known device.
type hotplugFakeOps struct {
	paramsetFakeOps
	mu            sync.Mutex
	descCalls     map[string]int
	paramsetCalls map[string]int
	// valuesParams maps a channel address to the VALUES paramset
	// description returned for it; MASTER stays empty unless a test fills
	// masterParams, so hydration + seedMasterValues stay cheap and
	// deterministic.
	valuesParams map[string]map[string]hmproto.ParameterData
	// masterParams is the MASTER counterpart, used by the tests that need a
	// writable MASTER parameter to trigger the post-write refresh hook.
	masterParams map[string]map[string]hmproto.ParameterData
}

func newHotplugFakeOps(valuesParams map[string]map[string]hmproto.ParameterData) *hotplugFakeOps {
	return &hotplugFakeOps{
		descCalls:     make(map[string]int),
		paramsetCalls: make(map[string]int),
		valuesParams:  valuesParams,
	}
}

func (f *hotplugFakeOps) GetParamsetDescription(
	_ context.Context, address string, key hmenum.ParamsetKey,
) (map[string]hmproto.ParameterData, error) {
	f.mu.Lock()
	f.descCalls[address]++
	f.mu.Unlock()
	if key == hmenum.ParamsetKeyValues {
		return f.valuesParams[address], nil
	}
	return f.masterParams[address], nil
}

func (f *hotplugFakeOps) GetParamset(
	_ context.Context, address string, _ hmenum.ParamsetKey,
) (map[string]any, error) {
	f.mu.Lock()
	f.paramsetCalls[address]++
	f.mu.Unlock()
	return map[string]any{}, nil
}

func (f *hotplugFakeOps) descCallCount(address string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.descCalls[address]
}

func (f *hotplugFakeOps) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.descCalls {
		n += c
	}
	for _, c := range f.paramsetCalls {
		n += c
	}
	return n
}

// stateParam is a minimal writable-bool VALUES parameter description used
// to prove that hydration actually builds a data point for a hot-plugged
// channel.
var stateParam = map[string]hmproto.ParameterData{
	string(hmenum.ParameterState): {
		Type:       hmenum.ParameterTypeBool,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
	},
}

// deviceRootAndChannel builds the two-description slice ["<addr>" root,
// "<addr>:1" channel] the hot-plug callback would announce for a freshly
// paired device.
func deviceRootAndChannel(addr, model, channelType string) []hmproto.DeviceDescription {
	return []hmproto.DeviceDescription{
		{Address: addr, Type: model},
		{Address: addr + ":1", Parent: addr, Type: channelType},
	}
}

// TestIngestNewDevicesMaterialisesNewDeviceRoot verifies that a hot-plug
// announcement of a device the ModelRegistry does not know yet is fully
// materialised: the returned address slice contains exactly the new root,
// the device is registered with its channels, and the VALUES paramset
// hydration produced a real data point.
func TestIngestNewDevicesMaterialisesNewDeviceRoot(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-hotplug-new"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	p := NewDevicePipeline(c)

	const addr = "AABBCC10"
	descs := []hmproto.DeviceDescription{
		{Address: addr, Type: "HmIP-PS"},
		{Address: addr + ":1", Parent: addr, Type: "SWITCH_VIRTUAL_RECEIVER"},
		{Address: addr + ":2", Parent: addr, Type: "MAINTENANCE"},
	}
	fake := newHotplugFakeOps(map[string]map[string]hmproto.ParameterData{
		addr + ":1": stateParam,
	})

	got, err := p.IngestNewDevices(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		fake, &fakeWriter{}, nil, descs, slog.Default(),
	)
	if err != nil {
		t.Fatalf("IngestNewDevices: %v", err)
	}
	if len(got) != 1 || got[0] != addr {
		t.Fatalf("IngestNewDevices addresses = %v, want [%s]", got, addr)
	}

	dev, ok := c.ModelRegistry.Get(addr)
	if !ok {
		t.Fatal("device not materialised in ModelRegistry")
	}
	if len(dev.Channels()) != 2 {
		t.Fatalf("device channels = %d, want 2", len(dev.Channels()))
	}
	ch := dev.Channel(addr + ":1")
	if ch == nil {
		t.Fatal("channel :1 not found")
	}
	dp := ch.Parameter(hmenum.ParameterState)
	if dp == nil {
		t.Fatal("STATE data point missing after hot-plug hydration")
	}
}

// TestIngestNewDevicesDedupsAlreadyKnownDevice verifies that announcing a
// device root the ModelRegistry already knows is a pure no-op: the returned
// slice is empty, no backend call is issued, and the device's existing
// channel set is left untouched (no re-hydration / replacement).
func TestIngestNewDevicesDedupsAlreadyKnownDevice(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-hotplug-dedup"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	p := NewDevicePipeline(c)

	const addr = "AABBCC11"
	existing := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-PS",
	})
	c.ModelRegistry.Put(existing)

	fake := newHotplugFakeOps(nil)
	descs := deviceRootAndChannel(addr, "HmIP-PS", "SWITCH_VIRTUAL_RECEIVER")

	got, err := p.IngestNewDevices(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		fake, &fakeWriter{}, nil, descs, slog.Default(),
	)
	if err != nil {
		t.Fatalf("IngestNewDevices: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("IngestNewDevices addresses = %v, want empty (device already known)", got)
	}
	if calls := fake.totalCalls(); calls != 0 {
		t.Fatalf("backend calls = %d, want 0 (already-known root must short-circuit)", calls)
	}
	if len(existing.Channels()) != 0 {
		t.Fatalf("existing device channels = %d, want 0 (no replacement on dedup)", len(existing.Channels()))
	}
}

// TestIngestNewDevicesScopesToNewRootsOnly verifies that when one device
// (A) is already materialised and a hot-plug callback announces both A and
// a genuinely new device (B), only B is materialised and returned, and the
// backend paramset calls triggered by the second ingest touch only B's
// addresses — A is never re-read.
func TestIngestNewDevicesScopesToNewRootsOnly(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-hotplug-scope"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	p := NewDevicePipeline(c)

	const addrA = "AAAA0001"
	const addrB = "BBBB0002"
	fake := newHotplugFakeOps(map[string]map[string]hmproto.ParameterData{
		addrA + ":1": stateParam,
		addrB + ":1": stateParam,
	})

	descsA := deviceRootAndChannel(addrA, "HmIP-PS", "SWITCH_VIRTUAL_RECEIVER")
	descsB := deviceRootAndChannel(addrB, "HmIP-PS", "SWITCH_VIRTUAL_RECEIVER")

	gotA, err := p.IngestNewDevices(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		fake, &fakeWriter{}, nil, descsA, slog.Default(),
	)
	if err != nil {
		t.Fatalf("first IngestNewDevices (A): %v", err)
	}
	if len(gotA) != 1 || gotA[0] != addrA {
		t.Fatalf("first ingest addresses = %v, want [%s]", gotA, addrA)
	}

	// Snapshot A's call counters before the second, mixed announcement.
	descCallsForABefore := fake.descCallCount(addrA)
	channelCallsForABefore := fake.descCallCount(addrA + ":1")

	both := append(append([]hmproto.DeviceDescription{}, descsA...), descsB...)
	gotBoth, err := p.IngestNewDevices(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		fake, &fakeWriter{}, nil, both, slog.Default(),
	)
	if err != nil {
		t.Fatalf("second IngestNewDevices (A+B): %v", err)
	}
	if len(gotBoth) != 1 || gotBoth[0] != addrB {
		t.Fatalf("second ingest addresses = %v, want [%s] (A already known)", gotBoth, addrB)
	}

	if got := fake.descCallCount(addrA); got != descCallsForABefore {
		t.Errorf("GetParamsetDescription(%s) calls after scoped ingest = %d, want unchanged %d",
			addrA, got, descCallsForABefore)
	}
	if got := fake.descCallCount(addrA + ":1"); got != channelCallsForABefore {
		t.Errorf("GetParamsetDescription(%s) calls after scoped ingest = %d, want unchanged %d",
			addrA+":1", got, channelCallsForABefore)
	}
	if fake.descCallCount(addrB) == 0 {
		t.Error("GetParamsetDescription(B root) was never called — B was not hydrated")
	}
	if fake.descCallCount(addrB+":1") == 0 {
		t.Error("GetParamsetDescription(B:1) was never called — B's channel was not hydrated")
	}

	if _, ok := c.ModelRegistry.Get(addrB); !ok {
		t.Fatal("device B not materialised in ModelRegistry")
	}
}

// TestIngestNewDevicesUsesDeviceDetailsNameFallback verifies that a
// hot-plugged device with no entry in the pipeline's static name snapshot
// falls back to the live DeviceDetails cache — the source the hot-plug path
// force-refreshes before every ingest, per [DevicePipeline.ensureDevice].
func TestIngestNewDevicesUsesDeviceDetailsNameFallback(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-hotplug-name"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	const addr = "AABBCC20"
	const wantName = "Mein Gerät"
	c.DeviceDetails.AddName(addr, wantName)

	p := NewDevicePipeline(c)
	descs := deviceRootAndChannel(addr, "HmIP-PS", "SWITCH_VIRTUAL_RECEIVER")
	fake := newHotplugFakeOps(map[string]map[string]hmproto.ParameterData{
		addr + ":1": stateParam,
	})

	got, err := p.IngestNewDevices(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		fake, &fakeWriter{}, nil, descs, slog.Default(),
	)
	if err != nil {
		t.Fatalf("IngestNewDevices: %v", err)
	}
	if len(got) != 1 || got[0] != addr {
		t.Fatalf("IngestNewDevices addresses = %v, want [%s]", got, addr)
	}

	dev, ok := c.ModelRegistry.Get(addr)
	if !ok {
		t.Fatal("device not materialised in ModelRegistry")
	}
	if dev.Name() != wantName {
		t.Errorf("device.Name = %q, want %q (DeviceDetails fallback)", dev.Name(), wantName)
	}
}

// TestIngestNewDevicesConcurrentSameDeviceMaterialisesOnce fires two
// concurrent IngestNewDevices calls announcing the same brand-new device.
// The shared ingest mutex must serialise them so the device is materialised
// exactly once — the second caller finds the root already registered and
// no-ops. Run with -race: neither call may panic or corrupt the registry.
func TestIngestNewDevicesConcurrentSameDeviceMaterialisesOnce(t *testing.T) {
	c, err := central.New(central.Config{Name: "ccu-hotplug-race"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	p := NewDevicePipeline(c)

	const addr = "AABBCC30"
	descs := deviceRootAndChannel(addr, "HmIP-PS", "SWITCH_VIRTUAL_RECEIVER")
	fake := newHotplugFakeOps(map[string]map[string]hmproto.ParameterData{
		addr + ":1": stateParam,
	})

	var wg sync.WaitGroup
	results := make([][]string, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := p.IngestNewDevices(
				context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
				fake, &fakeWriter{}, nil, descs, slog.Default(),
			)
			results[i] = got
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: IngestNewDevices: %v", i, err)
		}
	}

	nonEmpty := 0
	var materialised []string
	for _, got := range results {
		if len(got) > 0 {
			nonEmpty++
			materialised = got
		}
	}
	if nonEmpty != 1 {
		t.Fatalf("goroutines that materialised the device = %d, want exactly 1 (results=%v)", nonEmpty, results)
	}
	if len(materialised) != 1 || materialised[0] != addr {
		t.Fatalf("materialised addresses = %v, want [%s]", materialised, addr)
	}

	if c.ModelRegistry.Len() != 1 {
		t.Fatalf("ModelRegistry.Len() = %d, want 1 (device materialised exactly once)", c.ModelRegistry.Len())
	}
	dev, ok := c.ModelRegistry.Get(addr)
	if !ok {
		t.Fatal("device not found after concurrent ingest")
	}
	if len(dev.Channels()) != 1 {
		t.Fatalf("device channels = %d, want 1", len(dev.Channels()))
	}

	// The winning goroutine's Ingest call must have hit the backend exactly
	// once per address — the loser's early-return path issues zero calls.
	if got := fake.descCallCount(addr); got == 0 {
		t.Error("winning goroutine never called GetParamsetDescription for the device root")
	}
}

// TestIngestNewDevicesUsesTheAnnouncingInterfacesMasterRefreshHook pins the
// hook a hot-plugged device's channels receive to the interface that
// announced them.
//
// One pipeline serves every interface of a central, and the hot-plug ingestor
// is built after the whole interface loop has run. With a single hook slot the
// pipeline held whichever interface was wired last, so a device paired at
// runtime on HmIP-RF got the BidCos-RF poller: every MASTER read-back for it
// was then issued through the BidCos-RF backend with an HmIP address, which
// the CCU rejects and which counts against the wrong interface's breaker.
func TestIngestNewDevicesUsesTheAnnouncingInterfacesMasterRefreshHook(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-hotplug-hooks"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	hmipWire := WireInterfaceID(c.Name(), hmenum.InterfaceHmIPRF)
	bidcosWire := WireInterfaceID(c.Name(), hmenum.InterfaceBidCosRF)

	hmipHook := make(chan string, 1)
	bidcosHook := make(chan string, 1)
	p := NewDevicePipeline(c)
	// HmIP registers no poller in production; register one here anyway so the
	// test can tell "the right hook ran" from "no hook ran at all".
	p.WithMasterRefreshHook(hmipWire, func(addr string, _ hmenum.ParamsetKey) { hmipHook <- addr })
	// BidCos-RF is wired last, exactly as `interfaces: [HmIP-RF, BidCos-RF]`
	// brings the central up.
	p.WithMasterRefreshHook(bidcosWire, func(addr string, _ hmenum.ParamsetKey) { bidcosHook <- addr })

	const addr = "HMIPHOTPLUG1"
	const masterParam = hmenum.Parameter("CYCLIC_INFO_MSG")
	fake := newHotplugFakeOps(map[string]map[string]hmproto.ParameterData{
		addr + ":1": stateParam,
	})
	fake.masterParams = map[string]map[string]hmproto.ParameterData{
		addr + ":1": {
			string(masterParam): {
				Type:       hmenum.ParameterTypeBool,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			},
		},
	}

	if _, err := p.IngestNewDevices(
		context.Background(), hmipWire, hmenum.InterfaceHmIPRF,
		fake, &fakeWriter{}, nil, deviceRootAndChannel(addr, "HmIP-PS", "SWITCH_VIRTUAL_RECEIVER"),
		slog.Default(),
	); err != nil {
		t.Fatalf("IngestNewDevices: %v", err)
	}

	dev, ok := c.ModelRegistry.Get(addr)
	if !ok {
		t.Fatal("hot-plugged device not materialised")
	}
	ch := dev.Channel(addr + ":1")
	if ch == nil {
		t.Fatal("channel :1 not found")
	}
	if err := ch.Set(context.Background(), hmenum.ParamsetKeyMaster, masterParam,
		hmtypes.BoolValue(true), device.SetOptions{}); err != nil {
		t.Fatalf("MASTER write: %v", err)
	}

	select {
	case got := <-hmipHook:
		if got != addr+":1" {
			t.Fatalf("HmIP-RF refresh hook got address %q, want %q", got, addr+":1")
		}
	case got := <-bidcosHook:
		t.Fatalf("a device announced on HmIP-RF scheduled its MASTER read-back "+
			"through the BidCos-RF poller (address %q)", got)
	case <-time.After(2 * time.Second):
		t.Fatal("no MASTER refresh hook fired for the hot-plugged device")
	}
}
