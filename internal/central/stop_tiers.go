// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

// StopTier controls the phase of [Unit.Stop] in which a registered hook fires.
// The tiers are ordered: Northbound fires first, External fires last.
type StopTier int

const (
	// StopTierNorthbound runs first, while every coordinator is still live.
	// North-bound adapters detach here so they can emit a final
	// availability=offline through the still-running EventBus and clients.
	StopTierNorthbound StopTier = iota

	// StopTierCoordinator runs after the south-bound clients have stopped but
	// before the EventBus subscriptions are cleared — for adapters that bridge
	// the bus and need it still addressable during their own teardown.
	StopTierCoordinator

	// StopTierExternal runs last, after the state machine has transitioned to
	// STOPPED: pure external cleanup with no coordinator dependency (registry
	// unregister, health-tracker cleanup, metrics).
	StopTierExternal

	// stopTierCount is an unexported sentinel used to size the stopHooks array.
	// It must remain the last constant in this block.
	stopTierCount
)

// AddStopHook registers fn to be called during [Stop] at the given tier.
// Hooks within a tier run in registration order. A nil fn or an out-of-range
// tier is silently ignored. Thread-safe; hooks may be registered at any time
// before Stop is called.
func (u *Unit) AddStopHook(tier StopTier, fn func()) {
	if fn == nil {
		return
	}
	if tier < 0 || tier >= stopTierCount {
		return
	}
	u.stopHooksMu.Lock()
	u.stopHooks[tier] = append(u.stopHooks[tier], fn)
	u.stopHooksMu.Unlock()
}

// AddOnStopHook registers fn to be called after the central has transitioned
// to STOPPED during [Stop]. Hooks run in registration order. Use this to
// attach registry-level teardown (e.g. CentralRegistry.Unregister, health
// tracker deregistration) that cannot be expressed inside the central itself.
// Thread-safe; hooks may be registered at any time before Stop is called.
// This is a back-compat wrapper for [AddStopHook](StopTierExternal, fn).
func (u *Unit) AddOnStopHook(fn func()) {
	if fn == nil {
		return
	}
	u.AddStopHook(StopTierExternal, fn)
}

// fireStopTier snapshots the hooks registered for the given tier and runs them
// outside the lock so a hook may safely call back into the unit.
func (u *Unit) fireStopTier(tier StopTier) {
	u.stopHooksMu.Lock()
	hooks := u.stopHooks[tier]
	u.stopHooksMu.Unlock()
	for _, fn := range hooks {
		fn()
	}
}
