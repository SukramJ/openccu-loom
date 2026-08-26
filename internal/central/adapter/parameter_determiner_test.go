// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// paramFakeOpsExtended wraps paramsetFakeOps and lets individual tests
// override DetermineParameter without modifying the shared stub type.
type paramFakeOpsExtended struct {
	paramsetFakeOps
	determineFn func(ctx context.Context, channelAddress, parameter string) (any, error)
}

func (f *paramFakeOpsExtended) DetermineParameter(ctx context.Context, channelAddress, parameter string) (any, error) {
	if f.determineFn != nil {
		return f.determineFn(ctx, channelAddress, parameter)
	}
	return nil, nil
}

// TestParameterDeterminerAdapter_NilRegistry verifies the adapter returns an
// error when both registry and writer are nil.
func TestParameterDeterminerAdapter_NilRegistry(t *testing.T) {
	t.Parallel()
	a := NewParameterDeterminerAdapter(nil, nil)
	_, err := a.DetermineParameter(context.Background(), "HmIP-RF", "VCU0001:1", "TEMPERATURE")
	if err == nil {
		t.Fatal("expected error when registry is nil, got nil")
	}
	if !errors.Is(err, ErrNoDetermineBackend) {
		t.Fatalf("expected ErrNoDetermineBackend, got %v", err)
	}
}

// TestParameterDeterminerAdapter_DeviceNotFound verifies the adapter returns
// ErrNoDetermineBackend when the device cannot be found in the registry.
func TestParameterDeterminerAdapter_DeviceNotFound(t *testing.T) {
	t.Parallel()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	reg := central.NewRegistry()
	_ = reg.Register(c)

	a := NewParameterDeterminerAdapter(reg, client.NewValueWriter())
	_, err := a.DetermineParameter(context.Background(), "HmIP-RF", "UNKNOWN0001:1", "TEMPERATURE")
	if err == nil {
		t.Fatal("expected error when device not found, got nil")
	}
	if !errors.Is(err, ErrNoDetermineBackend) {
		t.Fatalf("expected ErrNoDetermineBackend, got %v", err)
	}
}

// TestParameterDeterminerAdapter_DelegatesToBackend verifies the adapter
// calls backend.DetermineParameter with the correct channel address and
// parameter ID, and forwards the returned value.
func TestParameterDeterminerAdapter_DelegatesToBackend(t *testing.T) {
	t.Parallel()
	const (
		centralName    = "ccu-01"
		interfaceID    = "HmIP-RF"
		deviceAddress  = "VCU0001"
		channelAddress = "VCU0001:1"
		parameterID    = "SET_POINT_TEMPERATURE"
		wantValue      = float64(21.5)
	)

	called := false
	fakeOps := &paramFakeOpsExtended{
		determineFn: func(_ context.Context, addr, param string) (any, error) {
			called = true
			if addr != channelAddress {
				return nil, errors.New("unexpected channelAddress: " + addr)
			}
			if param != parameterID {
				return nil, errors.New("unexpected parameter: " + param)
			}
			return wantValue, nil
		},
	}

	c, _ := central.New(central.Config{Name: centralName})
	reg := central.NewRegistry()
	_ = reg.Register(c)

	d := device.New(device.Config{
		Address:     deviceAddress,
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: interfaceID,
	})
	c.ModelRegistry.Put(d)

	w := client.NewValueWriter()
	w.Register(centralName, interfaceID, fakeOps)

	a := NewParameterDeterminerAdapter(reg, w)
	got, err := a.DetermineParameter(context.Background(), "ignored-interface-id", channelAddress, parameterID)
	if err != nil {
		t.Fatalf("DetermineParameter: %v", err)
	}
	if !called {
		t.Fatal("backend.DetermineParameter was not called")
	}
	if got != wantValue {
		t.Fatalf("DetermineParameter = %v; want %v", got, wantValue)
	}
}
