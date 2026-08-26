// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// uischema_expand_lookup_test.go covers the success paths in
// UISchemaAdapter.expandPresets (preset found with options) and
// UISchemaAdapter.lookupChannel (device found in registry),
// plus dpIsWritable fallback-to-descriptor path using a fake DP
// that doesn't implement IsWritable().

package adapter

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ============================================================
// expandPresets — preset found with options
// ============================================================

func TestExpandPresetsFound(t *testing.T) {
	t.Parallel()
	em := &ccudata.Easymode{
		OptionPresets: map[string]ccudata.OptionPreset{
			"my-preset": {
				ID: "my-preset",
				Options: []ccudata.OptionPresetVal{
					{Value: "opt1", Label: "label.opt1"},
					{Value: 42, Label: "label.opt2"},
				},
			},
		},
	}
	a := &UISchemaAdapter{easymode: em}
	got := a.expandPresets("en", "my-preset")
	if len(got) != 2 {
		t.Fatalf("expandPresets found preset = %d entries, want 2", len(got))
	}
}

func TestExpandPresetsEmptyOptions(t *testing.T) {
	t.Parallel()
	em := &ccudata.Easymode{
		OptionPresets: map[string]ccudata.OptionPreset{
			"empty-preset": {
				ID:      "empty-preset",
				Options: nil, // empty → should return nil
			},
		},
	}
	a := &UISchemaAdapter{easymode: em}
	got := a.expandPresets("en", "empty-preset")
	if got != nil {
		t.Errorf("expandPresets empty options = %v, want nil", got)
	}
}

// ============================================================
// UISchemaAdapter.lookupChannel — registry with device
// ============================================================

func TestLookupChannelUISchemaDeviceFound(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-ui"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	dev := device.New(device.Config{Address: "DEV001", InterfaceID: "HmIP-RF", Model: "TestModel"})
	dev.AddChannel("DEV001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)

	a := &UISchemaAdapter{registry: reg}
	gotDev, gotCh := a.lookupChannel("DEV001", 1)
	if gotDev == nil {
		t.Fatal("lookupChannel found device: dev must not be nil")
	}
	// Channel DEV001:1 exists
	if gotCh == nil {
		t.Fatal("lookupChannel found channel: ch must not be nil")
	}
}

func TestLookupChannelUISchemaDeviceFoundNoChannel(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-ui2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	dev := device.New(device.Config{Address: "DEV002", InterfaceID: "HmIP-RF", Model: "TestModel"})
	// No channel added → channel lookup returns nil
	c.ModelRegistry.Put(dev)

	a := &UISchemaAdapter{registry: reg}
	gotDev, gotCh := a.lookupChannel("DEV002", 5)
	if gotDev == nil {
		t.Fatal("lookupChannel: dev must not be nil when device is found")
	}
	// Channel 5 does not exist → ch may be nil
	_ = gotCh
}

// ============================================================
// dpIsWritable — fallback to pd.IsWritable()
// ============================================================

// noIsWritableDP implements device.ParameterDataPoint but NOT IsWritable().
// This hits the fallback `return pd.IsWritable()` branch in dpIsWritable.
type noIsWritableDP struct{}

func (n *noIsWritableDP) DataPointKey() hmtypes.DataPointKey { return hmtypes.DataPointKey{} }
func (n *noIsWritableDP) Parameter() hmenum.Parameter        { return "" }
func (n *noIsWritableDP) ParameterData() hmproto.ParameterData {
	return hmproto.ParameterData{}
}
func (n *noIsWritableDP) RawValue() (any, bool) { return nil, false }
func (n *noIsWritableDP) ModifiedAt() time.Time { return time.Time{} }
func (n *noIsWritableDP) OnAnyUpdate(fn func(old, next any)) func() {
	return func() {}
}

func TestDpIsWritableFallbackDescriptor(t *testing.T) {
	t.Parallel()
	dp := &noIsWritableDP{}
	// pd says writable → fallback returns true
	pd := hmproto.ParameterData{Operations: hmenum.OperationsWrite}
	if !dpIsWritable(dp, pd) {
		t.Error("dpIsWritable fallback to descriptor (write) = false, want true")
	}
}

func TestDpIsWritableFallbackDescriptorReadOnly(t *testing.T) {
	t.Parallel()
	dp := &noIsWritableDP{}
	// pd says read-only → fallback returns false
	pd := hmproto.ParameterData{Operations: hmenum.OperationsRead}
	if dpIsWritable(dp, pd) {
		t.Error("dpIsWritable fallback to descriptor (read) = true, want false")
	}
}
