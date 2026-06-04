// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package hmtypes holds primitive value types shared across the daemon.
// It is deliberately small — only cross-cutting types that do not
// belong in a more specific package (hmenum, hmerr, hmproto, hmevent)
// live here.
package hmtypes

import (
	"errors"
	"strings"

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

// NewDataPointKey constructs a key and validates its components.
func NewDataPointKey(interfaceID, channelAddress string, paramsetKey hmenum.ParamsetKey, parameter string) (DataPointKey, error) {
	if interfaceID == "" {
		return DataPointKey{}, errors.New("hmtypes: DataPointKey.InterfaceID is required")
	}
	if channelAddress == "" {
		return DataPointKey{}, errors.New("hmtypes: DataPointKey.ChannelAddress is required")
	}
	if paramsetKey == "" {
		return DataPointKey{}, errors.New("hmtypes: DataPointKey.ParamsetKey is required")
	}
	if parameter == "" {
		return DataPointKey{}, errors.New("hmtypes: DataPointKey.Parameter is required")
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
func (k DataPointKey) DeviceAddress() string {
	if i := strings.IndexByte(k.ChannelAddress, ':'); i >= 0 {
		return k.ChannelAddress[:i]
	}
	return k.ChannelAddress
}

// ChannelNo returns the numeric channel number or ok=false if the
// address doesn't look like "<addr>:<n>".
func (k DataPointKey) ChannelNo() (int, bool) {
	i := strings.IndexByte(k.ChannelAddress, ':')
	if i < 0 || i == len(k.ChannelAddress)-1 {
		return 0, false
	}
	var n int
	for _, c := range k.ChannelAddress[i+1:] {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// String returns a stable, human-readable form suitable for logs.
// Format: "<interface>|<channel_address>|<paramset_key>|<parameter>".
func (k DataPointKey) String() string {
	return k.InterfaceID + "|" + k.ChannelAddress + "|" + string(k.ParamsetKey) + "|" + k.Parameter
}
