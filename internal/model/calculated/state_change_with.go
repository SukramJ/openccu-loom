// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package calculated

// StateChangeOpt is a functional option for [IsStateChangeWith].
type StateChangeOpt func(*stateChangeConfig)

type stateChangeConfig struct {
	// forceTrue causes IsStateChangeWith to return true unconditionally,
	// regardless of the sensor state. Mirrors the `force=True` kwargs path.
	forceTrue bool
}

// WithForceStateChange returns an option that forces IsStateChangeWith to
// return true. Use when an explicit state push is required regardless of
// whether the value appears to have changed (e.g. after a re-subscribe).
func WithForceStateChange() StateChangeOpt {
	return func(c *stateChangeConfig) { c.forceTrue = true }
}

// calcIsStateChangeWith is the shared implementation used by every
// calculated sensor type.
func calcIsStateChangeWith(isRefreshed, stateUncertain bool, opts []StateChangeOpt) bool {
	cfg := stateChangeConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.forceTrue {
		return true
	}
	return isRefreshed && !stateUncertain
}

// IsStateChangeWith reports whether the sensor represents a meaningful state
// change, with optional overrides supplied as functional options.
//
// Without options the method is equivalent to [DewPointSensor.IsStateChange].
// Pass [WithForceStateChange] to force a true result (e.g. after re-subscribe).
func (s *DewPointSensor) IsStateChangeWith(opts ...StateChangeOpt) bool {
	return calcIsStateChangeWith(s.IsRefreshedFromSources(), s.StateUncertain(), opts)
}

// IsStateChangeWith is the option-accepting variant for DewPointSpreadSensor.
func (s *DewPointSpreadSensor) IsStateChangeWith(opts ...StateChangeOpt) bool {
	return calcIsStateChangeWith(s.IsRefreshedFromSources(), s.StateUncertain(), opts)
}

// IsStateChangeWith is the option-accepting variant for FrostPointSensor.
func (s *FrostPointSensor) IsStateChangeWith(opts ...StateChangeOpt) bool {
	return calcIsStateChangeWith(s.IsRefreshedFromSources(), s.StateUncertain(), opts)
}

// IsStateChangeWith is the option-accepting variant for VaporConcentrationSensor.
func (s *VaporConcentrationSensor) IsStateChangeWith(opts ...StateChangeOpt) bool {
	return calcIsStateChangeWith(s.IsRefreshedFromSources(), s.StateUncertain(), opts)
}

// IsStateChangeWith is the option-accepting variant for EnthalpySensor.
func (s *EnthalpySensor) IsStateChangeWith(opts ...StateChangeOpt) bool {
	return calcIsStateChangeWith(s.IsRefreshedFromSources(), s.StateUncertain(), opts)
}

// IsStateChangeWith is the option-accepting variant for ApparentTemperatureSensor.
func (s *ApparentTemperatureSensor) IsStateChangeWith(opts ...StateChangeOpt) bool {
	return calcIsStateChangeWith(s.IsRefreshedFromSources(), s.StateUncertain(), opts)
}

// IsStateChangeWith is the option-accepting variant for OperatingVoltageLevelSensor.
func (s *OperatingVoltageLevelSensor) IsStateChangeWith(opts ...StateChangeOpt) bool {
	return calcIsStateChangeWith(s.IsRefreshedFromSources(), s.StateUncertain(), opts)
}

// IsStateChangeWith is the option-accepting variant for DerivedBinarySensor.
func (s *DerivedBinarySensor) IsStateChangeWith(opts ...StateChangeOpt) bool {
	return calcIsStateChangeWith(s.IsRefreshedFromSources(), s.StateUncertain(), opts)
}
