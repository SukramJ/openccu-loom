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
// Groups by segment count, which is the same thing as grouping by potential
// collision class — no filter carries `#`, so MQTT filter matching requires
// equal segment counts and two filters of different lengths are disjoint no
// matter what literals they carry:
//
//	3 segments: the two daemon-level planes (alarm, addon_update) — literal
//	            first segment.
//	5 segments: the four hub planes — `hub` literal at index 1 and a second
//	            literal at index 2.
//	6 segments: the legacy bucket-less data-point catch-all, alone. It was
//	            once paired with a `week_profile` filter of the same length,
//	            which is the overlap coalescing removed.
//	7 segments: the bucket-aware data-point catch-all and the cdps/invoke
//	            filter, which escapes it on its literal last segment. The
//	            `combined` and `schedule` filters used to sit here too.
//	8 segments: the per-service-method filter — the only one of its length.
//
// The set is pairwise disjoint, which
// TestCommandFiltersArePairwiseDisjoint asserts by enumeration rather than
// by reading this comment.
var pinnedCommandFilters = []pinnedFilter{
	{"<base>/+/+/+/+/+/+/set", 7},
	{"<base>/+/+/+/+/+/set", 6},
	{"<base>/+/hub/sysvars/+/set", 5},
	{"<base>/+/hub/programs/+/set", 5},
	{"<base>/+/hub/programs/+/trigger", 5},
	{"<base>/+/hub/install_mode/+/set", 5},
	{"<base>/+/devices/+/cdps/+/+/invoke", 7},
	{"<base>/+/+/+/+/custom/+/set/+", 8},
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
// filter of six or seven segments below the base overlaps one of them on
// arrival, and an overlap is multiplied on delivery rather than resolved:
// the broker sends one copy per matching subscription and the client
// re-matches each copy against its whole local filter list, so N overlapping
// filters cost N*N handler runs. MQTT has no exclusion wildcard, so the only
// resolution is to register the catch-all alone and dispatch the narrow
// shape from inside its handler.
//
// The set held three such filters when this pin was taken —
// `<base>/+/+/+/+/week_profile/set`, `…/combined/+/set` and
// `…/schedule/+/set` — and the only things keeping their second dispatch off
// the CCU were two hand-maintained lists in two places:
// `reservedLegacyParamSegments` and the bucket allow-list in
// [CommandSubscriber.handleDataPoint]. Those three rows are gone from the
// pin, the list is deleted, and the property that replaced both is asserted
// by enumeration in TestCommandFiltersArePairwiseDisjoint.
//
// The ORDER is pinned too, including the fact that it is not grouped by
// length. [CommandSubscriber.Start] aborts on the first refused subscribe
// and leaves the partial set live, so the order decides which filters
// survive a partial ACL denial — a property the shared library's rollback
// removes and this daemon still has.
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
				"overlaps a data-point catch-all on arrival if it is 6 or 7 segments below the "+
				"base; add it to pinnedCommandFilters deliberately, and see "+
				"TestCommandFiltersArePairwiseDisjoint", base, i, got[i])
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
					"into or out of reach of a catch-all", base, got[i], n, pinnedCommandFilters[i].segs)
			}
			if strings.Contains(rest, "#") {
				t.Errorf("base %q: filter %q carries `#` — the whole overlap analysis on this plane "+
					"assumes equal-length matching only", base, got[i])
			}
		}
	}
}

// filtersOverlap reports whether two MQTT topic filters can both match some
// topic.
//
// It is the same walk the shared library's unexported
// `publisher.filtersOverlap` performs, restated here because that is the
// predicate `publisher.CommandRouter.Handle` refuses a registration on and
// this test has to answer the same question about this daemon's own set
// without importing the library. Two positions are compatible when either is
// `+` or both are the same literal; a `#` on either side swallows the rest.
func filtersOverlap(a, b []string) bool {
	for {
		switch {
		case len(a) == 0 && len(b) == 0:
			return true
		case len(a) == 0:
			return len(b) == 1 && b[0] == "#"
		case len(b) == 0:
			return len(a) == 1 && a[0] == "#"
		case a[0] == "#" || b[0] == "#":
			return true
		case a[0] != "+" && b[0] != "+" && a[0] != b[0]:
			return false
		}
		a, b = a[1:], b[1:]
	}
}

// TestCommandFiltersArePairwiseDisjoint is the property that replaced both
// hand-maintained collision guards: no two filters
// [CommandSubscriber.Start] registers can both match any topic.
//
// It is asserted by enumerating every pair rather than by reading the filter
// set, because the failure it guards is not visible in any one filter. Two of
// this plane's filters are wildcard catch-alls (`+/+/+/+/+/set` and
// `+/+/+/+/+/+/set`), so a new filter is judged only against them, and a
// reviewer adding one legitimately — a new command shape at six or seven
// segments below the base — has no local reason to look.
//
// What an overlap costs, and why nothing downstream can undo it: a broker
// sends one copy of a message per matching subscription (MQTT 3.1.1 §4.7.3 /
// 5.0 §3.3.4; measured on Mosquitto 2.1.2 on both versions), and go-mqtt then
// re-matches every arriving copy against its whole local filter list and
// calls every matching handler without correlating a copy with the
// subscription it arrived on. The two fan-outs multiply, so N overlapping
// filters turn one operator action into N*N handler runs, indistinguishable
// from N*N genuine publishes. MQTT has no exclusion wildcard, so narrowing a
// catch-all to subtract a sibling shape is inexpressible; the resolution is
// to register the catch-all alone and dispatch the narrow shape from inside
// its handler, which is what handleDataPoint does for `week_profile`,
// `combined` and `schedule`.
//
// Until this held, three of the 78 pairs overlapped and the second dispatch
// was kept off the CCU by `reservedLegacyParamSegments` (one entry) and by
// the bucket allow-list's `default:` drop — two mechanisms, in two places,
// neither derivable from the filter set and both complete by inspection
// only.
//
// It is also the precondition for adopting `publisher.CommandRouter`:
// `Handle` refuses an overlapping pair outright with `ErrAmbiguousRoutes`, so
// a set that fails this test cannot be registered at all.
func TestCommandFiltersArePairwiseDisjoint(t *testing.T) {
	t.Parallel()

	for _, base := range []string{"openccu-loom", "home/loom"} {
		rec := newFilterRecorder()
		if err := NewCommandSubscriber(rec, NewTopicBuilder(base), &fakeSink{}, nil).
			Start(context.Background()); err != nil {
			t.Fatalf("base %q: start: %v", base, err)
		}
		filters := rec.recorded()

		// Vacuity guard: the enumeration is only meaningful over the real
		// set, and a Start that registered nothing would pass trivially.
		if len(filters) < 2 {
			t.Fatalf("base %q: %d filters registered — nothing to enumerate", base, len(filters))
		}

		parts := make([][]string, len(filters))
		for i, f := range filters {
			parts[i] = strings.Split(f, "/")
		}

		pairs, overlaps := 0, 0
		for i := range filters {
			for j := i + 1; j < len(filters); j++ {
				pairs++
				if filtersOverlap(parts[i], parts[j]) {
					overlaps++
					t.Errorf("base %q: %q and %q both match some topic — one command runs "+
						"both handlers, and a broker's per-subscription copy multiplies that "+
						"again; register the general shape only and dispatch the narrow one "+
						"from inside its handler", base, filters[i], filters[j])
				}
			}
		}
		if want := len(filters) * (len(filters) - 1) / 2; pairs != want {
			t.Errorf("base %q: enumerated %d pairs over %d filters, want %d", base, pairs, len(filters), want)
		}
		if overlaps != 0 {
			t.Errorf("base %q: %d of %d pairs overlap, want 0", base, overlaps, pairs)
		}
	}
}

// TestFiltersOverlapDetectsAnOverlap guards the guard above: an overlap
// predicate that answered "no" unconditionally would make
// TestCommandFiltersArePairwiseDisjoint pass over any filter set at all,
// which is precisely the vacuity this programme has been bitten by.
//
// The rows are the three pairs that really did overlap in this plane before
// coalescing, plus the shapes that must NOT be called an overlap — the
// cdps/invoke filter, which sits at seven segments alongside the bucket-aware
// catch-all and escapes it on its literal last segment, and two filters of
// unequal length.
func TestFiltersOverlapDetectsAnOverlap(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"class A: legacy catch-all x week_profile", "gh/+/+/+/+/+/set", "gh/+/+/+/+/week_profile/set", true},
		{"class B: bucket catch-all x combined", "gh/+/+/+/+/+/+/set", "gh/+/+/+/+/combined/+/set", true},
		{"class B: bucket catch-all x schedule", "gh/+/+/+/+/+/+/set", "gh/+/+/+/+/schedule/+/set", true},
		{"identical filters overlap", "gh/+/+/set", "gh/+/+/set", true},
		{"cdps/invoke escapes on its last segment", "gh/+/+/+/+/+/+/set", "gh/+/devices/+/cdps/+/+/invoke", false},
		{"unequal length without `#`", "gh/+/+/+/+/+/set", "gh/+/+/+/+/+/+/set", false},
		{"differing literal at the same position", "gh/+/hub/sysvars/+/set", "gh/+/hub/programs/+/set", false},
		{"`#` swallows the rest", "gh/#", "gh/a/b/c/set", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := filtersOverlap(strings.Split(tc.a, "/"), strings.Split(tc.b, "/")); got != tc.want {
				t.Errorf("filtersOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
