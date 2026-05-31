// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ParamFromAnyParamset looks up a wire parameter on the channel
// across both VALUES and MASTER paramsets. Climate-internal DPs are
// split between the two — runtime state on VALUES (SET_POINT_MODE,
// ACTIVE_PROFILE, BOOST_MODE), operator-tunable config on MASTER
// (TEMPERATURE_OFFSET, OPTIMUM_START_STOP, MIN_MAX_NOT_RELEVANT_*),
// and the same pattern repeats for blinds (CHANNEL_OPERATION_MODE
// is MASTER on most IP blinds) and RGBW lights (DEVICE_OPERATION_MODE
// is MASTER on the HmIP-RGBW family). VALUES wins when both paramsets
// carry the same name (rare; the runtime value is the more
// authoritative live source).
func ParamFromAnyParamset(ch *device.Channel, p hmenum.Parameter) device.ParameterDataPoint {
	if ch == nil {
		return nil
	}
	if dp := ch.Parameter(p); dp != nil {
		return dp
	}
	return ch.MasterParameter(p)
}

// RawValuer is the minimal "snapshot of the wire DP's last observed
// value" interface needed by [ReplayCurrentValue]. Every
// [device.ParameterDataPoint] satisfies it; the helper takes the
// narrow form so cover / light / garage can replay both
// VALUES-paramset and MASTER-paramset DPs without importing each
// other's type aliases.
type RawValuer interface {
	RawValue() (any, bool)
}

// ReplayCurrentValue feeds the DP's last observed RawValue (if any) through
// `apply` immediately after a Subscribe handler has been registered.
// Custom-DP Subscribe methods register OnAnyUpdate handlers that fire only
// for *future* value changes — at boot the wire DPs already carry observed
// values from `fetch_all_device_data`, but Subscribe runs *after* fetch, so a
// pure-callback wiring would never see those initial values. Without the
// replay, a Cover that booted in a stable "level=0.6, direction=NONE"
// configuration would surface `current_position = unknown` until the next
// push event arrives — even though the CCU has already pushed the real values
// pre-Subscribe.
//
// The Go model's hot-path-cached fields need an explicit replay to land in
// the same end state.
func ReplayCurrentValue(dp RawValuer, apply func(value any)) {
	if dp == nil {
		return
	}
	if v, observed := dp.RawValue(); observed {
		apply(v)
	}
}
