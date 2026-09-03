// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package hmtypes holds primitive value types shared across the daemon.
// It is deliberately small — only cross-cutting types that do not
// belong in a more specific package (hmenum, hmerr, hmproto, hmevent)
// live here.
package hmtypes

import (
	"errors"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// DataPointKey is the composite identifier of a single CCU parameter
// on a single channel of a single interface. It is the primary lookup
// key used throughout the data plane.
//
// The four-tuple is hashable and suitable for use as a Go map key.
// The zero value is not a valid key.
type DataPointKey struct {
	InterfaceID    string
	ChannelAddress string
	ParamsetKey    hmenum.ParamsetKey
	Parameter      string
}

// Sentinel errors returned by [NewDataPointKey] validation, one per
// required field, so callers can distinguish the failure with errors.Is
// instead of matching on message text.
var (
	ErrDataPointKeyInterfaceIDRequired    = errors.New("hmtypes: DataPointKey.InterfaceID is required")
	ErrDataPointKeyChannelAddressRequired = errors.New("hmtypes: DataPointKey.ChannelAddress is required")
	ErrDataPointKeyParamsetKeyRequired    = errors.New("hmtypes: DataPointKey.ParamsetKey is required")
	ErrDataPointKeyParameterRequired      = errors.New("hmtypes: DataPointKey.Parameter is required")
)

// NewDataPointKey constructs a key and validates its components.
func NewDataPointKey(interfaceID, channelAddress string, paramsetKey hmenum.ParamsetKey, parameter string) (DataPointKey, error) {
	if interfaceID == "" {
		return DataPointKey{}, ErrDataPointKeyInterfaceIDRequired
	}
	if channelAddress == "" {
		return DataPointKey{}, ErrDataPointKeyChannelAddressRequired
	}
	if paramsetKey == "" {
		return DataPointKey{}, ErrDataPointKeyParamsetKeyRequired
	}
	if parameter == "" {
		return DataPointKey{}, ErrDataPointKeyParameterRequired
	}
	return DataPointKey{
		InterfaceID:    interfaceID,
		ChannelAddress: channelAddress,
		ParamsetKey:    paramsetKey,
		Parameter:      parameter,
	}, nil
}

// DeviceAddress returns the channel's device address. "ABC:1" → "ABC".
// Returns the channel address unchanged if it has no colon (shouldn't
// happen in practice; devices always come back as addr:channel).
// Delegates to the package-level [DeviceAddress] helper so both entry
// points share one parsing rule.
func (k DataPointKey) DeviceAddress() string {
	return DeviceAddress(k.ChannelAddress)
}

// ChannelNo returns the numeric channel number or ok=false if the
// address doesn't look like "<addr>:<n>". Delegates to the package-level
// [ChannelNo] helper so both entry points share one parsing rule.
func (k DataPointKey) ChannelNo() (int, bool) {
	return ChannelNo(k.ChannelAddress)
}

// String returns a stable, human-readable form suitable for logs.
// Format: "<interface>|<channel_address>|<paramset_key>|<parameter>".
func (k DataPointKey) String() string {
	return k.InterfaceID + "|" + k.ChannelAddress + "|" + string(k.ParamsetKey) + "|" + k.Parameter
}
