// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

// Live model-object subscriptions of the snapshot path.
//
// A snapshot pass walks the model and wires callbacks onto the objects it
// finds — the week-profile pointer, the climate / simple schedules, the
// channel-lock bitfield, the firmware tracker, the combined data points. The
// pass is not a one-off: the MQTT lifecycle re-runs it on every broker
// reconnect, each central re-runs it when its readiness gate clears, and a
// hot-plugged device re-runs its own. Wiring the callback unconditionally on
// every pass stacked one more subscription on the same object each time, so a
// single value change fanned out N times to the broker after N reconnects and
// the release list grew without bound until the bridge stopped.
//
// The fix cannot key on the address alone. A mid-life re-ingest rebuilds the
// Channel and everything hanging off it (see [DevicePipeline.Ingest], which
// constructs a fresh [device.NewChannel] and publishes it over the previous
// one), so "we already subscribed at 0001ABCD:1" would leave the replacement
// objects — the ones the model now hands to every reader — with no
// subscription at all. That is silent delivery loss, strictly worse than the
// leak.
//
// So the key is the model slot and the value carries the identity of the
// object the callback sits on: the same object is subscribed once no matter
// how often a pass revisits it, while a replacement at the same slot takes
// over the slot and the callback on the object it replaced is released.

// liveSubKind names one of the callbacks a snapshot pass installs. It is part
// of the key so several callbacks on the same object stay independent — a
// week profile carries the profile pointer, the Zeitplan attributes and the
// per-target schedule switches at once.
type liveSubKind string

const (
	liveSubWeekProfilePointer liveSubKind = "week_profile_pointer"
	liveSubClimateSchedule    liveSubKind = "climate_schedule"
	liveSubSimpleSchedule     liveSubKind = "simple_schedule"
	liveSubChannelLocks       liveSubKind = "channel_locks"
	liveSubScheduleEntity     liveSubKind = "schedule_entity"
	liveSubScheduleSwitch     liveSubKind = "schedule_switch"
	liveSubFirmware           liveSubKind = "firmware"
	liveSubCombinedTimer      liveSubKind = "combined_timer"
	liveSubCombinedLevel      liveSubKind = "combined_level"
	liveSubCombinedHSColor    liveSubKind = "combined_hs_color"
)

// liveSubKey identifies the model slot one live subscription belongs to.
// Comparable by design: it is the map key.
type liveSubKey struct {
	central string
	iface   string
	device  string
	channel int
	kind    liveSubKind
	// variant separates several subscriptions of the same kind on one
	// channel. A channel may carry more than one combined data point of a
	// kind, and they are told apart by the wire parameter they wrap.
	variant string
}

// liveSub is one installed subscription.
type liveSub struct {
	// owner is the model object the callback is registered on. It is an
	// interface because the objects share no common type — the only thing
	// asked of it is identity, compared with ==. Only pointers are ever
	// stored, so the comparison is defined.
	owner any
	unsub func()
}

// subscribeOnce installs the callback subscribe returns for key, unless the
// object already carries this bridge's callback for that key.
//
// Three outcomes:
//
//   - the slot already holds a subscription on this very object — nothing
//     happens, so a reconnect that hands back the same objects does not stack
//     a second callback on them;
//   - the slot holds a subscription on a different object — the new one is
//     subscribed and the old one released, which is what a mid-life re-ingest
//     needs: it replaces the Channel and every object hanging off it, and the
//     replacements are the ones the model now serves;
//   - the slot is empty — the callback is installed.
//
// subscribe is only called when a subscription is actually needed, and never
// under startMu: an unsub is a barrier that waits for an in-flight dispatch of
// that handler, and the object's own lock is held while it fans callbacks out.
// Recording, however, happens under the lock detach() reads the registry
// under, so a pass running on the MQTT lifecycle goroutine cannot lose its
// bookkeeping to a concurrent one — and a pass that finishes after the bridge
// was torn down releases what it wired instead of leaving a callback nobody
// will ever release.
func (b *EventBridge) subscribeOnce(key liveSubKey, owner any, subscribe func() func()) {
	if b == nil || owner == nil || subscribe == nil {
		return
	}

	b.startMu.Lock()
	if !b.started {
		// Nothing would ever release it: the bridge is not running, and
		// detach() has already drained the registry.
		b.startMu.Unlock()
		return
	}
	if cur, ok := b.liveSubs[key]; ok && cur.owner == owner {
		b.startMu.Unlock()
		return
	}
	b.startMu.Unlock()

	unsub := subscribe()
	if unsub == nil {
		return
	}

	b.startMu.Lock()
	if !b.started {
		b.startMu.Unlock()
		unsub()
		return
	}
	prev, hadPrev := b.liveSubs[key]
	if hadPrev && prev.owner == owner {
		// A concurrent pass won the race for the same object. Keep its
		// registration — the callback must sit on the object exactly once.
		b.startMu.Unlock()
		unsub()
		return
	}
	if b.liveSubs == nil {
		b.liveSubs = make(map[liveSubKey]liveSub)
	}
	b.liveSubs[key] = liveSub{owner: owner, unsub: unsub}
	b.startMu.Unlock()

	if hadPrev && prev.unsub != nil {
		prev.unsub()
	}
}

// releaseLiveSubsForDevice drops every live subscription wired for one
// device of one central. Called when the model announces the device is gone:
// its objects are unreachable from the model, so no later pass can evict
// their callbacks through the replacement path above.
func (b *EventBridge) releaseLiveSubsForDevice(centralName, deviceAddr string) {
	if b == nil || deviceAddr == "" {
		return
	}
	b.startMu.Lock()
	var stale []func()
	for key, sub := range b.liveSubs {
		if key.central != centralName || key.device != deviceAddr {
			continue
		}
		delete(b.liveSubs, key)
		if sub.unsub != nil {
			stale = append(stale, sub.unsub)
		}
	}
	b.startMu.Unlock()

	for _, unsub := range stale {
		unsub()
	}
}
