// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package configui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"
)

// ExportVersion is the only version string recognised by [ImportConfiguration].
const ExportVersion = "1.0"

// ParamsetReader is a local provider interface that supplies a channel's
// current paramset values. Implementations live outside this package
// (e.g. internal/central); tests provide a fake.
//
// CentralName scopes the lookup to one of the potentially many active
// Units (multi-CCU safe).
type ParamsetReader interface {
	// ReadParamset fetches the named paramset (usually "MASTER" or "VALUES")
	// for the given channel address on the given central.
	// Returns a map of parameter name → current value.
	ReadParamset(ctx context.Context, centralName, channelAddress, paramsetKey string) (map[string]any, error)
}

// ParamsetWriter is a local provider interface that applies a paramset
// diff back to the CCU.
type ParamsetWriter interface {
	// WriteParamset applies values to the named paramset on the given
	// channel address on the given central.
	WriteParamset(ctx context.Context, centralName, channelAddress, paramsetKey string, values map[string]any) error
}

// ExportedConfiguration is a serialisable snapshot of one channel's
// paramset. It mirrors the reference config panel's ExportedConfiguration
// 1:1 in field semantics; the JSON tag names are kept identical so
// payloads are interchangeable with the Python implementation.
type ExportedConfiguration struct {
	// Version identifies the serialisation format.  Always "1.0" for
	// configurations produced by this package.
	Version string `json:"version"`

	// ExportedAt is the UTC instant at which the snapshot was taken.
	ExportedAt time.Time `json:"exported_at"`

	// CentralName scopes the snapshot to one CCU (multi-CCU safe).
	// Empty string means "default / only central" for single-CCU setups.
	CentralName string `json:"central_name,omitempty"`

	// DeviceAddress is the serial / address of the parent device,
	// e.g. "0001ABCD".
	DeviceAddress string `json:"device_address"`

	// Model is the device model string, e.g. "HmIP-eTRV-2".
	Model string `json:"model"`

	// ChannelAddress is the full channel address including channel index,
	// e.g. "0001ABCD:1".
	ChannelAddress string `json:"channel_address"`

	// ChannelType is the functional type of the channel,
	// e.g. "CLIMATE_TRANSCEIVER".
	ChannelType string `json:"channel_type"`

	// ParamsetKey names the paramset that was exported,
	// typically "MASTER" or "VALUES".
	ParamsetKey string `json:"paramset_key"`

	// Values holds the parameter name → value mapping captured at export
	// time.  Values are untyped (any) because the CCU wire types are
	// heterogeneous (bool, int, float64, string).
	Values map[string]any `json:"values"`
}

// Validate checks that all required fields are non-empty and that the
// version string is supported.  It is a deliberate stand-in for
// Pydantic's model validators: call it after unmarshalling untrusted
// input.
func (e *ExportedConfiguration) Validate() error {
	if e.Version != ExportVersion {
		return fmt.Errorf("configui/exporter: unsupported version %q (expected %q)", e.Version, ExportVersion)
	}
	if e.DeviceAddress == "" {
		return errors.New("configui/exporter: DeviceAddress is required")
	}
	if e.Model == "" {
		return errors.New("configui/exporter: Model is required")
	}
	if e.ChannelAddress == "" {
		return errors.New("configui/exporter: ChannelAddress is required")
	}
	if e.ChannelType == "" {
		return errors.New("configui/exporter: ChannelType is required")
	}
	if e.ParamsetKey == "" {
		return errors.New("configui/exporter: ParamsetKey is required")
	}
	if e.ExportedAt.IsZero() {
		return errors.New("configui/exporter: ExportedAt is required")
	}
	return nil
}

// ExportInput carries all arguments required to produce an
// [ExportedConfiguration].  Values is optional: when nil the reader is
// called to fetch live data from the CCU.
type ExportInput struct {
	CentralName    string
	DeviceAddress  string
	Model          string
	ChannelAddress string
	ChannelType    string
	ParamsetKey    string

	// Reader is used when Values is nil.  May be nil only when Values is
	// already populated.
	Reader ParamsetReader

	// Values may pre-populate the snapshot; if non-nil the Reader is
	// not called.
	Values map[string]any
}

// ExportConfiguration builds an [ExportedConfiguration] from the given
// input.  If Values is not pre-populated, it fetches them via
// Reader.ReadParamset.  ctx cancellation is propagated.
func ExportConfiguration(ctx context.Context, in ExportInput) (*ExportedConfiguration, error) {
	values := in.Values
	if values == nil {
		if in.Reader == nil {
			return nil, errors.New("configui/exporter: Reader must not be nil when Values is not provided")
		}
		var err error
		values, err = in.Reader.ReadParamset(ctx, in.CentralName, in.ChannelAddress, in.ParamsetKey)
		if err != nil {
			return nil, fmt.Errorf("configui/exporter: read paramset: %w", err)
		}
	}

	// Shallow-copy values so the returned struct is independent of the
	// caller's map.
	snapshot := make(map[string]any, len(values))
	maps.Copy(snapshot, values)

	cfg := &ExportedConfiguration{
		Version:        ExportVersion,
		ExportedAt:     time.Now().UTC(),
		CentralName:    in.CentralName,
		DeviceAddress:  in.DeviceAddress,
		Model:          in.Model,
		ChannelAddress: in.ChannelAddress,
		ChannelType:    in.ChannelType,
		ParamsetKey:    in.ParamsetKey,
		Values:         snapshot,
	}
	return cfg, nil
}

// ImportConfiguration parses a JSON string produced by
// [ExportedConfiguration] (or the Python reference) and returns a
// validated struct.  It mirrors the reference config panel's import_configuration
// exactly: unknown versions are rejected, malformed JSON returns a
// descriptive error.
func ImportConfiguration(jsonData []byte) (*ExportedConfiguration, error) {
	// Decode to a map first so we can inspect the version field before
	// paying the cost of struct validation.
	var raw map[string]any
	if err := json.Unmarshal(jsonData, &raw); err != nil {
		return nil, fmt.Errorf("configui/exporter: invalid JSON: %w", err)
	}

	version, _ := raw["version"].(string)
	if version != ExportVersion {
		return nil, fmt.Errorf("configui/exporter: unsupported configuration version %q (expected %q)", version, ExportVersion)
	}

	var cfg ExportedConfiguration
	if err := json.Unmarshal(jsonData, &cfg); err != nil {
		return nil, fmt.Errorf("configui/exporter: unmarshal: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ApplyConfiguration writes the values from an [ExportedConfiguration]
// back to the CCU via writer.  ctx cancellation is propagated.
func ApplyConfiguration(ctx context.Context, cfg *ExportedConfiguration, writer ParamsetWriter) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("configui/exporter: invalid configuration: %w", err)
	}
	if writer == nil {
		return errors.New("configui/exporter: writer must not be nil")
	}
	if err := writer.WriteParamset(ctx, cfg.CentralName, cfg.ChannelAddress, cfg.ParamsetKey, cfg.Values); err != nil {
		return fmt.Errorf("configui/exporter: write paramset: %w", err)
	}
	return nil
}
