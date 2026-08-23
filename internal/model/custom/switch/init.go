// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// init.go registers Constructor functions for every switch
// DeviceProfile in the process-wide custom registry ( D.12).
//
// Two profiles are covered:
//
// - IPSwitch → [Switch] (HmIP family: HmIP-PS, HmIP-BS2, HmIP-DRSI4, …)
// - RfSwitch → [Switch] (HM-LC-Sw1-* family and related BidCos-RF switches)
//
// Both profiles share the same [CustomDpSwitch] data point type
// Because the wire parameters
// (STATE, ON_TIME) are identical across HmIP and classic RF switches.
//
// *Switch satisfies [device.AttachableDataPoint] via its embedded
// *generic.Switch (which carries DataPointKey() backed by
// STATE / VALUES).
//
// # Architecture divergence — RfSwitch (WX-F2 investigation)
//
// In, DeviceProfile.RF_SWITCH exists in PROFILE_CONFIGS
// And in the PROFILE_CONFIGS
// catalogue (line 931), but *no* device model is ever registered for it
// in DeviceProfileRegistry._configs. Classic BidCos-RF switches such as
// HM-LC-Sw1-Pl, HM-LC-Sw1-FM, HM-LC-Sw2-FM, HM-LC-Sw4-DR etc. fall
// Through to generic data points — their STATE parameter
// is exposed as a plain DpSwitch, not as a custom data point.
//
// Consequently, generated_profiles.go contains no RfSwitch entries:
// the generator faithfully mirrors DeviceProfileRegistry._configs, and
// that registry has zero RF_SWITCH rows. This is correct behaviour — no
// generator bug, no conversion bug (Option A, not B or C).
//
// The rfSwitchConstructor registration below is therefore future-facing:
// It pre-wires the constructor so that if a future
// adds HM-LC-Sw* device registrations for RF_SWITCH, the re-generated
// generated_profiles.go will work immediately without touching this file.
// It is NOT dead code — it is a forward-compatible hook. The constructor
// itself is cheap (no allocation until a matching profile appears in the
// registry) and does not affect runtime for devices that have no
// RfSwitch profile row.

package switchdev

import (
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func init() {
	reg := custom.DefaultRegistry()

	reg.MustRegisterConstructor(hmenum.DeviceProfileIPSwitch, ipSwitchConstructor)
	reg.MustRegisterConstructor(hmenum.DeviceProfileRfSwitch, rfSwitchConstructor)
	reg.MustRegisterConstructor(hmenum.DeviceProfileIPAccessPermission, ipAccessPermissionConstructor)

	// Pre-populate the package-level scalar-arg key table so north-bound
	// adapters can resolve the key before any device has materialised. The
	// same mapping is re-applied per Source by
	// [AccessPermission.registerServices].
	payload.RegisterGlobalScalarArgKey(serviceAccessPermission, argAccessPermission)
}

// ipAccessPermissionConstructor materialises the per-user access-permission
// switch on an ACCESS_RECEIVER channel (HmIP-DLD / HmIP-FWI). Returns nil
// when the channel carries neither the read-only STATE nor the un-ignored
// write-only ACCESS_AUTHORIZATION control, so the materializer skips it.
func ipAccessPermissionConstructor(ch *device.Channel, group custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	ap := NewAccessPermission(ch, group)
	if ap == nil {
		return nil, nil //nolint:nilnil // required field absent — no custom DP, reference parity
	}
	return ap, nil
}

func ipSwitchConstructor(ch *device.Channel, group custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	sw := New(ch, group)
	if sw == nil {
		return nil, nil
	}
	applyGroupState(sw, ch, group)
	return sw, nil
}

func rfSwitchConstructor(ch *device.Channel, group custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	sw := New(ch, group)
	if sw == nil {
		return nil, nil
	}
	applyGroupState(sw, ch, group)
	return sw, nil
}

// applyGroupState resolves the profile's [hmenum.FieldGroupState] mapping
// (when present) to the absolute channel + parameter and binds the
// corresponding STATE data point onto the switch via [Switch.SetGroupState].
//
// The companion parameter's wire shape differs by model — read-only
// ([*generic.BinarySensor]) on most, writable ([*generic.Switch]) on a
// few — so both are tried before giving up on that channel.
func applyGroupState(sw *Switch, ch *device.Channel, rebased custom.RebasedChannelGroupConfig) {
	if sw == nil || ch == nil || ch.Device() == nil {
		return
	}
	for chNo, fields := range rebased.ChannelFields {
		fv, ok := fields[hmenum.FieldGroupState]
		if !ok {
			continue
		}
		param, _ := custom.ResolveFieldValue(fv)
		if param == "" {
			continue
		}
		var groupCh *device.Channel
		for _, sibling := range ch.Device().Channels() {
			if sibling.Number == chNo {
				groupCh = sibling
				break
			}
		}
		if groupCh == nil {
			continue
		}
		switch dp := groupCh.Parameter(param).(type) {
		case *generic.Switch:
			sw.SetGroupState(dp)
			return
		case *generic.BinarySensor:
			sw.SetGroupState(dp)
			return
		}
	}
}

// Compile-time assertions: *Switch and *AccessPermission satisfy
// [device.AttachableDataPoint].
var (
	_ device.AttachableDataPoint = (*Switch)(nil)
	_ device.AttachableDataPoint = (*AccessPermission)(nil)
)
