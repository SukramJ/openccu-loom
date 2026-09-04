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
// Pass [WithForceStateChange] to force a true result (e.g. after re-subscribe).
//
// The option form is NOT the same predicate as [DewPointSensor.IsStateChange],
// which reads IsRefreshed — whether the sensor has emitted a value
// (emitState.hasLast). This form reads IsRefreshedFromSources — whether every
// registered source carries an observation. The two disagree in the window
// where all sources are observed but emitState.feed has not set hasLast yet,
// because a publish guard suppressed the emission. No production code calls
// either method today, so which predicate the option form should carry is
// undecided rather than settled: pick it when the first consumer states what
// it needs.
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
