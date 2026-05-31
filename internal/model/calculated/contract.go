// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package calculated

import (
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Sensor is the common contract every calculated sensor (climate-derived,
// derived-binary, voltage-level, …) implements. North-bound adapters can
// register them by their stable [hmenum.CalculatedParameter] id and let
// Subscribe wire the underlying generic data points so updates flow into the
// sensor without every adapter knowing the per-sensor source list.
//
// `CalculatedParameter.DEW_POINT`). - `Subscribe(channel)` wires the source
// DPs and returns an unsubscribe closure (mirrors `_resolve_data_point` + the
// cleanup callbacks), - `IsRefreshed()` reports whether at least one source
// has been observed (mirrors `is_refreshed`). - `StateUncertain()` aggregates
// uncertainty across all source DPs (mirrors `state_uncertain =
// any(dp.state_uncertain for dp in relevant_dps)`). -
// `LoadDataPointValue(loader)` pulls the current value from the CCU for all
// underlying source data points.
//
// `Subscribe` may be a no-op when the sensor has no auxiliary inputs — e.g. a
// derived-binary sensor that already wraps a single source DP in its
// constructor.
type Sensor interface {
	CalculatedParameter() hmenum.CalculatedParameter
	Subscribe(c *device.Channel) func()
	IsRefreshed() bool
	StateUncertain() bool
	// LoadDataPointValue triggers a CCU-side value refresh for all underlying
	// source data points. The loader function is invoked once per source
	// parameter with (channelAddress, parameterName) arguments. Implementations
	// iterate their registered source DPs and delegate the load to the supplied
	// function. A nil loader is a no-op.
	LoadDataPointValue(loader func(channelAddress, parameter string))
}

// Compile-time interface assertions. Failing the cast at build time is
// the right place to catch drift.
var (
	_ Sensor = (*DewPointSensor)(nil)
	_ Sensor = (*DewPointSpreadSensor)(nil)
	_ Sensor = (*FrostPointSensor)(nil)
	_ Sensor = (*VaporConcentrationSensor)(nil)
	_ Sensor = (*EnthalpySensor)(nil)
	_ Sensor = (*ApparentTemperatureSensor)(nil)
	_ Sensor = (*OperatingVoltageLevelSensor)(nil)
	_ Sensor = (*DerivedBinarySensor)(nil)
)
