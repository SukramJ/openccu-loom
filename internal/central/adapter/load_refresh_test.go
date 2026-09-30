// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

// Tests for the central.refresh_client_data handler installed by
// wireLoadAndRefresh: besides the value reseed it re-pulls the paramset
// descriptions of every channel whose own description declares MASTER or
// VALUES but whose paramset registry entry is gone, so a missed or failed
// post-callback reload heals on the next periodic run instead of at restart.

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

const loadRefreshCentral = "ccu-01"

// paramsetDescOps serves a VALUES description for every address and records
// the addresses GetParamsetDescription was asked about.
type paramsetDescOps struct {
	fakeOperations

	mu    sync.Mutex
	calls map[string]int
}

func (f *paramsetDescOps) GetParamsetDescription(_ context.Context, addr string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = make(map[string]int)
	}
	f.calls[addr]++
	switch key {
	case hmenum.ParamsetKeyValues:
		return map[string]hmproto.ParameterData{"STATE": {Type: "BOOL"}}, nil
	case hmenum.ParamsetKeyMaster:
		return map[string]hmproto.ParameterData{}, nil
	default:
		return nil, errors.New("no such paramset")
	}
}

func (f *paramsetDescOps) callsFor(addr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[addr]
}

// countingSeeder records every interface it was asked to seed.
type countingSeeder struct {
	mu     sync.Mutex
	ifaces []hmenum.Interface
}

func (s *countingSeeder) SeedValues(_ context.Context, iface hmenum.Interface, _ SeedDepth) (map[string]map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ifaces = append(s.ifaces, iface)
	return map[string]map[string]any{}, nil
}

func (s *countingSeeder) seeded() []hmenum.Interface {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.ifaces)
}

// loadRefreshFixture builds a unit whose description registry holds one
// HmIP-RF device: :1 declares MASTER+VALUES, :2 declares neither. The
// paramset registry is empty, as it is after an eviction whose reload never
// completed.
func loadRefreshFixture(t *testing.T) (*central.Unit, hmtypes.WireInterfaceID) {
	t.Helper()
	unit, err := central.New(central.Config{Name: loadRefreshCentral})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	wireID := hmtypes.ParseWireInterfaceID(WireInterfaceID(loadRefreshCentral, hmenum.InterfaceHmIPRF))
	descList := []hmproto.DeviceDescription{
		{Address: "DEV0000001", Type: "HmIP-PS", Children: []string{"DEV0000001:1", "DEV0000001:2"}, Paramsets: []string{"MASTER"}},
		{Address: "DEV0000001:1", Parent: "DEV0000001", Type: "SWITCH_VIRTUAL_RECEIVER", Paramsets: []string{"LINK", "MASTER", "VALUES"}},
		{Address: "DEV0000001:2", Parent: "DEV0000001", Type: "MAINTENANCE_LIKE"},
	}
	for i := range descList {
		unit.DescRegistry.Put(wireID, descList[i])
	}
	return unit, wireID
}

func hmipOnly() []config.InterfaceSpec {
	return []config.InterfaceSpec{{Name: string(hmenum.InterfaceHmIPRF)}}
}

func runLoadAndRefresh(t *testing.T, unit *central.Unit) {
	t.Helper()
	if err := unit.LoadAndRefreshDataPointData(context.Background()); err != nil {
		t.Fatalf("LoadAndRefreshDataPointData: %v", err)
	}
}

// A channel that declares VALUES but lost its registry entry gets the
// description back from one run of the periodic handler.
func TestLoadAndRefreshRestoresMissingDeclaredParamsets(t *testing.T) {
	unit, wireID := loadRefreshFixture(t)
	ops := &paramsetDescOps{}
	seeder := &countingSeeder{}
	wireLoadAndRefresh(unit, NewDevicePipeline(unit), hmipOnly(), seeder,
		func(iface hmenum.Interface) (backends.Operations, bool) {
			return ops, iface == hmenum.InterfaceHmIPRF
		}, discardLogger())

	runLoadAndRefresh(t, unit)

	ps, ok := unit.ParamsetReg.Get(wireID, "DEV0000001:1", hmenum.ParamsetKeyValues)
	if !ok {
		t.Fatal("VALUES description of DEV0000001:1 still missing after the refresh run")
	}
	if _, ok := ps["STATE"]; !ok {
		t.Fatalf("VALUES description of DEV0000001:1 = %v, want the fetched STATE entry", ps)
	}
	if got := seeder.seeded(); len(got) != 1 || got[0] != hmenum.InterfaceHmIPRF {
		t.Fatalf("reseed interfaces = %v, want [HmIP-RF]", got)
	}
}

// A channel whose description declares neither MASTER nor VALUES can never
// gain one; fetching it on every run would be a permanent retry loop.
func TestLoadAndRefreshSkipsChannelsWithoutDeclaredParamsets(t *testing.T) {
	unit, _ := loadRefreshFixture(t)
	ops := &paramsetDescOps{}
	wireLoadAndRefresh(unit, NewDevicePipeline(unit), hmipOnly(), &countingSeeder{},
		func(hmenum.Interface) (backends.Operations, bool) { return ops, true }, discardLogger())

	runLoadAndRefresh(t, unit)
	runLoadAndRefresh(t, unit)

	if n := ops.callsFor("DEV0000001:2"); n != 0 {
		t.Fatalf("GetParamsetDescription called %d times for DEV0000001:2 (declares no MASTER/VALUES), want 0", n)
	}
	// The declared channel is healed by the first run and not fetched again.
	if n := ops.callsFor("DEV0000001:1"); n == 0 {
		t.Fatal("GetParamsetDescription never called for DEV0000001:1, want a heal on the first run")
	}
	first := ops.callsFor("DEV0000001:1")
	runLoadAndRefresh(t, unit)
	if n := ops.callsFor("DEV0000001:1"); n != first {
		t.Fatalf("DEV0000001:1 fetched again after it was healed (%d -> %d calls)", first, n)
	}
}

// An interface whose backend is not wired is skipped for the paramset heal;
// the value reseed still runs.
func TestLoadAndRefreshWithoutOperationsStillReseeds(t *testing.T) {
	for name, lookup := range map[string]func(hmenum.Interface) (backends.Operations, bool){
		"lookup miss": func(hmenum.Interface) (backends.Operations, bool) { return nil, false },
		"nil lookup":  nil,
	} {
		t.Run(name, func(t *testing.T) {
			unit, wireID := loadRefreshFixture(t)
			seeder := &countingSeeder{}
			wireLoadAndRefresh(unit, NewDevicePipeline(unit), hmipOnly(), seeder, lookup, discardLogger())

			runLoadAndRefresh(t, unit)

			if got := seeder.seeded(); len(got) != 1 || got[0] != hmenum.InterfaceHmIPRF {
				t.Fatalf("reseed interfaces = %v, want [HmIP-RF]", got)
			}
			if _, ok := unit.ParamsetReg.Get(wireID, "DEV0000001:1", hmenum.ParamsetKeyValues); ok {
				t.Fatal("VALUES description appeared without any backend")
			}
		})
	}
}
