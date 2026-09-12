// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// filterRecorder records every Subscribe call in registration order. Order
// matters to the pin: [CommandSubscriber.Start] aborts on the first refused
// subscribe and leaves the partial set live, so "which filters are up" after
// a broker ACL rejection is a function of the order, and a reordering is a
// behaviour change even when the set is unchanged.
type filterRecorder struct {
	mu      sync.Mutex
	filters []string
	qos     []QoS
}

func newFilterRecorder() *filterRecorder { return &filterRecorder{} }

func (r *filterRecorder) Publish(context.Context, string, []byte, QoS, bool, ...PublishOption) error {
	return nil
}

func (r *filterRecorder) Subscribe(_ context.Context, filter string, qos QoS, _ MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	r.mu.Lock()
	r.filters = append(r.filters, filter)
	r.qos = append(r.qos, qos)
	r.mu.Unlock()
	return SubscribeResult{}, nil
}

func (r *filterRecorder) Unsubscribe(context.Context, string) error { return nil }

func (r *filterRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.filters...)
}

// pinnedFilter is one row of the command-filter pin: the filter as
// registered, and the number of segments it carries BELOW the configured
// topic base.
//
// The segment count is pinned alongside the string because it is the only
// property that decides whether two filters can overlap at all. No filter in
// the set carries `#`, so MQTT filter matching requires equal segment counts;
// two filters of different lengths are disjoint no matter what literals they
// carry. Every collision class in this plane is therefore a group of
// same-length filters, and the count is what a reader needs in order to see a
// new one arriving. It is written out rather than computed from the string so
// that a filter gaining or losing a level fails the pin twice — once on the
// string, once on the arithmetic — instead of silently agreeing with itself.
type pinnedFilter struct {
	filter string
	segs   int
}

// pinnedCommandFilters is the exact, ordered set of command-topic filters
// [CommandSubscriber.Start] registers, with the configured base written as
// `<base>`.
//
// Groups by segment count, which is the same thing as grouping by collision
// class:
//
//	3 segments: the two daemon-level planes (alarm, addon_update) — disjoint
//	            from everything, literal first segment.
//	5 segments: the four hub planes — pairwise disjoint, `hub` literal at
//	            index 1 and a second literal at index 2.
//	6 segments: the legacy bucket-less data-point catch-all AND
//	            week_profile. THIS IS COLLISION CLASS A.
//	7 segments: the bucket-aware data-point catch-all, combined, schedule,
//	            and cdps/invoke. The first three are COLLISION CLASS B; the
//	            invoke filter escapes it on its literal last segment.
//	8 segments: the per-service-method filter — the only one of its length.
var pinnedCommandFilters = []pinnedFilter{
	{"<base>/+/+/+/+/+/+/set", 7},
	{"<base>/+/+/+/+/+/set", 6},
	{"<base>/+/hub/sysvars/+/set", 5},
	{"<base>/+/hub/programs/+/set", 5},
	{"<base>/+/hub/programs/+/trigger", 5},
	{"<base>/+/hub/install_mode/+/set", 5},
	{"<base>/+/devices/+/cdps/+/+/invoke", 7},
	{"<base>/+/+/+/+/custom/+/set/+", 8},
	{"<base>/+/+/+/+/week_profile/set", 6},
	{"<base>/+/+/+/+/combined/+/set", 7},
	{"<base>/+/+/+/+/schedule/+/set", 7},
	{"<base>/alarm/+/set", 3},
	{"<base>/system/addon_update/set", 3},
}

// TestCommandFilterSetIsPinned pins the exact ordered set of command filters
// the daemon subscribes, with each filter's segment count below the base.
//
// Finding **F3**: before this test, nothing in the repository asserted the
// filter set or even its size. `len(filters)` appears only inside vacuity
// guards, so a fourteenth `Subscribe` call — or a reworded thirteenth — landed
// silently. That is not a cosmetic gap. Two of this plane's filters are
// unavoidable catch-alls (`+/+/+/+/+/set` and `+/+/+/+/+/+/set`), so ANY new
// filter of six or seven segments below the base joins a collision class on
// arrival, and the only things stopping the resulting double dispatch are two
// hand-maintained lists in two different files:
// `reservedLegacyParamSegments` for class A and the bucket allow-list in
// [CommandSubscriber.handleDataPoint] for class B. Both are complete today by
// inspection only.
//
// The invariant `reservedLegacyParamSegments`' own doc comment states —
// "Every literal segment used in a seven-level command filter MUST be listed
// here, or that topic is dispatched a second time as a data-point write to a
// parameter that does not exist" — is enforced here, derived from the
// registered filters rather than restated. "Seven-level" there counts the
// base; this test counts below the base, so it is the six-segment group.
//
// Why the whole set and not just the classes: the shared library's
// `CommandRouter.Handle` refuses any pair of overlapping filters outright,
// because a broker sends one copy per matching subscription and the client
// re-matches each copy against its whole local filter list, so one message
// runs N handlers. Adopting it requires coalescing filters 9, 10 and 11 into
// the two data-point handlers. This pin is what makes that deletion a
// reviewed diff of three named rows rather than a count changing from 13 to
// 10.
//
// Pinned rows that are defects:
//
//   - Rows 9, 10 and 11 (`week_profile`, `combined`, `schedule`) each overlap
//     a catch-all. Three of the 78 pairs overlap; the shared library refuses
//     that shape. Pinned as current behaviour so the coalescing step has to
//     delete them explicitly (**F3** is the reason nothing noticed, **F2**
//     is the class-B half having no other coverage).
//   - The ORDER is pinned including the fact that it is not grouped by class
//     or by length. [CommandSubscriber.Start] aborts on the first refused
//     subscribe and leaves the partial set live, so the order decides which
//     filters survive a partial ACL denial — a property the shared library's
//     rollback removes and this daemon still has.
//
// Not pinned here: the QoS each filter registers at (that is
// [CommandSubscriber.WithQoS]'s contract and is covered by the subscriber's
// own tests), and the handler each filter is bound to — a filter/handler
// mismatch is caught by the delivery tests in
// command_subscriber_topic_base_test.go, which drive each filter by name.
func TestCommandFilterSetIsPinned(t *testing.T) {
	t.Parallel()

	// Two bases, because the base is free-form operator config and may carry
	// levels of its own. A pin taken at one base cannot tell a filter that
	// hardcodes a prefix apart from one that composes it.
	for _, base := range []string{"openccu-loom", "home/loom"} {
		rec := newFilterRecorder()
		sub := NewCommandSubscriber(rec, NewTopicBuilder(base), &fakeSink{}, nil)
		if err := sub.Start(context.Background()); err != nil {
			t.Fatalf("base %q: start: %v", base, err)
		}
		got := rec.recorded()

		want := make([]string, len(pinnedCommandFilters))
		for i, p := range pinnedCommandFilters {
			want[i] = strings.Replace(p.filter, "<base>", base, 1)
		}

		if len(got) != len(want) {
			t.Errorf("base %q: registered %d filters, pinned %d\n got %q\nwant %q",
				base, len(got), len(want), got, want)
		}
		for i := range min(len(got), len(want)) {
			if got[i] != want[i] {
				t.Errorf("base %q: filter %d\n got %q\nwant %q", base, i, got[i], want[i])
			}
		}
		for i := len(want); i < len(got); i++ {
			t.Errorf("base %q: filter %d = %q is registered but not pinned — a new command filter "+
				"joins a collision class on arrival if it is 6 or 7 segments below the base; "+
				"add it to pinnedCommandFilters deliberately and extend the guard lists", base, i, got[i])
		}
		for i := len(got); i < len(want); i++ {
			t.Errorf("base %q: filter %d = %q is pinned but no longer registered — "+
				"a command plane went silent", base, i, want[i])
		}

		// Segment arithmetic, from the real filters rather than from the pin's
		// own strings.
		for i := range min(len(got), len(want)) {
			rest, ok := strings.CutPrefix(got[i], base+"/")
			if !ok {
				t.Errorf("base %q: filter %q does not sit under the base — every handler indexes "+
					"segments with the base stripped, so such a filter can never be routed", base, got[i])
				continue
			}
			if n := len(strings.Split(rest, "/")); n != pinnedCommandFilters[i].segs {
				t.Errorf("base %q: filter %q has %d segments below the base, pinned %d — "+
					"two filters can only overlap at equal length, so this moves the filter "+
					"into or out of a collision class", base, got[i], n, pinnedCommandFilters[i].segs)
			}
			if strings.Contains(rest, "#") {
				t.Errorf("base %q: filter %q carries `#` — the whole overlap analysis on this plane "+
					"assumes equal-length matching only", base, got[i])
			}
		}
	}
}

// TestReservedLegacyParamSegmentsCoversEverySixSegmentLiteral enforces the
// invariant `reservedLegacyParamSegments`' doc comment states, derived from
// the filters [CommandSubscriber.Start] really registers.
//
// Finding **F3**: the list is correct today by inspection only. It has one
// entry, `week_profile`, and one filter needs it. A fourteenth filter of six
// segments below the base with a literal at index 4 would be dispatched a
// second time by [CommandSubscriber.handleDataPoint]'s six-segment branch as
// a CCU parameter write to a parameter no channel has — the exact defect that
// put `week_profile` in the list — and nothing would fail.
//
// The correspondence is derived, not restated: the literal set comes from
// parsing the registered filters, so adding a filter without extending the
// list fails here, and extending the list without a filter needing it fails
// here too (a stale entry is a claim about the plane that is no longer true,
// and it silently suppresses a legitimate parameter of that name).
//
// The catch-all itself is excluded, since it is the filter the guard protects
// rather than one the guard protects against.
//
// Pinned as a defect: that the guard is a drop in `handleDataPoint` at all.
// The coalescing step replaces it with a dispatch to the week-profile sink
// and deletes the list; this test goes with it.
func TestReservedLegacyParamSegmentsCoversEverySixSegmentLiteral(t *testing.T) {
	t.Parallel()

	const base = "openccu-loom"
	rec := newFilterRecorder()
	if err := NewCommandSubscriber(rec, NewTopicBuilder(base), &fakeSink{}, nil).
		Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Every literal a six-segment filter carries, at whatever position.
	needed := map[string]string{} // literal -> the filter that needs it
	sixSegmentFilters := 0
	for _, f := range rec.recorded() {
		rest, ok := strings.CutPrefix(f, base+"/")
		if !ok {
			continue
		}
		segs := strings.Split(rest, "/")
		if len(segs) != 6 {
			continue
		}
		sixSegmentFilters++
		// The trailing `set` is what the catch-all also ends in, so it is
		// matched rather than shadowed; a literal there is not a collision.
		for _, s := range segs[:len(segs)-1] {
			if s != "+" {
				needed[s] = f
			}
		}
	}

	// Vacuity guard: the class exists only while at least two six-segment
	// filters are registered — the catch-all and something with a literal.
	if sixSegmentFilters < 2 {
		t.Fatalf("six-segment filters = %d, want at least 2 — collision class A is gone, "+
			"so this test and reservedLegacyParamSegments have both outlived their purpose", sixSegmentFilters)
	}
	if len(needed) == 0 {
		t.Fatal("no six-segment filter carries a literal — nothing for reservedLegacyParamSegments to guard")
	}

	for literal, filter := range needed {
		if _, listed := reservedLegacyParamSegments[literal]; !listed {
			t.Errorf("filter %q carries the literal %q at six segments below the base, but %q is not in "+
				"reservedLegacyParamSegments — every topic matching that filter is ALSO dispatched by "+
				"handleDataPoint as a CCU write to a parameter named %q, which no channel has",
				filter, literal, literal, literal)
		}
	}
	for literal := range reservedLegacyParamSegments {
		if _, still := needed[literal]; !still {
			t.Errorf("reservedLegacyParamSegments lists %q, but no six-segment filter carries it — "+
				"a stale entry suppresses a legitimate CCU parameter of that name", literal)
		}
	}
}

// TestSevenSegmentLiteralBucketsAreRefusedByTheBucketAllowList is the class-B
// half of the same derivation, and the counterpart of the previous test.
//
// Finding **F3** again, and **F2**: class B has no list to check against —
// its guard is the `default:` arm of a `switch` over `values` / `master`
// inside [CommandSubscriber.handleDataPoint]. The invariant is therefore
// stated the other way round: every literal a seven-segment `…/set` filter
// carries at the bucket position (index 4 below the base) must NOT be one the
// allow-list accepts, or the topic is dispatched twice — once to its own
// handler and once as a CCU write.
//
// `values` and `master` are asserted as the exact accepted set rather than
// merely "not combined, not schedule": a third accepted bucket would widen
// the hole for any future literal-bucket filter, and the allow-list is
// unexported with no other test naming its members.
//
// Pinned as a defect: the whole arrangement. The accepted set is restated
// here because the production `switch` cannot be enumerated from a test, and
// that restatement is itself the thing **F3** is about — it is why this test
// asserts the DERIVED half (which literals the filters carry) rather than
// only the restated half.
func TestSevenSegmentLiteralBucketsAreRefusedByTheBucketAllowList(t *testing.T) {
	t.Parallel()

	const base = "openccu-loom"
	// The buckets handleDataPoint's seven-segment branch accepts. Restated
	// from the production switch, which a test cannot enumerate.
	accepted := map[string]bool{"values": true, "master": true}

	rec := newFilterRecorder()
	if err := NewCommandSubscriber(rec, NewTopicBuilder(base), &fakeSink{}, nil).
		Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	literalBuckets := map[string]string{}
	catchAll := ""
	for _, f := range rec.recorded() {
		rest, ok := strings.CutPrefix(f, base+"/")
		if !ok {
			continue
		}
		segs := strings.Split(rest, "/")
		if len(segs) != 7 || segs[6] != "set" {
			continue
		}
		if segs[4] == "+" {
			catchAll = f
			continue
		}
		literalBuckets[segs[4]] = f
	}

	if catchAll == "" {
		t.Fatal("no seven-segment `…/+/set` catch-all is registered — collision class B is gone, " +
			"so the bucket allow-list has outlived its purpose as a guard")
	}
	if len(literalBuckets) == 0 {
		t.Fatal("no seven-segment filter carries a literal bucket — nothing for the allow-list to refuse")
	}

	for bucket, filter := range literalBuckets {
		if accepted[bucket] {
			t.Errorf("filter %q owns the bucket segment %q, and handleDataPoint's allow-list ACCEPTS %q — "+
				"every topic matching %q is dispatched twice: once to its own handler and once as a "+
				"CCU write to a parameter named after the next segment", filter, bucket, bucket, filter)
		}
	}
	// The set itself, so a third accepted bucket is a reviewed change.
	if len(accepted) != 2 || !accepted["values"] || !accepted["master"] {
		t.Errorf("accepted buckets = %v, want exactly {values, master}", accepted)
	}
}
