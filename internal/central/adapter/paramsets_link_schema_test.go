// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// linkSchemaFakeOps embeds paramsetFakeOps and lets individual tests override
// GetLinkParamsetDescription without modifying the shared stub.
type linkSchemaFakeOps struct {
	paramsetFakeOps
	linkDescFn func(ctx context.Context, channelAddress, peerAddress string) (map[string]hmproto.ParameterData, error)
}

func (f *linkSchemaFakeOps) GetLinkParamsetDescription(
	ctx context.Context, channelAddress, peerAddress string,
) (map[string]hmproto.ParameterData, error) {
	if f.linkDescFn != nil {
		return f.linkDescFn(ctx, channelAddress, peerAddress)
	}
	return nil, nil
}

// TestGetLinkFormSchema_NoRegistry verifies the method returns
// ErrNoParamsetBackend when no registry is wired.
func TestGetLinkFormSchema_NoRegistry(t *testing.T) {
	t.Parallel()
	domain := NewParamsetsDomain(nil, nil)
	_, err := domain.GetLinkFormSchema(context.Background(), "HmIP-RF", "VCU0001:1", "VCU0002:1")
	if err == nil {
		t.Fatal("expected error when registry is nil, got nil")
	}
	if !errors.Is(err, ErrNoParamsetBackend) {
		t.Fatalf("expected ErrNoParamsetBackend, got %v", err)
	}
}

// TestGetLinkFormSchema_ReturnsDescriptorAsMap verifies that the method
// fetches the LINK paramset descriptor and converts it to map[string]any.
func TestGetLinkFormSchema_ReturnsDescriptorAsMap(t *testing.T) {
	t.Parallel()
	const (
		centralName   = "ccu-01"
		interfaceID   = "HmIP-RF"
		deviceAddress = "VCU0001"
		receiverAddr  = "VCU0001:1"
		senderAddr    = "VCU0002:1"
		paramName     = "COND_TX"
	)

	wantDesc := map[string]hmproto.ParameterData{
		paramName: {
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Min:        json.RawMessage("0"),
			Max:        json.RawMessage("15"),
		},
	}

	called := false
	fakeOps := &linkSchemaFakeOps{
		linkDescFn: func(_ context.Context, chAddr, peer string) (map[string]hmproto.ParameterData, error) {
			called = true
			if chAddr != receiverAddr {
				return nil, errors.New("unexpected receiverAddr: " + chAddr)
			}
			if peer != senderAddr {
				return nil, errors.New("unexpected senderAddr: " + peer)
			}
			return wantDesc, nil
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
	domain := NewParamsetsDomain(reg, w)

	schema, err := domain.GetLinkFormSchema(context.Background(), "", receiverAddr, senderAddr)
	if err != nil {
		t.Fatalf("GetLinkFormSchema: %v", err)
	}
	if !called {
		t.Fatal("backend.GetLinkParamsetDescription was not called")
	}
	if _, ok := schema[paramName]; !ok {
		t.Fatalf("schema does not contain parameter %q; got %v", paramName, schema)
	}
}

// TestGetLinkFormSchema_DeviceNotFound verifies ErrNoParamsetBackend is
// returned when the device cannot be found in the registry.
func TestGetLinkFormSchema_DeviceNotFound(t *testing.T) {
	t.Parallel()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	reg := central.NewRegistry()
	_ = reg.Register(c)
	w := client.NewValueWriter()
	domain := NewParamsetsDomain(reg, w)

	_, err := domain.GetLinkFormSchema(context.Background(), "", "UNKNOWN:1", "OTHER:1")
	if err == nil {
		t.Fatal("expected error when device not found, got nil")
	}
	if !errors.Is(err, ErrNoParamsetBackend) {
		t.Fatalf("expected ErrNoParamsetBackend, got %v", err)
	}
}
