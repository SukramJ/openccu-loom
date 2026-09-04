// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package valve

import (
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// init registers the [Constructor] for the IPIrrigationValve profile
// on the process-wide [custom.DefaultRegistry]. D.12.
//
// The constructor delegates to [NewIrrigation] and passes the
// channel's installed writer (via [device.Channel.Writer]). A nil
// writer is valid at construction time — the field may be set later
// once the pipeline wires the channel.
//
// *Irrigation satisfies [device.AttachableDataPoint] via its embedded
// *generic.Switch (which carries DataPointKey() backed by
// STATE / VALUES paramset).
func init() {
	custom.DefaultRegistry().MustRegisterConstructor(
		hmenum.DeviceProfileIPIrrigationValve,
		func(ch *device.Channel, group custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
			v := NewIrrigation(ch, group)
			if v == nil {
				return nil, nil
			}
			applyGroupState(v, ch, group)
			return v, nil
		},
	)

	payload.RegisterGlobalScalarArgKey("set_level", "level")
}

// applyGroupState resolves the profile's [hmenum.FieldGroupState] mapping
// (when present) to the absolute channel + parameter and binds the
// corresponding STATE data point onto the valve via
// [Irrigation.SetGroupState].
//
// The companion parameter's wire shape differs by model — read-only
// ([*generic.BinarySensor]) on most, writable ([*generic.Switch]) on a
// few — so both are tried before giving up on that channel.
func applyGroupState(v *Irrigation, ch *device.Channel, rebased custom.RebasedChannelGroupConfig) {
	if v == nil || ch == nil || ch.Device() == nil {
		return
	}
	// One resolver for the group field: it reads the group-wide block before
	// the per-channel ones, which this profile did not do (see
	// custom.ResolveGroupFieldSlot).
	param, groupCh, ok := custom.ResolveGroupFieldSlot(ch, rebased, hmenum.FieldGroupState)
	if !ok {
		return
	}
	switch dp := groupCh.Parameter(param).(type) {
	case *generic.Switch:
		v.SetGroupState(dp)
	case *generic.BinarySensor:
		v.SetGroupState(dp)
	}
}
