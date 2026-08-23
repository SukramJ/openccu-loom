// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package valve

import (
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
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
			return v, nil
		},
	)

	payload.RegisterGlobalScalarArgKey("set_level", "level")
}
