// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package configui

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestApplyConfigurationNilWriter verifies that passing a nil writer
// returns an error rather than panicking.
func TestApplyConfigurationNilWriter(t *testing.T) {
	t.Parallel()

	cfg := validExportedConfiguration()
	if err := ApplyConfiguration(context.Background(), cfg, nil); err == nil {
		t.Fatal("expected error when writer is nil, got nil")
	}
}

// TestApplyConfigurationWriterError verifies that a writer error is
// propagated wrapped.
func TestApplyConfigurationWriterError(t *testing.T) {
	t.Parallel()

	w := &fakeParamsetWriter{err: errors.New("CCU unreachable")}
	cfg := validExportedConfiguration()
	if err := ApplyConfiguration(context.Background(), cfg, w); err == nil {
		t.Fatal("expected error propagation from writer, got nil")
	}
}

// TestApplyConfigurationInvalidConfig checks that an invalid
// ExportedConfiguration is rejected before the writer is called.
func TestApplyConfigurationInvalidConfig(t *testing.T) {
	t.Parallel()

	cfg := validExportedConfiguration()
	cfg.ChannelAddress = "" // invalidate
	w := &fakeParamsetWriter{}
	if err := ApplyConfiguration(context.Background(), cfg, w); err == nil {
		t.Fatal("expected error for invalid config, got nil")
	}
	if w.capturedCentral != "" {
		t.Fatal("writer should not have been called for an invalid config")
	}
}

// TestImportConfigurationWithValidateFailure exercises the path where
// unmarshal succeeds but Validate rejects the payload (e.g. missing
// required field).
func TestImportConfigurationWithValidateFailure(t *testing.T) {
	t.Parallel()

	// Valid version but missing ChannelAddress so Validate will fail.
	payload := []byte(`{
		"version":        "` + ExportVersion + `",
		"exported_at":    "2026-04-28T12:00:00Z",
		"device_address": "0001ABCD",
		"model":          "HmIP-eTRV-2",
		"channel_address":"",
		"channel_type":   "CLIMATE_TRANSCEIVER",
		"paramset_key":   "MASTER",
		"values":         {}
	}`)

	_, err := ImportConfiguration(payload)
	if err == nil {
		t.Fatal("expected error from Validate inside ImportConfiguration, got nil")
	}
}

// --- helpers ---

// validExportedConfiguration returns a fully-populated ExportedConfiguration
// that passes Validate().
func validExportedConfiguration() *ExportedConfiguration {
	return &ExportedConfiguration{
		Version:        ExportVersion,
		ExportedAt:     time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC),
		CentralName:    "ccu1",
		DeviceAddress:  "0001ABCD",
		Model:          "HmIP-eTRV-2",
		ChannelAddress: "0001ABCD:1",
		ChannelType:    "CLIMATE_TRANSCEIVER",
		ParamsetKey:    "MASTER",
		Values:         map[string]any{},
	}
}
