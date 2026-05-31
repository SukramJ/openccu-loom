// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// configuration_coordinator_paramset_copy_test.go covers:
// CopyParamset (nil guards, empty-source, filter-missing, filter-non-writable,
// successful copy), GetAllParamsetDescriptions (multi-key return, empty
// channel), GetConfigurableDevices (single device with channel, MAINTENANCE
// filter, sorted order), result structs (CopyParamsetResult, PutParamsetResult,
// MaintenanceData field semantics), and ConfigurableDeviceChannel struct
// validation.

package coordinators

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ─────────────────────────────────────────────────────────────────────────────
// Stubs for LiveParamsetReader / LiveParamsetWriter
// ─────────────────────────────────────────────────────────────────────────────

type stubParamsetReader struct {
	values map[string]any
	err    error
}

func (s *stubParamsetReader) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]any, len(s.values))
	for k, v := range s.values {
		out[k] = v
	}
	return out, nil
}

type stubParamsetWriter struct {
	written map[string]any
	err     error
	called  bool
}

func (s *stubParamsetWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any) error {
	s.called = true
	if s.err != nil {
		return s.err
	}
	s.written = values
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// CopyParamset — nil reader / nil writer guards
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityCopyParamsetNilReaderReturnsError(t *testing.T) {
	t.Parallel()
	c, _, _ := newTestConfCoord()
	writer := &stubParamsetWriter{}

	_, _, _, err := c.CopyParamset(
		context.Background(),
		testIface, testChannel,
		testIface, testChannel,
		hmenum.ParamsetKeyMaster,
		nil, writer,
	)
	if err == nil {
		t.Fatal("nil reader must return an error")
	}
}

func TestConfigurationParityCopyParamsetNilWriterReturnsError(t *testing.T) {
	t.Parallel()
	c, _, _ := newTestConfCoord()
	reader := &stubParamsetReader{}

	_, _, _, err := c.CopyParamset(
		context.Background(),
		testIface, testChannel,
		testIface, testChannel,
		hmenum.ParamsetKeyMaster,
		reader, nil,
	)
	if err == nil {
		t.Fatal("nil writer must return an error")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CopyParamset — empty source
// Mirrors test_configuration_coordinator.py::TestCopyParamset::test_empty_source
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityCopyParamsetEmptySourceSucceeds(t *testing.T) {
	t.Parallel()
	descs := registry.NewDeviceDescriptionRegistry()
	pss := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	const dst = "DST:1"
	pss.Put(testIface, dst, hmenum.ParamsetKeyMaster, hmproto.Paramset{})

	c := NewConfigurationCoordinator(descs, pss, devs)
	reader := &stubParamsetReader{values: map[string]any{}}
	writer := &stubParamsetWriter{}

	result, _, _, err := c.CopyParamset(
		context.Background(),
		testIface, "SRC:1",
		testIface, dst,
		hmenum.ParamsetKeyMaster,
		reader, writer,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatal("empty source copy must succeed")
	}
	if result.ParametersCopied != 0 || result.ParametersSkipped != 0 {
		t.Fatalf("empty source: copied=%d skipped=%d, both want 0", result.ParametersCopied, result.ParametersSkipped)
	}
	if writer.called {
		t.Fatal("writer must not be called when source is empty")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CopyParamset — filters parameters missing from target description
// Mirrors test_filters_missing_params
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityCopyParamsetFiltersMissingParams(t *testing.T) {
	t.Parallel()
	descs := registry.NewDeviceDescriptionRegistry()
	pss := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	const dst = "DST2:1"
	// Target description has PARAM_A but not PARAM_B.
	pss.Put(testIface, dst, hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"PARAM_A": hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})

	c := NewConfigurationCoordinator(descs, pss, devs)
	reader := &stubParamsetReader{values: map[string]any{"PARAM_A": 1, "PARAM_B": 2}}
	writer := &stubParamsetWriter{}

	result, _, _, err := c.CopyParamset(
		context.Background(),
		testIface, "SRC2:1",
		testIface, dst,
		hmenum.ParamsetKeyMaster,
		reader, writer,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ParametersCopied != 1 {
		t.Fatalf("want 1 copied, got %d", result.ParametersCopied)
	}
	if result.ParametersSkipped != 1 {
		t.Fatalf("want 1 skipped (PARAM_B not in target), got %d", result.ParametersSkipped)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CopyParamset — filters non-writable parameters
// Mirrors test_filters_non_writable
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityCopyParamsetFiltersNonWritable(t *testing.T) {
	t.Parallel()
	descs := registry.NewDeviceDescriptionRegistry()
	pss := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	const dst = "DST3:1"
	pss.Put(testIface, dst, hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"WRITABLE": hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
		"READONLY": hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead,
		},
	})

	c := NewConfigurationCoordinator(descs, pss, devs)
	reader := &stubParamsetReader{values: map[string]any{"WRITABLE": 10, "READONLY": 20}}
	writer := &stubParamsetWriter{}

	result, _, _, err := c.CopyParamset(
		context.Background(),
		testIface, "SRC3:1",
		testIface, dst,
		hmenum.ParamsetKeyMaster,
		reader, writer,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatal("copy must succeed")
	}
	if result.ParametersCopied != 1 {
		t.Fatalf("want 1 copied (WRITABLE only), got %d", result.ParametersCopied)
	}
	if result.ParametersSkipped != 1 {
		t.Fatalf("want 1 skipped (READONLY), got %d", result.ParametersSkipped)
	}
	if _, ok := writer.written["WRITABLE"]; !ok {
		t.Fatal("WRITABLE must appear in writer payload")
	}
	if _, ok := writer.written["READONLY"]; ok {
		t.Fatal("READONLY must not appear in writer payload")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CopyParamset — reader error propagates
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityCopyParamsetReaderErrorPropagates(t *testing.T) {
	t.Parallel()
	c, _, _ := newTestConfCoord()
	readerErr := errors.New("reader failure")
	reader := &stubParamsetReader{err: readerErr}
	writer := &stubParamsetWriter{}

	_, _, _, err := c.CopyParamset(
		context.Background(),
		testIface, testChannel,
		testIface, testChannel,
		hmenum.ParamsetKeyValues,
		reader, writer,
	)
	if !errors.Is(err, readerErr) {
		t.Fatalf("want readerErr propagated, got %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CopyParamset — writer error propagates; result.Success = false
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityCopyParamsetWriterErrorReportsFailure(t *testing.T) {
	t.Parallel()
	descs := registry.NewDeviceDescriptionRegistry()
	pss := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	const dst = "DST4:1"
	pss.Put(testIface, dst, hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"PARAM": hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})

	c := NewConfigurationCoordinator(descs, pss, devs)
	writerErr := errors.New("write failed")
	reader := &stubParamsetReader{values: map[string]any{"PARAM": 42}}
	writer := &stubParamsetWriter{err: writerErr}

	result, _, _, err := c.CopyParamset(
		context.Background(),
		testIface, "SRC4:1",
		testIface, dst,
		hmenum.ParamsetKeyMaster,
		reader, writer,
	)
	if err == nil || !errors.Is(err, writerErr) {
		t.Fatalf("want writerErr, got %v", err)
	}
	if result.Success {
		t.Fatal("result.Success must be false on writer error")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CopyParamset — no target description: all params skipped
// Mirrors the case where dstDesc lookup returns ok=false.
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityCopyParamsetNoTargetDescriptionSkipsAll(t *testing.T) {
	t.Parallel()
	descs := registry.NewDeviceDescriptionRegistry()
	pss := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()
	// No registration for target channel — GetChannelParamset returns ok=false.
	c := NewConfigurationCoordinator(descs, pss, devs)
	reader := &stubParamsetReader{values: map[string]any{"PARAM": 1}}
	writer := &stubParamsetWriter{}

	result, _, _, err := c.CopyParamset(
		context.Background(),
		testIface, "SRC5:1",
		testIface, "UNKNOWN_DST:1",
		hmenum.ParamsetKeyMaster,
		reader, writer,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatal("no-target case must still succeed")
	}
	if result.ParametersCopied != 0 {
		t.Fatalf("copied must be 0 when target has no description, got %d", result.ParametersCopied)
	}
	if result.ParametersSkipped != 1 {
		t.Fatalf("skipped must equal source length (%d), got %d", 1, result.ParametersSkipped)
	}
	if writer.called {
		t.Fatal("writer must not be called when target has no description")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GetAllParamsetDescriptions — multi-key return
// Mirrors TestGetAllParamsetDescriptions::test_returns_all_descriptions
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityGetAllParamsetDescriptionsReturnsAllKeys(t *testing.T) {
	t.Parallel()
	descs := registry.NewDeviceDescriptionRegistry()
	pss := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	const ch = "ALLPS:1"
	pss.Put(testIface, ch, hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"TEMP_MIN": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	})
	pss.Put(testIface, ch, hmenum.ParamsetKeyValues, hmproto.Paramset{
		"ACTUAL_TEMPERATURE": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	})

	c := NewConfigurationCoordinator(descs, pss, devs)
	got := c.GetAllParamsetDescriptions(testIface, ch)

	if _, ok := got[hmenum.ParamsetKeyMaster]; !ok {
		t.Fatal("MASTER must be in GetAllParamsetDescriptions result")
	}
	if _, ok := got[hmenum.ParamsetKeyValues]; !ok {
		t.Fatal("VALUES must be in GetAllParamsetDescriptions result")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GetConfigurableDevices — single device with configurable channel
// Mirrors TestGetConfigurableDevices::test_device_with_channels
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityGetConfigurableDevicesReturnsSingleDevice(t *testing.T) {
	t.Parallel()
	descs := registry.NewDeviceDescriptionRegistry()
	pss := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	descs.Put(testIface, hmproto.DeviceDescription{Address: "DEV0001", Type: "HmIP-BSM"})
	descs.Put(testIface, hmproto.DeviceDescription{Address: "DEV0001:1", Parent: "DEV0001", Type: "SWITCH"})
	pss.Put(testIface, "DEV0001:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"ON_TIME": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	})

	c := NewConfigurationCoordinator(descs, pss, devs)
	result := c.GetConfigurableDevices(testIface)

	if len(result) != 1 {
		t.Fatalf("want 1 configurable device, got %d", len(result))
	}
	if result[0].Address != "DEV0001" {
		t.Fatalf("device address = %q, want DEV0001", result[0].Address)
	}
	if len(result[0].Channels) != 1 {
		t.Fatalf("want 1 channel on device, got %d", len(result[0].Channels))
	}
	if result[0].Channels[0].Address != "DEV0001:1" {
		t.Fatalf("channel address = %q, want DEV0001:1", result[0].Channels[0].Address)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GetConfigurableDevices — groups multiple channels under one device
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityGetConfigurableDevicesGroupsChannels(t *testing.T) {
	t.Parallel()
	descs := registry.NewDeviceDescriptionRegistry()
	pss := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	descs.Put(testIface, hmproto.DeviceDescription{Address: "GRP0001", Type: "HmIP-4BS"})
	for _, addr := range []string{"GRP0001:1", "GRP0001:2"} {
		descs.Put(testIface, hmproto.DeviceDescription{Address: addr, Parent: "GRP0001", Type: "GENERIC"})
		pss.Put(testIface, addr, hmenum.ParamsetKeyMaster, hmproto.Paramset{
			"X": hmproto.ParameterData{Type: hmenum.ParameterTypeBool},
		})
	}

	c := NewConfigurationCoordinator(descs, pss, devs)
	result := c.GetConfigurableDevices(testIface)

	if len(result) != 1 {
		t.Fatalf("want 1 device aggregate, got %d", len(result))
	}
	if len(result[0].Channels) != 2 {
		t.Fatalf("want 2 channels under device, got %d", len(result[0].Channels))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GetConfigurableDevices — sorted by address
// Mirrors TestGetConfigurableChannels::test_sorted_by_address
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityGetConfigurableDevicesSortedByAddress(t *testing.T) {
	t.Parallel()
	descs := registry.NewDeviceDescriptionRegistry()
	pss := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	// Insert in reverse order.
	for _, addr := range []string{"ZZZ0003", "AAA0001", "MMM0002"} {
		descs.Put(testIface, hmproto.DeviceDescription{Address: addr, Type: "GENERIC"})
		chAddr := addr + ":1"
		descs.Put(testIface, hmproto.DeviceDescription{Address: chAddr, Parent: addr, Type: "CHAN"})
		pss.Put(testIface, chAddr, hmenum.ParamsetKeyMaster, hmproto.Paramset{
			"Y": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
		})
	}

	c := NewConfigurationCoordinator(descs, pss, devs)
	result := c.GetConfigurableDevices(testIface)

	if len(result) != 3 {
		t.Fatalf("want 3 devices, got %d", len(result))
	}
	want := []string{"AAA0001", "MMM0002", "ZZZ0003"}
	for i, w := range want {
		if result[i].Address != w {
			t.Fatalf("result[%d].Address = %q, want %q", i, result[i].Address, w)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ConfigurableChannel — MAINTENANCE channel without MASTER is excluded
// Mirrors test_skips_hidden_channels_without_master
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityConfigurableChannelsExcludesMaintenanceWithoutMaster(t *testing.T) {
	t.Parallel()
	descs := registry.NewDeviceDescriptionRegistry()
	pss := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	descs.Put(testIface, hmproto.DeviceDescription{Address: "MNT0001", Type: "ROOT"})
	descs.Put(testIface, hmproto.DeviceDescription{Address: "MNT0001:0", Parent: "MNT0001", Type: "MAINTENANCE"})
	descs.Put(testIface, hmproto.DeviceDescription{Address: "MNT0001:1", Parent: "MNT0001", Type: "SWITCH"})

	// Channel :0 only has VALUES (no MASTER); channel :1 has MASTER.
	pss.Put(testIface, "MNT0001:0", hmenum.ParamsetKeyValues, hmproto.Paramset{
		"UNREACH": hmproto.ParameterData{Type: hmenum.ParameterTypeBool},
	})
	pss.Put(testIface, "MNT0001:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"ON_TIME": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	})

	c := NewConfigurationCoordinator(descs, pss, devs)
	channels := c.ConfigurableChannels(testIface)

	if len(channels) != 1 {
		t.Fatalf("want 1 configurable channel (SWITCH only), got %d: %+v", len(channels), channels)
	}
	if channels[0].ChannelAddress != "MNT0001:1" {
		t.Fatalf("channel = %q, want MNT0001:1", channels[0].ChannelAddress)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CopyParamsetResult struct — mirrors TestCopyParamsetResult::test_creation
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityCopyParamsetResultFields(t *testing.T) {
	t.Parallel()
	r := CopyParamsetResult{Success: true, ParametersCopied: 3, ParametersSkipped: 1}
	if !r.Success {
		t.Fatal("Success must be true")
	}
	if r.ParametersCopied != 3 {
		t.Fatalf("ParametersCopied = %d, want 3", r.ParametersCopied)
	}
	if r.ParametersSkipped != 1 {
		t.Fatalf("ParametersSkipped = %d, want 1", r.ParametersSkipped)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PutParamsetResult struct — mirrors TestPutParamsetResult tests
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityPutParamsetResultSuccess(t *testing.T) {
	t.Parallel()
	r := PutParamsetResult{Success: true, ParametersWritten: 2, ValidationErrors: nil}
	if !r.Success {
		t.Fatal("Success must be true")
	}
	if r.ParametersWritten != 2 {
		t.Fatalf("ParametersWritten = %d, want 2", r.ParametersWritten)
	}
}

func TestConfigurationParityPutParamsetResultFailureWithErrors(t *testing.T) {
	t.Parallel()
	r := PutParamsetResult{
		Success:          false,
		ValidationErrors: map[string]string{"TEMP": "Value 99 is above maximum 30.5"},
	}
	if r.Success {
		t.Fatal("Success must be false")
	}
	if r.ValidationErrors["TEMP"] == "" {
		t.Fatal("TEMP must be in ValidationErrors")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// MaintenanceData struct — mirrors TestMaintenanceData tests
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityMaintenanceDataFields(t *testing.T) {
	t.Parallel()
	m := MaintenanceData{UnreachCount: 0, StickyUnreach: false, LowBat: true, RSSI: -65}
	if m.LowBat != true {
		t.Fatal("LowBat must be true")
	}
	if m.RSSI != -65 {
		t.Fatalf("RSSI = %d, want -65", m.RSSI)
	}
}

func TestConfigurationParityMaintenanceDataZeroValue(t *testing.T) {
	t.Parallel()
	m := MaintenanceData{}
	if m.UnreachCount != 0 || m.StickyUnreach || m.LowBat || m.RSSI != 0 || m.Error != 0 {
		t.Fatalf("zero-value MaintenanceData has unexpected fields: %+v", m)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ConfigurableDeviceChannel struct — mirrors TestConfigurableDeviceChannel
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityConfigurableDeviceChannelStruct(t *testing.T) {
	t.Parallel()
	ch := ConfigurableDeviceChannel{
		Address:      "VCU:1",
		ChannelType:  "SWITCH",
		ParamsetKeys: []hmenum.ParamsetKey{hmenum.ParamsetKeyMaster, hmenum.ParamsetKeyValues},
	}
	if ch.Address != "VCU:1" {
		t.Fatalf("Address = %q, want VCU:1", ch.Address)
	}
	if ch.ChannelType != "SWITCH" {
		t.Fatalf("ChannelType = %q, want SWITCH", ch.ChannelType)
	}
	if len(ch.ParamsetKeys) != 2 {
		t.Fatalf("ParamsetKeys len = %d, want 2", len(ch.ParamsetKeys))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ConfigurableDevice struct — mirrors TestConfigurableDevice
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationParityConfigurableDeviceStruct(t *testing.T) {
	t.Parallel()
	dev := ConfigurableDevice{
		Address:     "VCU0000001",
		InterfaceID: "HmIP-RF",
		Model:       "HmIP-BSM",
		Name:        "Test Switch",
	}
	if dev.Address != "VCU0000001" {
		t.Fatalf("Address = %q, want VCU0000001", dev.Address)
	}
	if dev.Model != "HmIP-BSM" {
		t.Fatalf("Model = %q, want HmIP-BSM", dev.Model)
	}
}
