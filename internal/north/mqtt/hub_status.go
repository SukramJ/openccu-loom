// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"sync"
	"time"
)

// hubStatusDwell is how long a folded CCU reachability level has to hold
// before it is written to the per-CCU gate topic.
//
// It is a debounce, not a rate limit, and the difference is what makes it
// the right shape here. A CCU that flaps — a radio module restarting, a
// LAN gateway renegotiating, the daemon's own reconnect to a rebooting
// interface process — produces a burst of ConnectivityChangedEvents whose
// fold alternates. Writing each one through would put a burst of retained
// messages on the broker AND strobe every CCU-scoped entity in Home
// Assistant between available and unavailable, which is worse than either
// steady state: an operator watching the dashboard sees values blink out
// and come back, and any automation with an `unavailable` trigger fires on
// every blink. A dwell absorbs the whole flap: a level that does not
// survive the window is never written at all, so a flap that ends where it
// started puts nothing on the wire.
//
// Fifteen seconds is chosen against what it has to beat and what it costs.
// It has to beat the CCU's own reconnect cycle, which for an interface
// process restart is a handful of seconds; it costs a delay of at most one
// window before a genuinely dead CCU's entities go stale-marked, which is
// far below the interval at which an operator or an automation acts on a
// dead CCU. It is deliberately symmetric — recovery is debounced exactly
// like loss. An asymmetric gate that published `online` immediately would
// still strobe, because a flap alternates: every upward edge would reach
// the broker and only the downward ones would be held.
const hubStatusDwell = 15 * time.Second

// hubStatusTimer is the one thing [hubStatusGate] needs from a timer: the
// ability to cancel a pending fire. [time.Timer] satisfies it.
//
// It is an interface so a test can drive the dwell deterministically
// instead of sleeping past it. A test that sleeps a real fifteen seconds
// is a test nobody runs, and one that shortens the constant tests a
// different debounce than the daemon ships.
type hubStatusTimer interface{ Stop() bool }

// hubStatusGate folds per-interface reachability into one retained level
// per CCU and debounces the result.
//
// It holds no reachability state of its own: the fold is computed by the
// caller from the [hub.Connectivity] tracker that owns it, and this type
// remembers only what the BROKER HAS TAKEN, which is the state the debounce
// reasons about. Keeping it that way is what makes re-observing the level
// already on the broker cheap: it cancels a pending opposite write and does
// nothing else.
//
// That is a claim about the broker, not about this process, so it is only
// as good as the two things that can invalidate it. A write that FAILS
// never becomes a remembered level ([hubStatusGate.settle] puts the entry
// back), because a level the gate believes is retained is a level nothing
// will ever publish again. And a broker that LOST its retained store is
// what [hubStatusGate.Reset] is for: it re-opens every remembered level so
// the next observation of it seeds the topic again. The gate is registered
// as one of the bridge's runtime gates precisely so that reset is not
// something a future plane has to remember to wire — see
// [Bridge.ResetRuntimeGates].
type hubStatusGate struct {
	mu    sync.Mutex
	dwell time.Duration
	// after is [time.AfterFunc] in production. The callback runs on its own
	// goroutine, exactly as [time.AfterFunc]'s does.
	after   func(time.Duration, func()) hubStatusTimer
	entries map[string]*hubStatusEntry
}

// hubStatusEntry is one CCU's written level plus the write waiting on the
// dwell, if any.
type hubStatusEntry struct {
	// written is false until a level has reached the broker. The first
	// level is never debounced — see [hubStatusGate.observe]. It goes back
	// to false when a write fails and when [hubStatusGate.Reset] re-opens
	// the gate, and in both cases for the same reason: the broker does not
	// hold the level, so the next observation of it must seed rather than
	// dedup.
	written bool
	// level is the last level the broker took, meaningful only once
	// written.
	level bool
	// pending is the level the armed timer will write, meaningful only
	// while timer is non-nil.
	pending bool
	timer   hubStatusTimer
	// gen counts recorded levels, so a rollback of a failed write can tell
	// "nothing happened since" from "a newer write already landed" and
	// decline to clobber the latter.
	gen uint64
}

func newHubStatusGate(dwell time.Duration) *hubStatusGate {
	return &hubStatusGate{
		dwell:   dwell,
		after:   func(d time.Duration, f func()) hubStatusTimer { return time.AfterFunc(d, f) },
		entries: map[string]*hubStatusEntry{},
	}
}

// observe feeds one folded reachability level for one CCU into the gate and
// reports whether `write` was called synchronously.
//
// Three cases, and the first is the one that cannot be debounced. Home
// Assistant holds an entity unavailable until EVERY topic in its
// `availability` list has reported a payload it recognises, so an entity
// that lists this gate stays greyed out until the gate's first retained
// byte exists. Debouncing that byte would grey out every CCU-scoped entity
// for the length of the dwell on every daemon start, for no benefit: there
// is no previous level for it to flap against. So the first write for a
// CCU is immediate, and only transitions away from a written level wait.
//
// The second case is the flap-absorbing one: a level equal to what is
// already written cancels any pending opposite write and writes nothing.
// That is the whole debounce — down-then-up inside the window leaves the
// broker untouched.
//
// The third arms the dwell. An arm for a level already pending is left
// alone rather than restarted, so a storm of identical folds cannot push
// the write out indefinitely; the window starts at the first edge, not the
// last.
// `write` reports what the broker did with the level. Only a nil error
// records it — see [hubStatusGate.settle].
func (g *hubStatusGate) observe(ctx context.Context, central string, online bool, write func(context.Context, string, bool) error) bool {
	g.mu.Lock()
	e := g.entries[central]
	if e == nil {
		e = &hubStatusEntry{}
		g.entries[central] = e
	}

	switch {
	case !e.written:
		e.stopTimer()
		undo := e.record(online)
		g.mu.Unlock()
		g.settle(central, write(ctx, central, online), undo)
		return true

	case online == e.level:
		e.stopTimer()
		g.mu.Unlock()
		return false

	case e.timer != nil && e.pending == online:
		g.mu.Unlock()
		return false
	}

	e.stopTimer()
	e.pending = online
	e.timer = g.after(g.dwell, func() { g.fire(ctx, central, online, write) })
	g.mu.Unlock()
	return false
}

// fire is the dwell expiring: the pending level is written unless
// something cancelled it in the meantime.
//
// The re-check under the lock is not belt-and-braces. [time.Timer.Stop]
// returns false for a timer whose callback has already started, so a
// cancelling observe that arrives in that window cannot prevent this
// goroutine from running — it can only leave the entry disagreeing with
// what this call is about to write. Comparing against the entry's own
// pending timer is what makes the cancellation win.
func (g *hubStatusGate) fire(ctx context.Context, central string, online bool, write func(context.Context, string, bool) error) {
	g.mu.Lock()
	e := g.entries[central]
	if e == nil || e.timer == nil || e.pending != online {
		g.mu.Unlock()
		return
	}
	e.timer = nil
	undo := e.record(online)
	g.mu.Unlock()
	g.settle(central, write(ctx, central, online), undo)
}

// record stamps a level as taken and returns the undo that puts the entry
// back if it turns out the broker did not take it.
//
// The level is stamped BEFORE the write rather than after it, so that a
// second observation of the same level arriving while this one is still in
// flight is deduped instead of publishing the same byte twice. The undo is
// what makes that safe: it is the "recorded only once the broker accepted
// it" rule of go-hamqtt's own state gate, reached by rolling back rather
// than by holding the mutex across broker I/O.
//
// It declines to roll back over a newer recorded level. A failed write
// whose entry has moved on since is not the current claim about the broker
// any more, and restoring the level it superseded would resurrect a
// statement two writes stale.
func (e *hubStatusEntry) record(online bool) func(*hubStatusEntry) {
	prevWritten, prevLevel, gen := e.written, e.level, e.gen
	e.gen++
	e.written, e.level = true, online
	return func(cur *hubStatusEntry) {
		if cur != e || e.gen != gen+1 {
			return
		}
		e.written, e.level = prevWritten, prevLevel
	}
}

// settle un-records a level the broker refused.
//
// This is the half of the gate that a dedup cache gets wrong by default,
// and go-hamqtt's publisher/state.go states the reason: caching a level
// whose publish failed makes the next identical one hit the gate and
// publish nothing, leaving the entity blank until the value changes again.
// On THIS topic "until the value changes again" is, for a healthy CCU,
// never — so a swallowed failure is a permanently unavailable hub plane.
func (g *hubStatusGate) settle(central string, err error, undo func(*hubStatusEntry)) {
	if err == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	undo(g.entries[central])
}

// Reset re-opens every remembered level without forgetting the CCUs, so the
// next observation of each one writes again even though the level has not
// changed.
//
// It is the same contract as the shared state and availability publishers'
// own Reset, and it exists for the same event: a broker restarted without a
// persistent retained store holds none of the bytes this gate believes it
// took. Without this, a fold that has not changed across the reconnect —
// which for a healthy CCU is every fold — writes nothing, and every
// CCU-scoped hub entity sits `unavailable` under `availability_mode: "all"`
// until that CCU's reachability happens to change.
//
// The CCUs themselves are kept, and so is any armed dwell: a level that was
// mid-debounce when the link dropped is still the level that should land
// when the window elapses.
func (g *hubStatusGate) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.entries {
		e.written = false
	}
}

// forget drops one CCU's written level and cancels any pending write, so a
// re-wire of that central seeds the gate again rather than debouncing
// against a level the broker may no longer hold.
func (g *hubStatusGate) forget(central string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e := g.entries[central]; e != nil {
		e.stopTimer()
		delete(g.entries, central)
	}
}

// centrals returns every CCU the gate has observed a level for, so the
// shutdown path can write the counterpart without being told the fleet.
//
// Observed, not written: an entry exists only because [hubStatusGate.observe]
// created it, and the shutdown counterpart is wanted for exactly the CCUs
// this process may have claimed reachable — including one whose level was
// rolled back by a failed write, and one whose level [hubStatusGate.Reset]
// re-opened. Filtering on `written` would skip both while the broker may
// still hold an `online` for them.
func (g *hubStatusGate) centrals() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, 0, len(g.entries))
	for name := range g.entries {
		out = append(out, name)
	}
	return out
}

func (e *hubStatusEntry) stopTimer() {
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
}

// hubStatusTopic is the per-CCU reachability gate's topic,
// `<base>/<central>/hub/status`.
//
// It goes through [TopicBuilder.HubStatus] rather than composing the string,
// because the declaring side reaches the same builder from
// [DefaultDiscoveryBuilder.renderHubItem]. Under Home Assistant's default
// `availability_mode: "all"` a disagreement between the two does not degrade
// an entity, it makes it permanently unavailable with nothing on the wire
// naming the cause — the same reason [availabilityLayout] exists for the
// device plane.
func (b *Bridge) hubStatusTopic(centralName string) string {
	return b.topics.HubStatus(b.resolvedCentral(centralName))
}

// PublishHubReachability folds one CCU's reachability into the retained gate
// at `<base>/<central>/hub/status`, debounced by [hubStatusDwell].
//
// `online` is the caller's fold over the CCU's interface states — see
// [hub.Connectivity.AnyReachable] for what the disjunction means and why it
// is not the conjunction. This method owns only the debounce and the write.
//
// It publishes through the shared availability publisher, at QoS 1, for the
// reason [newAvailabilityPublisher] states: an availability level is written
// only on a transition, so a lost one is not repaired by a later publish. A
// lost `offline` here is the whole defect this topic exists to fix, left
// unfixed.
//
// The error is the SEEDING write's error only. A debounced write happens on
// the dwell's own goroutine, after this call has returned, and reports
// through the bridge's publish-error counter instead.
func (b *Bridge) PublishHubReachability(ctx context.Context, centralName string, online bool) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	var seedErr error
	synchronous := b.hubStatus.observe(ctx, b.resolvedCentral(centralName), online,
		func(ctx context.Context, central string, online bool) error {
			_, err := b.avail.Publish(ctx, b.hubStatusTopic(central), online)
			if err != nil {
				b.incPublishErrors(central)
				seedErr = err
			}
			return err
		})
	if synchronous {
		return seedErr
	}
	return nil
}

// announceHubStatusOffline writes `offline` to every per-CCU gate the daemon
// has written a level for, immediately and without the dwell.
//
// This is the LWT ordering answer, and it starts from a protocol fact: MQTT
// allows exactly ONE Last Will per connection, and this daemon's is already
// spent on `<base>/bridge/status`. `hub/status` therefore cannot have a will
// of its own, and a per-CCU `online` left retained by a daemon that died is
// not preventable at the broker.
//
// It does not have to be. The gate is added ALONGSIDE `bridge/status` in
// every entity's availability list, never instead of it, and Home Assistant's
// default `availability_mode: "all"` is a conjunction: the will's
// `bridge/status: offline` alone is enough to make every CCU-scoped entity
// unavailable, whatever the per-CCU gates still say. A stale `online` is a
// false STATEMENT on a topic an operator can read, not a ghost entity.
//
// So the daemon repairs the statement where it can. On a graceful stop the
// broker discards the will, and this call writes the per-CCU counterpart —
// BEFORE [Bridge.AnnounceOffline] writes the bridge marker, so no instant
// exists in which the daemon has already declared itself gone while its CCU
// gates still claim reachability. On an ungraceful death the will covers the
// entities and the next connect repairs the statement: the per-CCU seed
// republishes the current fold before that CCU's hub discovery configs, so a
// stale `online` survives only for as long as the daemon is down — exactly
// the window in which `bridge/status` already says otherwise.
func (b *Bridge) announceHubStatusOffline(ctx context.Context) {
	if !b.cfg.RawEnabled {
		return
	}
	for _, central := range b.hubStatus.centrals() {
		b.hubStatus.forget(central)
		if _, err := b.avail.Publish(ctx, b.hubStatusTopic(central), false); err != nil {
			b.incPublishErrors(central)
		}
	}
}

// RetractHubStatus clears one CCU's reachability gate and forgets the
// debounced level, for a central removed from the running fleet.
//
// It is the third of the three things that can happen to the topic, and the
// only one that is not a level: `offline` says "this CCU is unreachable",
// while a removal means there is no such CCU any more and nothing should
// keep reading a claim about it. The retraction leaves through the shared
// availability publisher, so it clears the topic at the level the publish
// used and drops it from the publisher's own index — the same reason the
// per-device retraction goes through there rather than through a hand-built
// topic and the state QoS.
//
// The gate entry is forgotten first: after a retraction the broker holds no
// byte, so a later re-wire of the same central must SEED again rather than
// debounce against a level nothing remembers.
func (b *Bridge) RetractHubStatus(ctx context.Context, centralName string) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	central := b.resolvedCentral(centralName)
	b.hubStatus.forget(central)
	return b.avail.Retract(ctx, b.hubStatusTopic(central))
}
