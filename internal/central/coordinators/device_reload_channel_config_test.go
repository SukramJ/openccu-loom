// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// channelParamsetFetcherStub records the paramset descriptions and value
// reads requested by DeviceCoordinator.ReloadChannelConfig.
type channelParamsetFetcherStub struct {
	descCalls   []hmenum.ParamsetKey
	valueCalls  []hmenum.ParamsetKey
	descReturns map[hmenum.ParamsetKey]map[string]hmproto.ParameterData
	descErr     map[hmenum.ParamsetKey]error
}

func (s *channelParamsetFetcherStub) GetParamsetDescription(
	_ context.Context, _ string, key hmenum.ParamsetKey,
) (map[string]hmproto.ParameterData, error) {
	s.descCalls = append(s.descCalls, key)
	if s.descErr != nil {
		if err := s.descErr[key]; err != nil {
			return nil, err
		}
	}
	if s.descReturns != nil {
		return s.descReturns[key], nil
	}
	return map[string]hmproto.ParameterData{}, nil
}

func (s *channelParamsetFetcherStub) GetParamset(
	_ context.Context, _ string, key hmenum.ParamsetKey,
) (map[string]any, error) {
	s.valueCalls = append(s.valueCalls, key)
	return map[string]any{}, nil
}

func newReloadChannelCoordinator() (*DeviceCoordinator, *registry.ParamsetRegistry) {
	bus := events.NewBus()
	devs := registry.NewDeviceRegistry()
	descs := registry.NewDeviceDescriptionRegistry()
	ps := registry.NewParamsetRegistry()
	return NewDeviceCoordinator("main", bus, devs, descs, ps, nil, nil), ps
}

func TestReloadChannelConfigPullsAllParamsetKindsAndStores(t *testing.T) {
	dc, ps := newReloadChannelCoordinator()
	fetcher := &channelParamsetFetcherStub{
		descReturns: map[hmenum.ParamsetKey]map[string]hmproto.ParameterData{
			hmenum.ParamsetKeyMaster: {"TEMPERATURE": {Type: "FLOAT"}},
			hmenum.ParamsetKeyValues: {"LEVEL": {Type: "FLOAT"}},
		},
	}

	if err := dc.ReloadChannelConfig(
		context.Background(), fetcher, wireKey(hmenum.InterfaceHmIPRF), "ABC0001:1", "HmIP-STH",
	); err != nil {
		t.Fatalf("ReloadChannelConfig: %v", err)
	}

	// VALUES, MASTER, LINK descriptions are requested.
	if len(fetcher.descCalls) != 3 {
		t.Fatalf("expected 3 description reads, got %v", fetcher.descCalls)
	}
	// No MASTER value round trip: nothing in the coordinator keeps the
	// answer, so a read would be pure CCU load.
	if len(fetcher.valueCalls) != 0 {
		t.Fatalf("expected no paramset value read, got %v", fetcher.valueCalls)
	}
	// The re-pulled MASTER description was stored in the registry.
	stored, ok := ps.Get(wireKey(hmenum.InterfaceHmIPRF), "ABC0001:1", hmenum.ParamsetKeyMaster)
	if !ok {
		t.Fatal("MASTER paramset not stored after reload")
	}
	if _, has := stored["TEMPERATURE"]; !has {
		t.Fatalf("stored MASTER paramset missing TEMPERATURE, got %v", stored)
	}
}

func TestReloadChannelConfigNilFetcherReturnsError(t *testing.T) {
	dc, _ := newReloadChannelCoordinator()
	if err := dc.ReloadChannelConfig(
		context.Background(), nil, wireKey(hmenum.InterfaceHmIPRF), "ABC0001:1", "",
	); err == nil {
		t.Fatal("expected error for nil fetcher")
	}
}

func TestReloadChannelConfigEmptyChannelReturnsError(t *testing.T) {
	dc, _ := newReloadChannelCoordinator()
	fetcher := &channelParamsetFetcherStub{}
	if err := dc.ReloadChannelConfig(
		context.Background(), fetcher, wireKey(hmenum.InterfaceHmIPRF), "", "",
	); err == nil {
		t.Fatal("expected error for empty channel address")
	}
}

func TestReloadChannelConfigAllDescriptionsFailReturnsError(t *testing.T) {
	dc, _ := newReloadChannelCoordinator()
	fetcher := &channelParamsetFetcherStub{
		descErr: map[hmenum.ParamsetKey]error{
			hmenum.ParamsetKeyValues: errors.New("ccu offline"),
			hmenum.ParamsetKeyMaster: errors.New("ccu offline"),
			hmenum.ParamsetKeyLink:   errors.New("ccu offline"),
		},
	}
	if err := dc.ReloadChannelConfig(
		context.Background(), fetcher, wireKey(hmenum.InterfaceHmIPRF), "ABC0001:1", "",
	); err == nil {
		t.Fatal("expected error when every paramset description fetch fails")
	}
}
