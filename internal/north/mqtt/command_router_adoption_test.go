// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
	hagomqtt "github.com/SukramJ/go-hamqtt/publisher/gomqtt"
)

// wireRecorder records what each command subscription actually asked the
// broker for, which is the only way this package can observe the difference
// between the router's two subscribe modes.
//
// The number of [SubscribeOption] values is the discriminator, and it is a
// deliberate choice of proxy. go-mqtt's subscribeOptions struct is
// unexported, so an option cannot be applied and read back from here; what
// the shared adapter passes is nevertheless exactly determined — one option
// (`WithNoLocal`) on the unattributed path, two (`WithNoLocal` plus
// `WithSubscriptionID`) on the attributed one, and none at all if the router
// ever fell back to a plain Subscribe. All three are wire differences that
// matter, so counting them pins the mode rather than merely observing it.
type wireRecorder struct {
	mu sync.Mutex
	// filters is every Subscribe call in registration order.
	filters []string
	// opts is the number of subscribe options each of those calls carried.
	opts []int
	// unsubscribed is every Unsubscribe call in call order.
	unsubscribed []string
	// live is the filter set the broker would currently hold.
	live map[string]bool
	// failAt refuses the Subscribe call at this index; -1 refuses none.
	failAt int
}

func newWireRecorder() *wireRecorder {
	return &wireRecorder{live: map[string]bool{}, failAt: -1}
}

func (r *wireRecorder) Publish(context.Context, string, []byte, QoS, bool, ...PublishOption) error {
	return nil
}

func (r *wireRecorder) Subscribe(_ context.Context, filter string, _ QoS, _ MessageHandler, opts ...SubscribeOption) (SubscribeResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := len(r.filters)
	r.filters = append(r.filters, filter)
	r.opts = append(r.opts, len(opts))
	if idx == r.failAt {
		return SubscribeResult{}, errSubscribeRefused
	}
	r.live[filter] = true
	return SubscribeResult{}, nil
}

func (r *wireRecorder) Unsubscribe(_ context.Context, filter string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unsubscribed = append(r.unsubscribed, filter)
	delete(r.live, filter)
	return nil
}

func (r *wireRecorder) snapshot() (filters []string, opts []int, unsubscribed, live []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for f := range r.live {
		live = append(live, f)
	}
	return append([]string(nil), r.filters...), append([]int(nil), r.opts...),
		append([]string(nil), r.unsubscribed...), live
}

// TestCommandRouterStaysUnattributedAndStampsNoIdentifier is the pin on the
// adoption's single most consequential choice: this plane runs on the shared
// router's UNATTRIBUTED path, and every one of its subscriptions goes on the
// wire exactly as it did before the router existed.
//
// go-hamqtt v0.28.0 lets a router accept OVERLAPPING command filters when the
// transport can tie a delivery to the subscription it arrived for, which it
// does with an MQTT 5.0 Subscription Identifier (§3.8.2.1.2). The shared
// go-mqtt adapter implements that capability, so the mode is not the
// library's choice to withhold — it is decided by whether this daemon's
// filters overlap. They do not, since the three overlapping shapes were
// coalesced into the two data-point handlers, so `Attributed()` must read
// false and 0 of the 10 subscriptions may carry an identifier.
//
// Why the mode matters more than the count suggests: attribution exists only
// on MQTT 5.0, `north.mqtt.protocol_version: "3.1.1"` is an
// operator-reachable config key, and the router deliberately never retries
// an attributed route without its identifier — a silent downgrade would
// leave accepted overlapping routes running handlers twice with nothing in
// any log. So on an overlapping filter set that one config key stops being a
// dialect choice and becomes a refused Start: the whole command plane down at
// boot, while the state plane keeps publishing and the deployment looks
// healthy. A filter edit that reintroduces an overlap has to fail here, in
// this repository, rather than on a customer's downgraded broker.
func TestCommandRouterStaysUnattributedAndStampsNoIdentifier(t *testing.T) {
	t.Parallel()

	rec := newWireRecorder()
	sub := NewCommandSubscriber(rec, NewTopicBuilder("openccu-loom"), &fakeSink{}, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	if sub.attributed() {
		t.Error("Attributed() = true: the router accepted an overlapping filter pair and now " +
			"subscribes with MQTT 5.0 subscription identifiers, which makes " +
			"north.mqtt.protocol_version: \"3.1.1\" a boot failure of the whole command plane")
	}

	filters, opts, _, _ := rec.snapshot()
	if len(filters) != len(pinnedCommandFilters) {
		t.Fatalf("registered %d subscriptions, want %d", len(filters), len(pinnedCommandFilters))
	}
	stamped := 0
	for i, f := range filters {
		if id := sub.subscriptionID(f); id != 0 {
			stamped++
			t.Errorf("filter %q carries subscription identifier %d, want none", f, id)
		}
		// One option: No Local, and nothing else. Two would be an
		// identifier alongside it; zero would mean the router fell back to
		// a plain Subscribe and the broker would deliver this daemon's own
		// publishes back into its command handlers.
		if opts[i] != 1 {
			t.Errorf("filter %q subscribed with %d options, want exactly 1 (No Local, no identifier)",
				f, opts[i])
		}
	}
	if stamped != 0 {
		t.Errorf("%d of %d subscriptions carry an identifier, want 0 of %d",
			stamped, len(filters), len(filters))
	}
}

// TestCommandTransportCanAttributeSoTheDisjointnessGuardIsLoadBearing is the
// anti-vacuity half of the pin above, and it is not optional.
//
// `Attributed() == false` would also be true of a transport that cannot
// attribute anything at all — on such a transport [hapublisher.CommandRouter.Handle]
// refuses every overlap outright, so the flag could never be set and the
// guard in [CommandSubscriber.Start] would be armed against nothing. This
// daemon's transport is not that transport: it is the shared go-mqtt adapter,
// which implements [hapublisher.AttributingSubscriber], so an overlapping
// pair is ACCEPTED and switches the plane into attributed mode silently.
//
// So this builds a router the same way Start does, hands it the pair that
// really did overlap here until it was coalesced — the legacy bucket-less
// data-point catch-all and the week-profile shape — and asserts both halves:
// the registration succeeds, and the mode flips. That is the state Start
// refuses to subscribe in, and the reason the refusal is a real gate.
func TestCommandTransportCanAttributeSoTheDisjointnessGuardIsLoadBearing(t *testing.T) {
	t.Parallel()

	rec := newWireRecorder()
	router := hapublisher.NewCommandRouter(
		hagomqtt.Split(inboundOnlyPublisher{}, rec),
		hapublisher.CommandConfig{QoS: hapublisher.QoSAtLeastOnce},
	)

	const catchAll = "openccu-loom/+/+/+/+/+/set"
	const weekProfile = "openccu-loom/+/+/+/+/week_profile/set"
	noop := func(context.Context, hapublisher.Command) {}
	if err := router.Handle(catchAll, noop); err != nil {
		t.Fatalf("handle %q: %v", catchAll, err)
	}
	if err := router.Handle(weekProfile, noop); err != nil {
		t.Fatalf("handle %q: %v — the overlap was REFUSED, so this transport cannot attribute "+
			"a delivery and the guard in Start is armed against nothing", weekProfile, err)
	}
	if !router.Attributed() {
		t.Fatal("Attributed() = false after registering an overlapping pair: the flag the guard " +
			"in Start reads cannot be set, so the guard proves nothing")
	}

	// And the mode is visible on the wire, which is what makes the
	// one-option assertion in the test above a discriminating one.
	if err := router.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = router.Stop(context.Background()) })
	_, opts, _, _ := rec.snapshot()
	for i, n := range opts {
		if n != 2 {
			t.Errorf("attributed subscription %d carried %d options, want 2 "+
				"(No Local plus a subscription identifier)", i, n)
		}
	}
}

// TestCommandStartRollsBackWhatItAlreadySubscribed pins the behaviour this
// daemon gained by adopting the router and could not express before.
//
// The hand-rolled subscriber aborted Start on the first refused SUBSCRIBE
// and left every filter it had already registered LIVE. That is the worst of
// the three possible outcomes: the daemon reports a failed start, the
// composition root tears the stack down, and meanwhile a broker that granted
// nine of ten filters keeps delivering commands into a subscriber nobody
// finished wiring. From the outside — an operator pressing a button in Home
// Assistant — some commands work and the rest do nothing, which reads as a
// broken CCU rather than a broken subscribe.
//
// [hapublisher.CommandRouter.Start] unsubscribes what it already registered,
// so a failed start leaves the broker holding nothing. Driven once per
// filter index, because the interesting case is a refusal in the middle and
// the interesting property is that everything BEFORE it comes back down.
func TestCommandStartRollsBackWhatItAlreadySubscribed(t *testing.T) {
	t.Parallel()

	total := len(pinnedCommandFilters)
	for failAt := range total {
		rec := newWireRecorder()
		rec.failAt = failAt
		sub := NewCommandSubscriber(rec, NewTopicBuilder("openccu-loom"), &fakeSink{}, nil)
		err := sub.Start(context.Background())
		if err == nil {
			t.Fatalf("failAt %d: Start returned nil despite a refused subscribe", failAt)
		}
		if !errors.Is(err, errSubscribeRefused) {
			t.Fatalf("failAt %d: error does not wrap the broker's: %v", failAt, err)
		}
		filters, _, unsubscribed, live := rec.snapshot()
		// Nothing after the refusal is attempted: a partial set is not
		// completed, it is withdrawn.
		if len(filters) != failAt+1 {
			t.Errorf("failAt %d: %d subscribes attempted, want %d — Start must abort at the refusal",
				failAt, len(filters), failAt+1)
		}
		if len(live) != 0 {
			t.Errorf("failAt %d: %d filters still live on the broker after a failed Start (%q) — "+
				"they would deliver commands into a subscriber whose start failed",
				failAt, len(live), live)
		}
		if len(unsubscribed) != failAt {
			t.Errorf("failAt %d: %d rollback unsubscribes, want %d (every filter registered before the refusal)",
				failAt, len(unsubscribed), failAt)
		}
		for i, f := range unsubscribed {
			if want := filters[i]; f != want {
				t.Errorf("failAt %d: rollback %d unsubscribed %q, want %q", failAt, i, f, want)
			}
		}
		sub.Close()
	}
}

// TestCommandStartRefusesAWildcardTopicBase guards the one hazard the move
// to [hapublisher.Command.Wildcards] introduced.
//
// Every handler now reads the topic off the route's captured wildcard levels
// instead of splitting the topic itself, which is what removed thirteen
// hand-rolled splits and the two index-arithmetic defects among them. The
// reading is positional, so it is only stable while the base contributes no
// wildcard level of its own: `topic_base: "home/+"` would make `+` the first
// captured wildcard and shift central, interface, address, channel and
// parameter each one place along. The old code was immune by accident — it
// cut the base off as a literal string prefix, so such a topic simply failed
// the prefix test and every command was silently dropped.
//
// `north.mqtt.topic_base` is free-form operator config and nothing in
// internal/config rejects a wildcard in it, so this is operator-reachable and
// the failure it would cause is a CCU write to the wrong parameter — not a
// dropped message. Refused before a single route is registered.
func TestCommandStartRefusesAWildcardTopicBase(t *testing.T) {
	t.Parallel()

	for _, base := range []string{"home/+", "home/#", "+", "loom/+/x"} {
		rec := newWireRecorder()
		sub := NewCommandSubscriber(rec, NewTopicBuilder(base), &fakeSink{}, nil)
		err := sub.Start(context.Background())
		if err == nil {
			t.Errorf("base %q: Start returned nil — every handler would read the topic one "+
				"segment out of step and write to a parameter nobody named", base)
		} else if !strings.Contains(err.Error(), "wildcard") {
			t.Errorf("base %q: error does not say why: %v", base, err)
		}
		if filters, _, _, _ := rec.snapshot(); len(filters) != 0 {
			t.Errorf("base %q: %d subscriptions were registered before the refusal", base, len(filters))
		}
	}
}

// TestRouteWildcardArityGuardDropsAMisboundRoute pins the one failure mode
// the move to [hapublisher.Command.Wildcards] made worse, and bounds it.
//
// Handlers read their topic positionally out of the route's captured wildcard
// levels, and the router runs them on a worker pool with no recover of its
// own — so a route bound to a handler that expects a different shape would
// index past the end of that slice and take the whole daemon down, where the
// old per-handler segment check returned and dropped one message.
// [CommandSubscriber.routes] makes the mismatch unreachable and the delivery
// tests drive every row of it, so this is the both-are-wrong case: it has to
// be a logged drop, not a panic.
//
// Driven directly rather than through a delivery, because there is no route
// in the real set that produces the wrong arity — which is the point.
func TestRouteWildcardArityGuardDropsAMisboundRoute(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{}
	wp := &fakeWPSink{}
	cdp := &fakeCDPSink{}
	sub := NewCommandSubscriber(NewNoopClient(), NewTopicBuilder("gh"), sink, nil).
		WithWeekProfileSink(wp).
		WithCDPSink(cdp)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(sub.Close)

	// Every handler that reads a fixed number of wildcards, handed a command
	// with none. Each must return rather than panic, and must reach no sink.
	short := hapublisher.Command{Topic: "gh/whatever", Filter: "gh/+", Payload: []byte("P1")}
	ctx := context.Background()
	for name, h := range map[string]hapublisher.CommandHandler{
		"sysvar":         sub.handleSysvar,
		"program":        sub.handleProgram,
		"program_enable": sub.handleProgramEnable,
		"install_mode":   sub.handleInstallMode,
		"cdp_invoke":     sub.handleCDPInvoke,
		"service_method": sub.handleServiceMethod,
		"alarm":          sub.handleAlarmCommand,
		"week_profile":   sub.handleWeekProfile,
		"combined":       sub.handleCombinedDP,
		"schedule":       sub.handleScheduleSwitch,
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: handler panicked on a command with no wildcards (%v) — "+
						"the router's pool does not recover, so this is the daemon going down", name, r)
				}
			}()
			h(ctx, short)
		}()
	}

	if got := sink.setValues.Load() + sink.setSysvars.Load() + sink.triggers.Load() +
		sink.programEnables.Load() + sink.masterValues.Load(); got != 0 {
		t.Errorf("a mis-bound route reached the CCU sink %d time(s)", got)
	}
	if got := wp.calls.Load(); got != 0 {
		t.Errorf("a mis-bound route reached the week-profile sink %d time(s)", got)
	}
	if got := cdp.calls.Load(); got != 0 {
		t.Errorf("a mis-bound route reached the custom-DP sink %d time(s)", got)
	}
}
