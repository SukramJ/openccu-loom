// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// perAddressParamsetFetcher serves a VALUES description per address and
// records every address it was asked about; failing addresses return an error
// for every paramset key.
type perAddressParamsetFetcher struct {
	mu      sync.Mutex
	seen    []string
	failing map[string]bool
}

func (f *perAddressParamsetFetcher) GetParamsetDescription(
	_ context.Context, address string, key hmenum.ParamsetKey,
) (map[string]hmproto.ParameterData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.Contains(f.seen, address) {
		f.seen = append(f.seen, address)
	}
	if f.failing[address] {
		return nil, errors.New("ccu offline")
	}
	if key != hmenum.ParamsetKeyValues {
		return map[string]hmproto.ParameterData{}, nil
	}
	return map[string]hmproto.ParameterData{"LEVEL": {Type: "FLOAT"}}, nil
}

func newReloadDeviceParamsetsCoordinator(descs ...hmproto.DeviceDescription) (*DeviceCoordinator, *registry.ParamsetRegistry) {
	descReg := registry.NewDeviceDescriptionRegistry()
	iface := wireKey(hmenum.InterfaceHmIPRF)
	for i := range descs {
		descReg.Put(iface, descs[i])
	}
	ps := registry.NewParamsetRegistry()
	return NewDeviceCoordinator("main", events.NewBus(), registry.NewDeviceRegistry(), descReg, ps, nil, nil), ps
}

func reloadDeviceDescs(address string) []hmproto.DeviceDescription {
	return []hmproto.DeviceDescription{
		{Address: address, Type: "HmIP-BROLL", Children: []string{address + ":0", address + ":1"}},
		{Address: address + ":0", Parent: address},
		{Address: address + ":1", Parent: address},
	}
}

func TestReloadDeviceParamsetsReloadsRootAndEveryChannelOnly(t *testing.T) {
	descs := append(reloadDeviceDescs("DEV0001"), reloadDeviceDescs("OTHER01")...)
	dc, ps := newReloadDeviceParamsetsCoordinator(descs...)
	fetcher := &perAddressParamsetFetcher{}

	if err := dc.ReloadDeviceParamsets(context.Background(), fetcher, wireKey(hmenum.InterfaceHmIPRF), "DEV0001"); err != nil {
		t.Fatalf("ReloadDeviceParamsets: %v", err)
	}

	slices.Sort(fetcher.seen)
	if want := []string{"DEV0001", "DEV0001:0", "DEV0001:1"}; !slices.Equal(fetcher.seen, want) {
		t.Fatalf("addresses fetched = %v, want %v", fetcher.seen, want)
	}
	for _, addr := range []string{"DEV0001", "DEV0001:0", "DEV0001:1"} {
		stored, ok := ps.Get(wireKey(hmenum.InterfaceHmIPRF), addr, hmenum.ParamsetKeyValues)
		if !ok {
			t.Fatalf("VALUES paramset of %s not stored", addr)
		}
		if _, has := stored["LEVEL"]; !has {
			t.Fatalf("VALUES paramset of %s lacks LEVEL, got %v", addr, stored)
		}
	}
	if _, ok := ps.Get(wireKey(hmenum.InterfaceHmIPRF), "OTHER01:1", hmenum.ParamsetKeyValues); ok {
		t.Fatal("paramset of an unrelated device was reloaded")
	}
}

func TestReloadDeviceParamsetsPartialFailureKeepsGoing(t *testing.T) {
	dc, ps := newReloadDeviceParamsetsCoordinator(reloadDeviceDescs("DEV0001")...)
	fetcher := &perAddressParamsetFetcher{failing: map[string]bool{"DEV0001:0": true}}

	if err := dc.ReloadDeviceParamsets(context.Background(), fetcher, wireKey(hmenum.InterfaceHmIPRF), "DEV0001"); err != nil {
		t.Fatalf("ReloadDeviceParamsets with one failing channel: %v", err)
	}
	if _, ok := ps.Get(wireKey(hmenum.InterfaceHmIPRF), "DEV0001:1", hmenum.ParamsetKeyValues); !ok {
		t.Fatal("channel after the failing one was not reloaded")
	}
}

func TestReloadDeviceParamsetsTotalFailureReturnsError(t *testing.T) {
	dc, _ := newReloadDeviceParamsetsCoordinator(reloadDeviceDescs("DEV0001")...)
	fetcher := &perAddressParamsetFetcher{failing: map[string]bool{"DEV0001": true, "DEV0001:0": true, "DEV0001:1": true}}

	if err := dc.ReloadDeviceParamsets(context.Background(), fetcher, wireKey(hmenum.InterfaceHmIPRF), "DEV0001"); err == nil {
		t.Fatal("expected error when every address fails")
	}
}

func TestReloadDeviceParamsetsUnknownDeviceReturnsError(t *testing.T) {
	dc, _ := newReloadDeviceParamsetsCoordinator()
	if err := dc.ReloadDeviceParamsets(context.Background(), &perAddressParamsetFetcher{}, wireKey(hmenum.InterfaceHmIPRF), "DEV0001"); err == nil {
		t.Fatal("expected error for an undescribed device")
	}
}

func TestReloadDeviceParamsetsNilFetcherReturnsError(t *testing.T) {
	dc, _ := newReloadDeviceParamsetsCoordinator(reloadDeviceDescs("DEV0001")...)
	if err := dc.ReloadDeviceParamsets(context.Background(), nil, wireKey(hmenum.InterfaceHmIPRF), "DEV0001"); err == nil {
		t.Fatal("expected error for nil fetcher")
	}
}
