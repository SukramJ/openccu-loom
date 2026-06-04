// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmtypes

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// addressSeparator is the single character that separates a device address
// from its channel number on the wire ("ABC:1").
const addressSeparator = ':'

// ChannelAddressPattern mirrors CHANNEL_ADDRESS_PATTERN.
// Format: 5–20 alphanumeric-or-dash chars, colon, 1–3 digits.
var channelAddressPattern = regexp.MustCompile(`^[0-9a-zA-Z-]{5,20}:\d{1,3}$`)

// DeviceAddressPattern mirrors DEVICE_ADDRESS_PATTERN.
// Format: 5–20 alphanumeric-or-dash chars (no colon).
var deviceAddressPattern = regexp.MustCompile(`^[0-9a-zA-Z-]{5,20}$`)

// validParamsetKeys is the set of canonical ParamsetKey values used by
// IsParamsetKey. It mirrors Python's ParamsetKey StrEnum membership test.
var validParamsetKeys = map[hmenum.ParamsetKey]struct{}{
	hmenum.ParamsetKeyCalculated: {},
	hmenum.ParamsetKeyCombined:   {},
	hmenum.ParamsetKeyDummy:      {},
	hmenum.ParamsetKeyLink:       {},
	hmenum.ParamsetKeyMaster:     {},
	hmenum.ParamsetKeyService:    {},
	hmenum.ParamsetKeyValues:     {},
}

// ChannelAddress returns the canonical channel address string.
// A channelNo of -1 signals "no channel" (Python None) and returns
// deviceAddress unchanged. Any other value appends ":<n>".
//
// Python equivalent: support/address.get_channel_address
func ChannelAddress(deviceAddress string, channelNo int) string {
	if channelNo < 0 {
		return deviceAddress
	}
	return deviceAddress + ":" + strconv.Itoa(channelNo)
}

// ChannelNo returns the numeric channel number parsed from address.
// ok is false when the address is a plain device address (no colon part)
// or when the part after the colon is not a valid non-negative integer.
//
// Python equivalent: support/address.get_channel_no (None → ok=false)
func ChannelNo(address string) (n int, ok bool) {
	i := strings.IndexByte(address, addressSeparator)
	if i < 0 || i == len(address)-1 {
		return 0, false
	}
	v, err := strconv.Atoi(address[i+1:])
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// DeviceAddress returns the device part of address, stripping the channel
// suffix if present. "ABC:1" → "ABC"; "ABC" → "ABC".
//
// Python equivalent: support/address.get_device_address
func DeviceAddress(address string) string {
	if before, _, ok := strings.Cut(address, ":"); ok {
		return before
	}
	return address
}

// SplitChannelAddress splits address into its device part and optional
// channel number. ok is true when a channel suffix was present and
// successfully parsed. When ok is false, channelNo is -1 (the sentinel for
// "no channel", mirroring Python's None).
//
// Python equivalent: support/address.get_split_channel_address
func SplitChannelAddress(address string) (deviceAddress string, channelNo int, ok bool) {
	before, after, ok := strings.Cut(address, ":")
	if !ok {
		return address, -1, false
	}
	suffix := after
	if suffix == "" || suffix == "None" {
		return before, -1, false
	}
	v, err := strconv.Atoi(suffix)
	if err != nil || v < 0 {
		return before, -1, false
	}
	return before, v, true
}

// IsChannelAddress reports whether s matches the CCU channel-address format
// (5–20 alphanumeric/dash chars, colon, 1–3 digit channel number).
//
// Python equivalent: support/address.is_channel_address
func IsChannelAddress(s string) bool {
	return channelAddressPattern.MatchString(s)
}

// IsDeviceAddress reports whether s matches the CCU device-address format
// (5–20 alphanumeric/dash chars, no colon).
//
// Python equivalent: support/address.is_device_address
func IsDeviceAddress(s string) bool {
	return deviceAddressPattern.MatchString(s)
}

// IsParamsetKey reports whether s is a known ParamsetKey value.
// It accepts both hmenum.ParamsetKey typed values and plain strings
// (mirroring Python's isinstance check against the StrEnum).
//
// Python equivalent: support/address.is_paramset_key
func IsParamsetKey(s string) bool {
	_, ok := validParamsetKeys[hmenum.ParamsetKey(s)]
	return ok
}
