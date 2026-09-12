// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// fanoutClient is the broker-plus-client double every class-B guard in this
// file runs its command through. It reproduces BOTH multiplications an
// inbound command survives on the way to a handler, because they compose and
// a double that models only one of them pins the wrong number:
//
//   - The BROKER sends one copy of the message per matching subscription.
//     MQTT 3.1.1 §4.7.3 / 5.0 §3.3.4 permit it and Mosquitto and EMQX both
//     do it; it was measured against Mosquitto 2.1.2 on v3.1.1 and v5 alike.
//   - The CLIENT then re-matches every arriving copy against its whole local
//     filter list and calls EVERY matching handler, without correlating a
//     copy with the subscription it arrived on (go-mqtt's
//     TCPClient.dispatch).
//
// So N overlapping filters cost N copies x N local handler calls. The
// existing [echoClient] in command_state_disjoint_test.go implements the
// second half only, which is enough for a yes/no "did the wrong handler run"
// question but understates the call count by a factor of N. That exact
// omission is what hid the shared library's own double-dispatch defect: its
// fake fanned out once, so its test asserted the wrong number and passed
// while a real `toggle` toggled twice against a real broker. See
// go-hamqtt's publisher/command_test.go `cmdBroker.deliver`.
//
// fanoutClient is deliberately a separate type rather than a change to
// echoClient: echoClient is load-bearing for two existing tests whose
// assertions are counts, and widening its fan-out under them would move
// those numbers for reasons unrelated to this pin.
type fanoutClient struct {
	mu        sync.Mutex
	subs      map[string]MessageHandler
	published []Publication
}

func newFanoutClient() *fanoutClient {
	return &fanoutClient{subs: make(map[string]MessageHandler)}
}

func (c *fanoutClient) Publish(_ context.Context, topic string, payload []byte, qos QoS, retain bool, _ ...PublishOption) error {
	c.mu.Lock()
	c.published = append(c.published, Publication{
		Topic: topic, Payload: append([]byte(nil), payload...), QoS: qos, Retain: retain,
	})
	c.mu.Unlock()
	// Live routing to an already-established subscription carries Retain=0
	// (MQTT 3.1.1 / 5.0 §3.3.1.3); only the stored replay to a fresh
	// subscription is retained, which is why a handler's retained-drop guard
	// does not save it from an echo.
	c.fanout(topic, payload, false)
	return nil
}

func (c *fanoutClient) Subscribe(_ context.Context, filter string, _ QoS, handler MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	c.mu.Lock()
	c.subs[filter] = handler
	c.mu.Unlock()
	return SubscribeResult{}, nil
}

func (c *fanoutClient) Unsubscribe(_ context.Context, filter string) error {
	c.mu.Lock()
	delete(c.subs, filter)
	c.mu.Unlock()
	return nil
}

// deliver drives one broker-originated message (Home Assistant publishing a
// command, not the daemon echoing its own state) through the two fan-outs and
// returns the number of copies the broker produced, i.e. the number of
// matching subscriptions.
func (c *fanoutClient) deliver(topic string, payload []byte, retained bool) int {
	return c.fanout(topic, payload, retained)
}

// fanout is the two-stage delivery both Publish and deliver go through.
func (c *fanoutClient) fanout(topic string, payload []byte, retained bool) int {
	c.mu.Lock()
	targets := make([]MessageHandler, 0, len(c.subs))
	for filter, h := range c.subs {
		if h != nil && mqttFilterMatches(filter, topic) {
			targets = append(targets, h)
		}
	}
	c.mu.Unlock()
	// One copy per matching subscription; each copy visits every matching
	// local handler.
	for range targets {
		for _, h := range targets {
			h(&Message{Topic: topic, Payload: payload, Retain: retained})
		}
	}
	return len(targets)
}

// Filters returns a snapshot of every active subscription filter.
func (c *fanoutClient) Filters() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.subs))
	for f := range c.subs {
		out = append(out, f)
	}
	return out
}

// matchingFilters counts the subscriptions a topic is delivered on.
func (c *fanoutClient) matchingFilters(topic string) int {
	n := 0
	for _, f := range c.Filters() {
		if mqttFilterMatches(f, topic) {
			n++
		}
	}
	return n
}

// classBFixture stands up a fully-sinked [CommandSubscriber] over a
// [fanoutClient], which is what makes a "did the OTHER handler also run"
// question answerable: every sink a class-B topic could plausibly reach is
// wired, so a wrong dispatch shows up as a call rather than as a nil-sink
// debug line.
type classBFixture struct {
	client *fanoutClient
	sub    *CommandSubscriber
	dp     *fakeSink
	cmb    *fakeCombinedDPSink
	sched  *fakeScheduleSwitchSink
}

func newClassBFixture(t *testing.T) *classBFixture {
	t.Helper()
	f := &classBFixture{
		client: newFanoutClient(),
		dp:     &fakeSink{},
		cmb:    &fakeCombinedDPSink{},
		sched:  &fakeScheduleSwitchSink{},
	}
	f.sub = NewCommandSubscriber(f.client, NewTopicBuilder(classBBase), f.dp, nil).
		WithCombinedDPSink(f.cmb).
		WithScheduleSwitchSink(f.sched)
	if err := f.sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	return f
}

// classBBase is the topic base every fixture in this file uses. It carries no
// "/" of its own on purpose — [CommandSubscriber.commandParts] strips the
// base before any handler indexes a segment, and the multi-level-base case is
// already pinned by command_subscriber_topic_base_test.go.
const classBBase = "openccu-loom"

// TestCombinedDPCommandDoesNotAlsoIssueADataPointWrite pins collision class B
// for the `combined` bucket: a combined-DP write must reach the combined sink
// and must NOT also be dispatched as a CCU parameter write.
//
// The overlap is real and is in the wire today. The bucket-aware data-point
// filter `<base>/+/+/+/+/+/+/set` is seven segments below the base and so is
// the combined filter `<base>/+/+/+/+/combined/+/set`, so a combined command
// topic matches both. Nothing narrows the first filter — MQTT has no
// exclusion wildcard — so the only thing standing between a combined write
// and a bogus CCU write to a parameter literally named after the `<kind>`
// segment is the bucket allow-list in [CommandSubscriber.handleDataPoint]'s
// seven-segment branch: `values` and `master` pass, and `default:` drops with
// a debug line.
//
// Before this test that guard had no coverage at all (finding **F2**). The
// sibling class A — the six-segment `week_profile` overlap, guarded by
// `reservedLegacyParamSegments` — has
// TestWeekProfileCommandDoesNotAlsoIssueADataPointWrite, and the class-A
// guard's own doc comment says class B "gets the same protection for free".
// That is true of the mechanism and was false of the coverage: `combined` and
// `schedule` were exercised only through `NoopClient.DeliverInbound(filter,
// topic, …)`, which delivers through ONE named filter and therefore cannot
// observe a second dispatch by construction. Nine of the eleven
// double-matched command topics in this repository's discovery goldens are
// class B.
//
// Pinned rows that are defects:
//
// The numbers this test pins moved once, deliberately, when the three
// overlapping filters were coalesced into the two data-point handlers. Before
// that the combined filter was live alongside the catch-all, so a topic
// matched 2 subscriptions and the combined sink was invoked FOUR times for
// one operator action (2 broker copies x 2 local handlers — see
// [fanoutClient]), harmless only because a combined write is idempotent.
// Now the combined shape has no filter of its own and
// [CommandSubscriber.handleDataPoint] dispatches it from the bucket position
// of the seven-segment branch, so exactly 1 subscription matches and the
// sink is invoked exactly once. All three counts are asserted exactly, in
// both directions: a re-added sibling filter fails on the match count before
// any sink count moves, and a dispatch lost in the coalesced branch fails on
// the sink count.
func TestCombinedDPCommandDoesNotAlsoIssueADataPointWrite(t *testing.T) {
	f := newClassBFixture(t)

	const topic = classBBase + "/ccu-01/HmIP-RF/0001ABCD/1/combined/duration/set"

	// Disjointness guard, and the load-bearing half of this test: one
	// matching subscription means the broker produces one copy and the
	// client has one handler to re-match it against, so neither fan-out can
	// multiply. A second matching filter reinstates the N*N dispatch and
	// fails here rather than silently doubling every combined write.
	if got := f.client.matchingFilters(topic); got != 1 {
		t.Fatalf("filters matching %q = %d, want exactly 1 — "+
			"the combined shape has no filter of its own since coalescing, and a second "+
			"matching subscription is a second dispatch of every combined write", topic, got)
	}

	copies := f.client.deliver(topic, []byte("30"), false)
	f.sub.WaitIdle()

	if copies != 1 {
		t.Fatalf("broker copies = %d, want 1", copies)
	}
	// 1 copy x 1 matching handler: handleDataPoint, which dispatches the
	// combined shape from its seven-segment branch.
	if got := f.cmb.count(); got != 1 {
		t.Errorf("combined-DP writes = %d, want 1 "+
			"(one broker copy, dispatched once out of handleDataPoint's bucket branch)", got)
	}
	if got := f.dp.setValues.Load(); got != 0 {
		t.Errorf("CCU VALUES writes = %d, want 0 — a combined command reached the CCU as a parameter named %q", got, "duration")
	}
	if got := f.dp.masterValues.Load(); got != 0 {
		t.Errorf("CCU MASTER writes = %d, want 0 — a combined command reached the CCU as a master parameter", got)
	}
	if got := f.sched.count(); got != 0 {
		t.Errorf("schedule-switch writes = %d, want 0 — a combined command reached the schedule sink", got)
	}
	// The combined write must still have arrived intact: a guard that dropped
	// the message in BOTH handlers would satisfy every assertion above.
	f.cmb.mu.Lock()
	defer f.cmb.mu.Unlock()
	for i, c := range f.cmb.calls {
		want := fakeCombinedCall{central: "ccu-01", iface: "HmIP-RF", addr: "0001ABCD", kind: "duration", channel: 1, raw: "30"}
		if c != want {
			t.Errorf("combined call %d = %+v, want %+v", i, c, want)
		}
	}
}

// TestScheduleSwitchCommandDoesNotAlsoIssueADataPointWrite is the second
// member of collision class B: `<base>/+/+/+/+/schedule/+/set`, seven
// segments below the base, against the same bucket-aware data-point
// catch-all.
//
// It is a separate test rather than a table row because the two members are
// guarded by the same `default:` arm but reach different sinks with different
// payload grammars — `schedule` parses "true"/"false" into a bool, `combined`
// forwards the payload verbatim — and a regression that broke the boolean
// parse while leaving the guard intact would be invisible in the combined
// fixture.
//
// The counts moved with its combined twin when the filters were coalesced:
// one matching subscription, one broker copy, one schedule write. See
// TestCombinedDPCommandDoesNotAlsoIssueADataPointWrite for the full
// reasoning; it is not repeated here.
func TestScheduleSwitchCommandDoesNotAlsoIssueADataPointWrite(t *testing.T) {
	f := newClassBFixture(t)

	const topic = classBBase + "/ccu-01/HmIP-RF/0001ABCD/1/schedule/1_1/set"

	if got := f.client.matchingFilters(topic); got != 1 {
		t.Fatalf("filters matching %q = %d, want exactly 1 — "+
			"the schedule shape has no filter of its own since coalescing, and a second "+
			"matching subscription is a second dispatch of every schedule write", topic, got)
	}

	copies := f.client.deliver(topic, []byte("true"), false)
	f.sub.WaitIdle()

	if copies != 1 {
		t.Fatalf("broker copies = %d, want 1", copies)
	}
	if got := f.sched.count(); got != 1 {
		t.Errorf("schedule-switch writes = %d, want 1 "+
			"(one broker copy, dispatched once out of handleDataPoint's bucket branch)", got)
	}
	if got := f.dp.setValues.Load(); got != 0 {
		t.Errorf("CCU VALUES writes = %d, want 0 — a schedule command reached the CCU as a parameter named %q", got, "1_1")
	}
	if got := f.dp.masterValues.Load(); got != 0 {
		t.Errorf("CCU MASTER writes = %d, want 0 — a schedule command reached the CCU as a master parameter", got)
	}
	if got := f.cmb.count(); got != 0 {
		t.Errorf("combined-DP writes = %d, want 0 — a schedule command reached the combined sink", got)
	}
	f.sched.mu.Lock()
	defer f.sched.mu.Unlock()
	for i, c := range f.sched.calls {
		want := fakeScheduleCall{central: "ccu-01", iface: "HmIP-RF", addr: "0001ABCD", key: "1_1", channel: 1, enabled: true}
		if c != want {
			t.Errorf("schedule call %d = %+v, want %+v", i, c, want)
		}
	}
}

// TestClassBFanoutDoubleIsNotSingleStage guards the guards: it pins that
// [fanoutClient] really does compose both fan-outs, so the two class-B tests
// above cannot silently degrade into the weaker model that hid the shared
// library's defect.
//
// This is not a tautology check. The assertion is on the number of HANDLER
// invocations a single delivery produces across two overlapping filters,
// which is N*N and not N — a single-stage double returns 2 here, and the
// library's own test stood at exactly that number while a real broker
// dispatched 4.
func TestClassBFanoutDoubleIsNotSingleStage(t *testing.T) {
	c := newFanoutClient()
	var calls int
	var mu sync.Mutex
	h := func(*Message) {
		mu.Lock()
		calls++
		mu.Unlock()
	}
	ctx := context.Background()
	for _, f := range []string{"gh/+/+/set", "gh/+/lit/set"} {
		if _, err := c.Subscribe(ctx, f, QoS1, h); err != nil {
			t.Fatalf("subscribe %q: %v", f, err)
		}
	}
	copies := c.deliver("gh/a/lit/set", []byte("x"), false)
	if copies != 2 {
		t.Fatalf("broker copies = %d, want 2 (two overlapping filters match)", copies)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 4 {
		t.Fatalf("handler invocations = %d, want 4 (2 broker copies x 2 local handlers). "+
			"2 means the double models only the client-side re-match and understates every "+
			"count in this file by a factor of N", calls)
	}
}

// TestClassBTopicsAreNotAlsoAWeekProfileShape pins the segment arithmetic
// that keeps the three coalesced shapes in the two branches that dispatch
// them, and pins that each of them matches exactly one subscription.
//
// The arithmetic is what decides which branch of
// [CommandSubscriber.handleDataPoint] a shape lands in: six segments below
// the base go to the branch that dispatches `week_profile`, seven to the
// branch that dispatches `combined` and `schedule` off the bucket position.
// A class-B shape that lost a level would arrive in the six-segment branch,
// which does not know it, and be written to the CCU as a parameter named
// after its own literal — the defect that put `week_profile` on a reserved
// list in the first place.
//
// The one-matching-filter assertion is the property that replaced that list.
// It held for none of these three shapes before coalescing: each matched two
// subscriptions, and the hand-maintained guards only kept the second
// dispatch from reaching the CCU.
func TestClassBTopicsAreNotAlsoAWeekProfileShape(t *testing.T) {
	f := newClassBFixture(t)
	for _, topic := range []string{
		classBBase + "/ccu-01/HmIP-RF/0001ABCD/1/combined/duration/set",
		classBBase + "/ccu-01/HmIP-RF/0001ABCD/1/schedule/1_1/set",
	} {
		rest := strings.TrimPrefix(topic, classBBase+"/")
		if got := len(strings.Split(rest, "/")); got != 7 {
			t.Errorf("%q is %d segments below the base, want 7 — "+
				"a class-B shape that became six segments would arrive in the six-segment "+
				"branch, which does not dispatch it and would write its literal to the CCU "+
				"as a parameter name", topic, got)
		}
		if got := f.client.matchingFilters(topic); got != 1 {
			t.Errorf("%q matches %d filters, want 1", topic, got)
		}
	}
	// The class-A shape for contrast: six segments, and likewise matched by
	// exactly one subscription since its own filter was folded in.
	wp := classBBase + "/ccu-01/HmIP-RF/0001ABCD/1/week_profile/set"
	if got := len(strings.Split(strings.TrimPrefix(wp, classBBase+"/"), "/")); got != 6 {
		t.Errorf("%q is %d segments below the base, want 6", wp, got)
	}
	if got := f.client.matchingFilters(wp); got != 1 {
		t.Errorf("%q matches %d filters, want 1", wp, got)
	}
}
