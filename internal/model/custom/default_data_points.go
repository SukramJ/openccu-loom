// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.
//
// The default data points every custom data point marks for creation.
// Hand-maintained — see ADR 0063.

package custom

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// DefaultDataPoints is the per-channel-offset table of generic data
// points every profile inherits unless
// [ProfileConfig.IncludeDefaultDataPoints] is false. Tuple keys in the
// source are expanded so each channel offset is its own map entry.
var DefaultDataPoints = map[int][]hmenum.Parameter{
	0: {
		hmenum.ParameterActualTemperature,
		hmenum.ParameterDutyCycle,
		hmenum.ParameterDutycycle,
		hmenum.ParameterLowBat,
		hmenum.ParameterLowbat,
		hmenum.ParameterOperatingVoltage,
		hmenum.ParameterRSSIDevice,
		hmenum.ParameterRSSIPeer,
		hmenum.ParameterSabotage,
		hmenum.ParameterTimeOfOperation,
	},
	2: {
		hmenum.ParameterBatteryState,
	},
	4: {
		hmenum.ParameterBatteryState,
	},
}
