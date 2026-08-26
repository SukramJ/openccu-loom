// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// callback_handlers_nil_channel_test.go covers the nil-channel and
// non-string-combined paths in CallbackHandlers.Event and
// dispatchCombined.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ============================================================
// Event — ch == nil path (device found, channel address unknown)
// ============================================================

func TestCallbackHandlersEventNilChannel(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-cbnil"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// Register device with only channel :1; call Event on :99 → ch == nil.
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "CBNIL001", Model: "HmIP-STH",
	})
	_ = d.AddChannel("CBNIL001:1", 1, "TEST", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	h := NewCallbackHandlers(c, nil)
	// Channel :99 is not registered → ch == nil → must return nil without panic.
	err = h.Event(context.Background(), "HmIP-RF", "CBNIL001:99", "STATE", xmlrpc.BoolValue(true))
	if err != nil {
		t.Fatalf("Event with nil channel must return nil, got: %v", err)
	}
}

// ============================================================
// dispatchCombined — non-string value path (line 149-155)
// ============================================================

func TestDispatchCombinedNonStringValue(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-cbcomb"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "CBCOMB001", Model: "HmIP-STH",
	})
	ch := d.AddChannel("CBCOMB001:1", 1, "TEST", hmenum.ParamsetKeyValues)
	_ = ch
	c.ModelRegistry.Put(d)

	h := NewCallbackHandlers(c, nil)
	// Pass a BoolValue for COMBINED_PARAMETER — AsString will fail,
	// hitting the non-string early-return branch.
	h.dispatchCombined("HmIP-RF", "CBCOMB001:1", "COMBINED_PARAMETER", xmlrpc.BoolValue(true))
	// Must not panic; early return with debug log.
}

// ============================================================
// dispatchCombined — dev == nil path (line 167-169)
// ============================================================

func TestDispatchCombinedNilDevice(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-cbdev"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// No devices registered → ModelRegistry.Get returns !ok → dev == nil.
	h := NewCallbackHandlers(c, nil)
	// Pass a valid string so AsString succeeds and we reach the device lookup.
	h.dispatchCombined("HmIP-RF", "NODEV001:1", "COMBINED_PARAMETER", xmlrpc.StringValue("LEVEL=0.5,LEVEL_2=0.5"))
	// Must not panic.
}

// ============================================================
// dispatchCombined — ch == nil path (line 171-173)
// ============================================================

func TestDispatchCombinedNilChannelInDispatch(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-cbch"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// Register device with only channel :1; dispatch on :99 → ch == nil.
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "CBCH001", Model: "HmIP-STH",
	})
	_ = d.AddChannel("CBCH001:1", 1, "TEST", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	h := NewCallbackHandlers(c, nil)
	// Valid string value, device found, but channel :99 not registered → ch == nil.
	h.dispatchCombined("HmIP-RF", "CBCH001:99", "COMBINED_PARAMETER", xmlrpc.StringValue("LEVEL=0.5,LEVEL_2=0.5"))
	// Must not panic.
}
