// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import (
	"maps"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// FeatureState is one feature's availability on a central.
type FeatureState struct {
	Available bool
	// Reason is empty when Available.
	Reason hmenum.FeatureReason
	// Scope is the missing credential scope when Reason is
	// [hmenum.FeatureReasonMissingScope].
	Scope string
}

// Features answers "can this central do X right now, and if not, why". It is
// per central — not per interface backend — because what is offered depends
// on the system behind the central and, on a token-authenticated system, on
// the scopes of the daemon's credential, both of which can change at runtime.
//
// A Features value is immutable; a change builds a new one. The zero value
// reports every feature unavailable with [hmenum.FeatureReasonNotReady]: a
// central that has not finished its first bring-up offers nothing yet.
type Features struct {
	system hmenum.SystemType
	states map[hmenum.Feature]FeatureState
}

// NewFeatures builds a feature set for system from states. A key missing
// from states reads as not supported by the system.
func NewFeatures(system hmenum.SystemType, states map[hmenum.Feature]FeatureState) Features {
	return Features{system: system, states: maps.Clone(states)}
}

// SystemType reports the system the set describes; empty before the first
// bring-up.
func (f Features) SystemType() hmenum.SystemType { return f.system }

// Known reports whether the set describes a resolved system rather than the
// not-yet-ready placeholder.
func (f Features) Known() bool { return f.states != nil }

// State returns one feature's availability.
func (f Features) State(k hmenum.Feature) FeatureState {
	if f.states == nil {
		return FeatureState{Reason: hmenum.FeatureReasonNotReady}
	}
	if s, ok := f.states[k]; ok {
		return s
	}
	return FeatureState{Reason: hmenum.FeatureReasonNotSupported}
}

// Available is shorthand for State(k).Available.
func (f Features) Available(k hmenum.Feature) bool { return f.State(k).Available }

// Require returns nil when k is available, otherwise a
// [*hmerr.FeatureUnavailableError] for central that wraps legacy — the error
// the caller's existing code branches on.
func (f Features) Require(central string, k hmenum.Feature, legacy error) error {
	s := f.State(k)
	if s.Available {
		return nil
	}
	return &hmerr.FeatureUnavailableError{
		Central: central,
		Feature: k,
		Reason:  s.Reason,
		Scope:   s.Scope,
		Legacy:  legacy,
	}
}

// Equal reports whether two sets describe the same system with the same
// states.
func (f Features) Equal(o Features) bool {
	if f.system != o.system || (f.states == nil) != (o.states == nil) {
		return false
	}
	return maps.Equal(f.states, o.states)
}

// Features returns what the central can do right now.
func (u *Unit) Features() Features {
	u.featuresMu.RLock()
	defer u.featuresMu.RUnlock()
	return u.features
}

// SetFeatures records the central's feature set and publishes
// [hmevent.CentralFeaturesChangedEvent] when it differs from the previous
// one, so a bring-up that re-resolves the same set on every generation does
// not wake every north-bound surface.
func (u *Unit) SetFeatures(f Features) {
	u.featuresMu.Lock()
	changed := !u.features.Equal(f)
	u.features = f
	u.featuresMu.Unlock()
	if changed && u.EventBus != nil {
		u.EventBus.Publish(hmevent.CentralFeaturesChangedEvent{
			Base:        hmevent.NewBase(),
			CentralName: u.Name(),
		})
	}
}
