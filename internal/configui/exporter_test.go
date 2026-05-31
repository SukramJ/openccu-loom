// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

// --- fakes -----------------------------------------------------------

// fakeParamsetReader satisfies [ParamsetReader] for tests.
type fakeParamsetReader struct {
	// values is returned on each call regardless of input.
	values map[string]any
	// err, when non-nil, is returned instead of values.
	err error
}

func (f *fakeParamsetReader) ReadParamset(_ context.Context, _, _, _ string) (map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	// Return a copy to avoid aliasing surprises.
	out := make(map[string]any, len(f.values))
	for k, v := range f.values {
		out[k] = v
	}
	return out, nil
}

// fakeParamsetWriter records the last WriteParamset call.
type fakeParamsetWriter struct {
	// err, when non-nil, is returned by WriteParamset.
	err error

	capturedCentral string
	capturedChannel string
	capturedKey     string
	capturedValues  map[string]any
}

func (f *fakeParamsetWriter) WriteParamset(_ context.Context, centralName, channelAddress, paramsetKey string, values map[string]any) error {
	if f.err != nil {
		return f.err
	}
	f.capturedCentral = centralName
	f.capturedChannel = channelAddress
	f.capturedKey = paramsetKey
	f.capturedValues = values
	return nil
}

// --- helpers ---------------------------------------------------------

func sampleExportInput(reader ParamsetReader) ExportInput {
	return ExportInput{
		CentralName:    "ccu1",
		DeviceAddress:  "0001ABCD",
		Model:          "HmIP-eTRV-2",
		ChannelAddress: "0001ABCD:1",
		ChannelType:    "CLIMATE_TRANSCEIVER",
		ParamsetKey:    "MASTER",
		Reader:         reader,
	}
}

// --- Tests -----------------------------------------------------------

// TestExportImportRoundtrip verifies: Export → marshal to JSON →
// ImportConfiguration → Validate passes, values match the originals.
func TestExportImportRoundtrip(t *testing.T) {
	t.Parallel()

	want := map[string]any{
		"TEMPERATURE_OFFSET": 1.5,
		"BOOST_MODE":         false,
	}
	reader := &fakeParamsetReader{values: want}
	in := sampleExportInput(reader)

	exported, err := ExportConfiguration(context.Background(), in)
	if err != nil {
		t.Fatalf("ExportConfiguration: %v", err)
	}

	raw, err := json.Marshal(exported)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	imported, err := ImportConfiguration(raw)
	if err != nil {
		t.Fatalf("ImportConfiguration: %v", err)
	}
	if err := imported.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if imported.DeviceAddress != in.DeviceAddress {
		t.Errorf("DeviceAddress=%q want %q", imported.DeviceAddress, in.DeviceAddress)
	}
	if imported.ChannelAddress != in.ChannelAddress {
		t.Errorf("ChannelAddress=%q want %q", imported.ChannelAddress, in.ChannelAddress)
	}
	if imported.ParamsetKey != in.ParamsetKey {
		t.Errorf("ParamsetKey=%q want %q", imported.ParamsetKey, in.ParamsetKey)
	}
	if imported.Version != ExportVersion {
		t.Errorf("Version=%q want %q", imported.Version, ExportVersion)
	}
	if len(imported.Values) != len(want) {
		t.Fatalf("Values len=%d want %d", len(imported.Values), len(want))
	}
	if imported.Values["TEMPERATURE_OFFSET"] != want["TEMPERATURE_OFFSET"] {
		t.Errorf("TEMPERATURE_OFFSET=%v want %v", imported.Values["TEMPERATURE_OFFSET"], want["TEMPERATURE_OFFSET"])
	}
}

// TestValidateMissingRequiredField checks that Validate() rejects a
// configuration that is missing one of the mandatory fields.
func TestValidateMissingRequiredField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(*ExportedConfiguration)
	}{
		{"missing DeviceAddress", func(c *ExportedConfiguration) { c.DeviceAddress = "" }},
		{"missing Model", func(c *ExportedConfiguration) { c.Model = "" }},
		{"missing ChannelAddress", func(c *ExportedConfiguration) { c.ChannelAddress = "" }},
		{"missing ChannelType", func(c *ExportedConfiguration) { c.ChannelType = "" }},
		{"missing ParamsetKey", func(c *ExportedConfiguration) { c.ParamsetKey = "" }},
		{"zero ExportedAt", func(c *ExportedConfiguration) { c.ExportedAt = time.Time{} }},
	}

	base := &ExportedConfiguration{
		Version:        ExportVersion,
		ExportedAt:     time.Now().UTC(),
		DeviceAddress:  "0001ABCD",
		Model:          "HmIP-eTRV-2",
		ChannelAddress: "0001ABCD:1",
		ChannelType:    "CLIMATE_TRANSCEIVER",
		ParamsetKey:    "MASTER",
		Values:         map[string]any{},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Clone base so mutations don't bleed between cases.
			clone := *base
			tc.mutate(&clone)
			if err := clone.Validate(); err == nil {
				t.Fatalf("%s: Validate() returned nil, want error", tc.name)
			}
		})
	}
}

// TestVersionMismatchIsRejected ensures ImportConfiguration refuses an
// unknown format version.
func TestVersionMismatchIsRejected(t *testing.T) {
	t.Parallel()

	payload := []byte(`{
		"version":        "2.0",
		"exported_at":    "2026-04-28T12:00:00Z",
		"device_address": "0001ABCD",
		"model":          "HmIP-eTRV-2",
		"channel_address":"0001ABCD:1",
		"channel_type":   "CLIMATE_TRANSCEIVER",
		"paramset_key":   "MASTER",
		"values":         {}
	}`)

	_, err := ImportConfiguration(payload)
	if err == nil {
		t.Fatal("ImportConfiguration with version=2.0 must return error")
	}
}

// TestEmptyValuesIsValid checks that an exported configuration with no
// paramset values (an edge case for channels with zero writable params)
// passes both Import and Validate.
func TestEmptyValuesIsValid(t *testing.T) {
	t.Parallel()

	payload := []byte(fmt.Sprintf(`{
		"version":        %q,
		"exported_at":    "2026-04-28T12:00:00Z",
		"device_address": "0001ABCD",
		"model":          "HmIP-eTRV-2",
		"channel_address":"0001ABCD:1",
		"channel_type":   "CLIMATE_TRANSCEIVER",
		"paramset_key":   "MASTER",
		"values":         {}
	}`, ExportVersion))

	cfg, err := ImportConfiguration(payload)
	if err != nil {
		t.Fatalf("ImportConfiguration: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(cfg.Values) != 0 {
		t.Fatalf("Values=%v want empty", cfg.Values)
	}
}

// TestCtxCancelPropagatesOnExport verifies that a cancelled context
// causes ExportConfiguration to return an error when it needs to call
// the reader.
func TestCtxCancelPropagatesOnExport(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	reader := &fakeParamsetReader{
		err: ctx.Err(), // mimic a reader that checks the context
	}
	in := sampleExportInput(reader)

	_, err := ExportConfiguration(ctx, in)
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v; want to wrap context.Canceled", err)
	}
}

// TestMultiCCUScopeIsolation verifies that two ExportInputs targeting
// different centrals carry independent CentralName values and that
// ApplyConfiguration forwards the correct central name to the writer.
func TestMultiCCUScopeIsolation(t *testing.T) {
	t.Parallel()

	buildInput := func(central string) ExportInput {
		return ExportInput{
			CentralName:    central,
			DeviceAddress:  "0001ABCD",
			Model:          "HmIP-eTRV-2",
			ChannelAddress: "0001ABCD:1",
			ChannelType:    "CLIMATE_TRANSCEIVER",
			ParamsetKey:    "MASTER",
			Values:         map[string]any{"TEMPERATURE_OFFSET": 0.5},
		}
	}

	cfg1, err := ExportConfiguration(context.Background(), buildInput("ccu1"))
	if err != nil {
		t.Fatalf("ccu1 export: %v", err)
	}
	cfg2, err := ExportConfiguration(context.Background(), buildInput("ccu2"))
	if err != nil {
		t.Fatalf("ccu2 export: %v", err)
	}

	if cfg1.CentralName != "ccu1" {
		t.Errorf("cfg1.CentralName=%q want ccu1", cfg1.CentralName)
	}
	if cfg2.CentralName != "ccu2" {
		t.Errorf("cfg2.CentralName=%q want ccu2", cfg2.CentralName)
	}

	// Apply cfg1 and verify the writer receives the correct central name.
	w := &fakeParamsetWriter{}
	if err := ApplyConfiguration(context.Background(), cfg1, w); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if w.capturedCentral != "ccu1" {
		t.Errorf("writer received central=%q want ccu1", w.capturedCentral)
	}
	if w.capturedChannel != cfg1.ChannelAddress {
		t.Errorf("writer received channel=%q want %q", w.capturedChannel, cfg1.ChannelAddress)
	}
}

// TestPrePopulatedValuesSkipsReader ensures that when ExportInput.Values
// is already set the reader is never called (readers can be expensive
// CCU round trips).
func TestPrePopulatedValuesSkipsReader(t *testing.T) {
	t.Parallel()

	// A reader that always errors — if it were called the test would fail.
	reader := &fakeParamsetReader{err: errors.New("reader must not be called")}
	in := sampleExportInput(reader)
	in.Values = map[string]any{"TEMPERATURE_OFFSET": 2.0}

	exported, err := ExportConfiguration(context.Background(), in)
	if err != nil {
		t.Fatalf("ExportConfiguration: %v", err)
	}
	if exported.Values["TEMPERATURE_OFFSET"] != 2.0 {
		t.Errorf("value=%v want 2.0", exported.Values["TEMPERATURE_OFFSET"])
	}
}

// TestImportInvalidJSONReturnsError verifies that malformed JSON is
// rejected with a descriptive error (not a panic).
func TestImportInvalidJSONReturnsError(t *testing.T) {
	t.Parallel()

	_, err := ImportConfiguration([]byte(`not json at all`))
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

// TestApplyConfigurationForwardsAllFields checks that ApplyConfiguration
// passes every relevant field from the ExportedConfiguration through to
// the writer without loss.
func TestApplyConfigurationForwardsAllFields(t *testing.T) {
	t.Parallel()

	cfg := &ExportedConfiguration{
		Version:        ExportVersion,
		ExportedAt:     time.Now().UTC(),
		CentralName:    "mycentral",
		DeviceAddress:  "DEADBEEF",
		Model:          "HmIP-WTH-2",
		ChannelAddress: "DEADBEEF:1",
		ChannelType:    "CLIMATE_TRANSCEIVER",
		ParamsetKey:    "MASTER",
		Values:         map[string]any{"DECALCIFICATION": true},
	}

	w := &fakeParamsetWriter{}
	if err := ApplyConfiguration(context.Background(), cfg, w); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if w.capturedCentral != "mycentral" {
		t.Errorf("central=%q want mycentral", w.capturedCentral)
	}
	if w.capturedChannel != "DEADBEEF:1" {
		t.Errorf("channel=%q want DEADBEEF:1", w.capturedChannel)
	}
	if w.capturedKey != "MASTER" {
		t.Errorf("paramset_key=%q want MASTER", w.capturedKey)
	}
	if w.capturedValues["DECALCIFICATION"] != true {
		t.Errorf("DECALCIFICATION=%v want true", w.capturedValues["DECALCIFICATION"])
	}
}
