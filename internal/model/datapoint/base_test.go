// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package datapoint_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// fakePublisher is a deterministic [datapoint.EventPublisher] for unit
// tests. It records every call so assertions can verify the key /
// value pair the data point routed through it.
type fakePublisher struct {
	mu    sync.Mutex
	calls []fakePublisherCall
}

type fakePublisherCall struct {
	key   string
	value any
}

func (f *fakePublisher) PublishUpdate(_ context.Context, key string, value any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakePublisherCall{key: key, value: value})
}

func (f *fakePublisher) snapshot() []fakePublisherCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakePublisherCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// Compile-time guard: BaseDataPointFields satisfies the
// BaseDataPoint interface via promotion when embedded.
var _ datapoint.BaseDataPoint = (*baseDataPointEmbedder)(nil)

type baseDataPointEmbedder struct {
	datapoint.BaseDataPointFields
}

func TestBaseDataPointUniqueIDFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		centralName string
		address     string
		key         string
		want        string
	}{
		{
			name:        "channel-bound parameter",
			centralName: "ccu-prod",
			address:     "VCU0123456:1",
			key:         "LEVEL",
			want:        "ccu-prod:VCU0123456:1:LEVEL",
		},
		{
			name:        "hub sysvar without address",
			centralName: "ccu-prod",
			address:     "",
			key:         "energy_today",
			want:        "ccu-prod::energy_today",
		},
		{
			name:        "device-level dp",
			centralName: "ccu-test",
			address:     "VCU000001",
			key:         "RSSI_DEVICE",
			want:        "ccu-test:VCU000001:RSSI_DEVICE",
		},
		{
			name:        "second central with same address",
			centralName: "ccu-secondary",
			address:     "VCU0123456:1",
			key:         "LEVEL",
			want:        "ccu-secondary:VCU0123456:1:LEVEL",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := datapoint.NewBaseDataPointFields(tc.centralName, tc.address, tc.key)
			if got := b.UniqueID(); got != tc.want {
				t.Fatalf("UniqueID() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBaseDataPointVisibleDefault(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu-prod", "VCU1:1", "STATE")
	if !b.Visible() {
		t.Fatal("default Visible() must be true (no forced usage)")
	}
	if !b.EnabledByDefault() {
		t.Fatal("default EnabledByDefault() must be true (no forced usage)")
	}
}

func TestBaseDataPointVisibleAfterNoCreate(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu-prod", "VCU1:1", "STATE")
	b.SetForcedUsage(hmenum.DataPointUsageNoCreate)

	if b.Visible() {
		t.Fatal("forced NoCreate must hide the data point (Visible()==false)")
	}
	if b.EnabledByDefault() {
		t.Fatal("forced NoCreate must disable the data point by default")
	}
}

func TestBaseDataPointVisibleAfterCDPPrimary(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu-prod", "VCU1:1", "STATE")
	b.SetForcedUsage(hmenum.DataPointUsageCDPPrimary)

	if !b.Visible() {
		t.Fatal("forced CDPPrimary must keep the DP visible")
	}
	if !b.EnabledByDefault() {
		t.Fatal("forced CDPPrimary must keep EnabledByDefault==true")
	}
}

func TestBaseDataPointEnabledByDefaultUsageMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		usage hmenum.DataPointUsage
		want  bool
	}{
		{hmenum.DataPointUsageCDPPrimary, true},
		{hmenum.DataPointUsageCDPVisible, true},
		{hmenum.DataPointUsageDataPoint, true},
		{hmenum.DataPointUsageEvent, true},
		{hmenum.DataPointUsageCDPSecondary, false},
		{hmenum.DataPointUsageNoCreate, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.usage), func(t *testing.T) {
			t.Parallel()
			b := datapoint.NewBaseDataPointFields("ccu", "VCU1:1", "P")
			b.SetForcedUsage(tc.usage)
			if got := b.EnabledByDefault(); got != tc.want {
				t.Fatalf("usage=%q EnabledByDefault()=%v want %v", tc.usage, got, tc.want)
			}
		})
	}
}

// TestBaseDataPointVisibleUsageMatrix pins V6: [Visible] hides for
// **both** [hmenum.DataPointUsageNoCreate] and
// [hmenum.DataPointUsageCDPSecondary]. CDPSecondary marks a DP that
// is owned by a parent combined / custom DP and must not surface as
// a top-level entity. Earlier versions only checked NoCreate which
// let a CDPSecondary-forced DP slip through every embedder that did
// not shadow Visible (hub.HubDataPoint, combined.WeekProfile,
// weekprofile.ProfileDataPoint).
func TestBaseDataPointVisibleUsageMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		usage hmenum.DataPointUsage
		want  bool
	}{
		{hmenum.DataPointUsageCDPPrimary, true},
		{hmenum.DataPointUsageCDPVisible, true},
		{hmenum.DataPointUsageDataPoint, true},
		{hmenum.DataPointUsageEvent, true},
		{hmenum.DataPointUsageCDPSecondary, false},
		{hmenum.DataPointUsageNoCreate, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.usage), func(t *testing.T) {
			t.Parallel()
			b := datapoint.NewBaseDataPointFields("ccu", "VCU1:1", "P")
			b.SetForcedUsage(tc.usage)
			if got := b.Visible(); got != tc.want {
				t.Fatalf("usage=%q Visible()=%v want %v", tc.usage, got, tc.want)
			}
			// Invariant: Visible == EnabledByDefault for the foundation
			// layer. Any drift between the two on raw BaseDataPointFields
			// is a regression.
			if got, want := b.Visible(), b.EnabledByDefault(); got != want {
				t.Fatalf("usage=%q Visible()=%v but EnabledByDefault()=%v — must match",
					tc.usage, got, want)
			}
		})
	}
}

func TestBaseDataPointSetForcedUsageRoundtrip(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu", "VCU1:1", "P")

	if _, ok := b.ForcedUsage(); ok {
		t.Fatal("fresh BaseDataPointFields must not report a forced usage")
	}

	b.SetForcedUsage(hmenum.DataPointUsageCDPVisible)
	got, ok := b.ForcedUsage()
	if !ok {
		t.Fatal("after SetForcedUsage, ForcedUsage() must report ok=true")
	}
	if got != hmenum.DataPointUsageCDPVisible {
		t.Fatalf("ForcedUsage()=%q want CDPVisible", got)
	}

	// Overriding works: round-trip a second value.
	b.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	got, ok = b.ForcedUsage()
	if !ok || got != hmenum.DataPointUsageNoCreate {
		t.Fatalf("override failed: got=%q ok=%v", got, ok)
	}
}

func TestBaseDataPointPublishUpdate(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu-prod", "VCU0123456:1", "LEVEL")
	pub := &fakePublisher{}
	b.SetPublisher(pub)

	b.PublishUpdate(context.Background(), 0.42)

	calls := pub.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 publisher call, got %d", len(calls))
	}
	want := "ccu-prod:VCU0123456:1:LEVEL"
	if calls[0].key != want {
		t.Fatalf("publisher key=%q want %q", calls[0].key, want)
	}
	v, ok := calls[0].value.(float64)
	if !ok || v != 0.42 {
		t.Fatalf("publisher value=%v want 0.42 (float64)", calls[0].value)
	}
}

func TestBaseDataPointPublishUpdateNilPublisherSafe(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu", "VCU1:1", "P")

	// Without a publisher: must be a silent no-op.
	b.PublishUpdate(context.Background(), 1)

	// Explicitly setting nil also works.
	b.SetPublisher(nil)
	b.PublishUpdate(context.Background(), 2)
}

func TestBaseDataPointConcurrent(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu-prod", "VCU0123456:1", "LEVEL")
	pub := &countingPublisher{}
	b.SetPublisher(pub)

	const (
		writers    = 4
		readers    = 4
		iterations = 200
	)

	usages := []hmenum.DataPointUsage{
		hmenum.DataPointUsageCDPPrimary,
		hmenum.DataPointUsageCDPVisible,
		hmenum.DataPointUsageDataPoint,
		hmenum.DataPointUsageEvent,
		hmenum.DataPointUsageCDPSecondary,
		hmenum.DataPointUsageNoCreate,
	}

	var wg sync.WaitGroup

	// Writers: spin SetForcedUsage / SetPublisher / PublishUpdate.
	for w := 0; w < writers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for i := 0; i < iterations; i++ {
				b.SetForcedUsage(usages[(w+i)%len(usages)])
				if i%17 == 0 {
					b.SetPublisher(pub)
				}
				b.PublishUpdate(ctx, i)
			}
		}()
	}

	// Readers: spin UniqueID / Visible / EnabledByDefault / ForcedUsage.
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = b.UniqueID()
				_ = b.Visible()
				_ = b.EnabledByDefault()
				_, _ = b.ForcedUsage()
			}
		}()
	}

	wg.Wait()

	// Sanity check: every PublishUpdate from every writer reached
	// the publisher.
	if got, want := pub.count(), int64(writers*iterations); got != want {
		t.Fatalf("publisher saw %d calls, want %d", got, want)
	}
}

// countingPublisher is a low-overhead [datapoint.EventPublisher] used
// by the concurrency test where per-call payload introspection is
// unnecessary.
type countingPublisher struct {
	n atomic.Int64
}

func (c *countingPublisher) PublishUpdate(_ context.Context, _ string, _ any) {
	c.n.Add(1)
}

func (c *countingPublisher) count() int64 { return c.n.Load() }

// TestBaseDataPointEmbedderSatisfiesInterface verifies the canonical
// embedding pattern: a struct that embeds [datapoint.BaseDataPointFields]
// automatically satisfies [datapoint.BaseDataPoint] via method
// promotion.
func TestBaseDataPointEmbedderSatisfiesInterface(t *testing.T) {
	t.Parallel()

	e := &baseDataPointEmbedder{
		BaseDataPointFields: datapoint.NewBaseDataPointFields(
			"ccu-prod", "VCU0123456:1", "LEVEL",
		),
	}

	var iface datapoint.BaseDataPoint = e
	if iface.UniqueID() != "ccu-prod:VCU0123456:1:LEVEL" {
		t.Fatalf("UniqueID() promoted incorrectly: %q", iface.UniqueID())
	}
	if !iface.Visible() {
		t.Fatal("default Visible() via promotion must be true")
	}
	if !iface.EnabledByDefault() {
		t.Fatal("default EnabledByDefault() via promotion must be true")
	}
}

// PublishedEventAt / PublishedEventRecently

func TestPublishedEventAtZeroBeforePublish(t *testing.T) {
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "STATE")
	if !b.PublishedEventAt().IsZero() {
		t.Fatal("PublishedEventAt() must be zero before any publish")
	}
}

func TestPublishedEventRecentlyFalseBeforePublish(t *testing.T) {
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "STATE")
	if b.PublishedEventRecently() {
		t.Fatal("PublishedEventRecently() must be false before any publish")
	}
}

func TestPublishedEventAtNotStampedWithNilPublisher(t *testing.T) {
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "STATE")
	// no publisher wired → PublishUpdate is a no-op
	b.PublishUpdate(context.Background(), "value")
	if !b.PublishedEventAt().IsZero() {
		t.Fatal("PublishedEventAt() must remain zero when publisher is nil")
	}
}

func TestPublishedEventAtStampedAfterPublish(t *testing.T) {
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "STATE")
	pub := &fakePublisher{}
	b.SetPublisher(pub)
	b.PublishUpdate(context.Background(), "value")
	if b.PublishedEventAt().IsZero() {
		t.Fatal("PublishedEventAt() must be non-zero after a successful publish")
	}
}

func TestPublishedEventRecentlyTrueAfterPublish(t *testing.T) {
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "STATE")
	pub := &fakePublisher{}
	b.SetPublisher(pub)
	b.PublishUpdate(context.Background(), "value")
	if !b.PublishedEventRecently() {
		t.Fatal("PublishedEventRecently() must be true immediately after a publish")
	}
}

// --- M-8: Register / Unregister / IsRegistered ---

// TestIsRegisteredFalseByDefault verifies that a freshly constructed
// BaseDataPointFields reports IsRegistered() = false (M-8).
func TestIsRegisteredFalseByDefault(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	if b.IsRegistered() {
		t.Fatal("IsRegistered() must be false on a fresh BaseDataPointFields")
	}
}

// TestMarkRegisteredFlipsFlag verifies that MarkRegistered makes
// IsRegistered() return true (M-8).
func TestMarkRegisteredFlipsFlag(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	b.MarkRegistered()
	if !b.IsRegistered() {
		t.Fatal("IsRegistered() must be true after MarkRegistered()")
	}
}

// TestUnmarkRegisteredClearsFlag verifies that UnmarkRegistered
// resets IsRegistered() to false (M-8).
func TestUnmarkRegisteredClearsFlag(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	b.MarkRegistered()
	b.UnmarkRegistered()
	if b.IsRegistered() {
		t.Fatal("IsRegistered() must be false after UnmarkRegistered()")
	}
}

// TestMarkRegisteredIdempotent verifies that calling MarkRegistered
// twice leaves IsRegistered() = true (idempotent call semantics match
func TestMarkRegisteredIdempotent(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	b.MarkRegistered()
	b.MarkRegistered()
	if !b.IsRegistered() {
		t.Fatal("IsRegistered() must remain true after two MarkRegistered() calls")
	}
}

// ─── Foundation timestamps ────────────────────────────────────────────

// TestModifiedAtZeroByDefault verifies that a freshly constructed
// BaseDataPointFields has a zero ModifiedAt timestamp.
func TestModifiedAtZeroByDefault(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	if !b.ModifiedAt().IsZero() {
		t.Fatal("ModifiedAt() must be zero on a fresh BaseDataPointFields")
	}
}

// TestRefreshedAtZeroByDefault verifies that a freshly constructed
// BaseDataPointFields has a zero RefreshedAt timestamp.
func TestRefreshedAtZeroByDefault(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	if !b.RefreshedAt().IsZero() {
		t.Fatal("RefreshedAt() must be zero on a fresh BaseDataPointFields")
	}
}

// TestMarkModifiedSetsTimestamp verifies that MarkModified stores the
// supplied timestamp and ModifiedAt returns it.
func TestMarkModifiedSetsTimestamp(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	stamp := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	b.MarkModified(stamp)
	if got := b.ModifiedAt(); !got.Equal(stamp) {
		t.Fatalf("ModifiedAt()=%v want %v", got, stamp)
	}
}

// TestRefreshedAtRoundtrip verifies that MarkRefreshed stores the
// supplied timestamp and RefreshedAt returns it.
func TestRefreshedAtRoundtrip(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	stamp := time.Date(2026, 4, 29, 13, 0, 0, 0, time.UTC)
	b.MarkRefreshed(stamp)
	if got := b.RefreshedAt(); !got.Equal(stamp) {
		t.Fatalf("RefreshedAt()=%v want %v", got, stamp)
	}
}

// TestModifiedRecentlyTrueWithin500ms verifies that ModifiedRecently
// returns true when MarkModified was called less than 500 ms ago.
func TestModifiedRecentlyTrueWithin500ms(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	b.MarkModified(time.Now())
	if !b.ModifiedRecently() {
		t.Fatal("ModifiedRecently() must be true immediately after MarkModified")
	}
}

// TestModifiedRecentlyFalseAfterWindow verifies that ModifiedRecently
// returns false when the modified timestamp is older than 500 ms.
func TestModifiedRecentlyFalseAfterWindow(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	// Timestamp far in the past — well outside the 500 ms window.
	b.MarkModified(time.Now().Add(-2 * time.Second))
	if b.ModifiedRecently() {
		t.Fatal("ModifiedRecently() must be false when modified > 500 ms ago")
	}
}

// TestModifiedRecentlyFalseWhenZero verifies that ModifiedRecently
// returns false when the timestamp is the zero value (never modified).
func TestModifiedRecentlyFalseWhenZero(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	if b.ModifiedRecently() {
		t.Fatal("ModifiedRecently() must be false when ModifiedAt is zero")
	}
}

// TestRefreshedRecentlyTrueWithin500ms verifies that RefreshedRecently
// returns true when MarkRefreshed was called less than 500 ms ago.
func TestRefreshedRecentlyTrueWithin500ms(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	b.MarkRefreshed(time.Now())
	if !b.RefreshedRecently() {
		t.Fatal("RefreshedRecently() must be true immediately after MarkRefreshed")
	}
}

// TestRefreshedRecentlyFalseAfterWindow verifies that RefreshedRecently
// returns false when the refreshed timestamp is older than 500 ms.
func TestRefreshedRecentlyFalseAfterWindow(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	b.MarkRefreshed(time.Now().Add(-2 * time.Second))
	if b.RefreshedRecently() {
		t.Fatal("RefreshedRecently() must be false when refreshed > 500 ms ago")
	}
}

// TestConcurrentMarkAndRead is a race-detector test verifying that
// concurrent MarkModified / MarkRefreshed / ModifiedAt / RefreshedAt /
// ModifiedRecently / RefreshedRecently calls on the same
// BaseDataPointFields are free of data races.
func TestConcurrentMarkAndRead(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")

	const goroutines = 8
	const iterations = 200

	var wg sync.WaitGroup

	// Writers: alternate between MarkModified and MarkRefreshed.
	for w := 0; w < goroutines/2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				now := time.Now()
				if i%2 == 0 {
					b.MarkModified(now)
				} else {
					b.MarkRefreshed(now)
				}
			}
		}()
	}

	// Readers: exercise all four read methods.
	for r := 0; r < goroutines/2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = b.ModifiedAt()
				_ = b.RefreshedAt()
				_ = b.ModifiedRecently()
				_ = b.RefreshedRecently()
			}
		}()
	}

	wg.Wait()
}

// ─── Cluster 1 — Cached presentation surface ────────────────────────────────

// TestNameDataCaching verifies that SetNameData stores the quadruple and the
// convenience getters delegate correctly.
func TestNameDataCaching(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu", "VCU1234567:3", "STATE")
	if !b.NameData().IsZero() {
		t.Fatalf("zero-state NameData() must be empty, got %+v", b.NameData())
	}
	if b.Name() != "" || b.FullName() != "" || b.TranslatedName() != "" || b.TranslatedFullName() != "" {
		t.Fatal("zero-state name accessors must all return empty strings")
	}

	nd := naming.NameData{
		DeviceName:              "Wohnzimmer",
		ChannelName:             "Wohnzimmer",
		ParameterName:           "State ch3",
		TranslatedParameterName: "Schalter ch3",
	}
	b.SetNameData(nd)

	if got := b.NameData(); got != nd {
		t.Errorf("NameData() = %+v, want %+v", got, nd)
	}
	if got := b.Name(); got != "State ch3" {
		t.Errorf("Name() = %q, want %q", got, "State ch3")
	}
	if got := b.FullName(); got != "Wohnzimmer State ch3" {
		t.Errorf("FullName() = %q, want %q", got, "Wohnzimmer State ch3")
	}
	if got := b.TranslatedName(); got != "Schalter ch3" {
		t.Errorf("TranslatedName() = %q, want %q", got, "Schalter ch3")
	}
	if got := b.TranslatedFullName(); got != "Wohnzimmer Schalter ch3" {
		t.Errorf("TranslatedFullName() = %q, want %q", got, "Wohnzimmer Schalter ch3")
	}
}

// TestPathDataCaching verifies that SetPathData stores the path strings and
// the convenience getters delegate correctly.
func TestPathDataCaching(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu", "VCU1234567:3", "STATE")
	if !b.PathData().IsZero() {
		t.Fatalf("zero-state PathData() must be empty, got %+v", b.PathData())
	}
	if b.SetPath() != "" || b.StatePath() != "" {
		t.Fatal("zero-state set/state path accessors must all return empty strings")
	}

	pd := naming.NewDataPointPathData(hmenum.InterfaceHmIPRF, "VCU1234567", 3, naming.BucketValues, "STATE")
	b.SetPathData(pd)

	if got := b.PathData(); got != pd {
		t.Errorf("PathData() = %+v, want %+v", got, pd)
	}
	if got := b.SetPath(); got != "device/set/VCU1234567/3/values/STATE" {
		t.Errorf("SetPath() = %q, want %q", got, "device/set/VCU1234567/3/values/STATE")
	}
	if got := b.StatePath(); got != "device/status/VCU1234567/3/values/STATE" {
		t.Errorf("StatePath() = %q, want %q", got, "device/status/VCU1234567/3/values/STATE")
	}
}

// TestIsInMultipleChannelsCaching verifies the multi-channel flag.
func TestIsInMultipleChannelsCaching(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu", "VCU1234567:3", "STATE")
	if b.IsInMultipleChannels() {
		t.Fatal("default IsInMultipleChannels() must be false")
	}

	b.SetIsInMultipleChannels(true)
	if !b.IsInMultipleChannels() {
		t.Fatal("after SetIsInMultipleChannels(true), getter must return true")
	}

	b.SetIsInMultipleChannels(false)
	if b.IsInMultipleChannels() {
		t.Fatal("after SetIsInMultipleChannels(false), getter must return false")
	}
}

// TestIsRefreshedPredicate verifies the boolean predicate based on
// MarkRefreshed timestamp.
func TestIsRefreshedPredicate(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu", "VCU1234567:3", "STATE")
	if b.IsRefreshed() {
		t.Fatal("freshly-constructed DP must report IsRefreshed() = false")
	}

	b.MarkRefreshed(time.Now())
	if !b.IsRefreshed() {
		t.Fatal("after MarkRefreshed, IsRefreshed() must be true")
	}
}

// ─── Unconfirmed timestamp slots ─────────────────────────────────────────────

// TestUnconfirmedModifiedAtZeroByDefault verifies that a freshly constructed
// BaseDataPointFields has zero unconfirmed timestamps.
func TestUnconfirmedModifiedAtZeroByDefault(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	if !b.UnconfirmedModifiedAt().IsZero() {
		t.Fatal("UnconfirmedModifiedAt() must be zero on fresh BaseDataPointFields")
	}
	if !b.UnconfirmedRefreshedAt().IsZero() {
		t.Fatal("UnconfirmedRefreshedAt() must be zero on fresh BaseDataPointFields")
	}
}

// TestMarkUnconfirmedModifiedSetsTimestamps verifies that
// MarkUnconfirmedModified sets both the modified and refreshed unconfirmed
// timestamps.
func TestMarkUnconfirmedModifiedSetsTimestamps(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	stamp := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	b.MarkUnconfirmedModified(stamp)
	if got := b.UnconfirmedModifiedAt(); !got.Equal(stamp) {
		t.Fatalf("UnconfirmedModifiedAt()=%v want %v", got, stamp)
	}
	// _set_unconfirmed_modified_at also sets _unconfirmed_refreshed_at
	if got := b.UnconfirmedRefreshedAt(); !got.Equal(stamp) {
		t.Fatalf("UnconfirmedRefreshedAt()=%v want %v (must be set by MarkUnconfirmedModified)", got, stamp)
	}
}

// TestMarkUnconfirmedRefreshedDoesNotTouchModified verifies that
// MarkUnconfirmedRefreshed sets only the refreshed timestamp, not the
// modified timestamp.
func TestMarkUnconfirmedRefreshedDoesNotTouchModified(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	stamp := time.Date(2026, 4, 29, 13, 0, 0, 0, time.UTC)
	b.MarkUnconfirmedRefreshed(stamp)
	if got := b.UnconfirmedRefreshedAt(); !got.Equal(stamp) {
		t.Fatalf("UnconfirmedRefreshedAt()=%v want %v", got, stamp)
	}
	if !b.UnconfirmedModifiedAt().IsZero() {
		t.Fatal("MarkUnconfirmedRefreshed must not touch UnconfirmedModifiedAt")
	}
}

// TestResetUnconfirmedTimestampsClears verifies that
// ResetUnconfirmedTimestamps sets both unconfirmed timestamps back to zero.
func TestResetUnconfirmedTimestampsClears(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	stamp := time.Now()
	b.MarkUnconfirmedModified(stamp)
	b.ResetUnconfirmedTimestamps()
	if !b.UnconfirmedModifiedAt().IsZero() {
		t.Fatal("UnconfirmedModifiedAt() must be zero after Reset")
	}
	if !b.UnconfirmedRefreshedAt().IsZero() {
		t.Fatal("UnconfirmedRefreshedAt() must be zero after Reset")
	}
}

// ─── modified_at / refreshed_at blend ────────────────────────────────────────

// TestModifiedAtBlendReturnsUnconfirmedWhenLater verifies that ModifiedAt
// returns the unconfirmed timestamp when it is more recent than the confirmed
// one.
func TestModifiedAtBlendReturnsUnconfirmedWhenLater(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	earlier := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	later := time.Date(2026, 4, 28, 11, 0, 0, 0, time.UTC)
	b.MarkModified(earlier)
	b.MarkUnconfirmedModified(later)
	if got := b.ModifiedAt(); !got.Equal(later) {
		t.Fatalf("ModifiedAt()=%v want unconfirmed %v", got, later)
	}
}

// TestModifiedAtBlendReturnsConfirmedWhenLater verifies that ModifiedAt
// returns the confirmed timestamp when it is more recent than the unconfirmed
// one.
func TestModifiedAtBlendReturnsConfirmedWhenLater(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	earlier := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	later := time.Date(2026, 4, 28, 11, 0, 0, 0, time.UTC)
	b.MarkUnconfirmedModified(earlier)
	b.MarkModified(later)
	if got := b.ModifiedAt(); !got.Equal(later) {
		t.Fatalf("ModifiedAt()=%v want confirmed %v", got, later)
	}
}

// TestRefreshedAtBlendReturnsUnconfirmedWhenLater verifies that RefreshedAt
// returns the unconfirmed timestamp when it is more recent than the confirmed
// one.
func TestRefreshedAtBlendReturnsUnconfirmedWhenLater(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	earlier := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	later := time.Date(2026, 4, 28, 11, 0, 0, 0, time.UTC)
	b.MarkRefreshed(earlier)
	b.MarkUnconfirmedRefreshed(later)
	if got := b.RefreshedAt(); !got.Equal(later) {
		t.Fatalf("RefreshedAt()=%v want unconfirmed %v", got, later)
	}
}

// TestRefreshedAtBlendReturnsConfirmedWhenLater verifies that RefreshedAt
// returns the confirmed timestamp when it is more recent.
func TestRefreshedAtBlendReturnsConfirmedWhenLater(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	earlier := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	later := time.Date(2026, 4, 28, 11, 0, 0, 0, time.UTC)
	b.MarkUnconfirmedRefreshed(earlier)
	b.MarkRefreshed(later)
	if got := b.RefreshedAt(); !got.Equal(later) {
		t.Fatalf("RefreshedAt()=%v want confirmed %v", got, later)
	}
}

// TestBlendResetAfterConfirm verifies that after ResetUnconfirmedTimestamps
// the blend falls back to the confirmed value (reset path).
func TestBlendResetAfterConfirm(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	confirmed := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	unconfirmed := time.Date(2026, 4, 28, 11, 0, 0, 0, time.UTC)
	b.MarkModified(confirmed)
	b.MarkUnconfirmedModified(unconfirmed)
	// unconfirmed wins now
	if got := b.ModifiedAt(); !got.Equal(unconfirmed) {
		t.Fatalf("expected unconfirmed %v before reset, got %v", unconfirmed, got)
	}
	b.ResetUnconfirmedTimestamps()
	// confirmed wins after reset
	if got := b.ModifiedAt(); !got.Equal(confirmed) {
		t.Fatalf("expected confirmed %v after reset, got %v", confirmed, got)
	}
}

// ─── PublishDataPointUpdatedEvent with old/new value ─────────────────────────

// TestPublishDataPointUpdatedEventOldNew verifies that
// PublishDataPointUpdatedEvent routes a DataPointUpdatedPayload carrying both
// old and new values to the registered publisher.
func TestPublishDataPointUpdatedEventOldNew(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	pub := &fakePublisher{}
	b.SetPublisher(pub)

	b.PublishDataPointUpdatedEvent(context.Background(), 0.25, 0.75)

	calls := pub.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 publisher call, got %d", len(calls))
	}
	payload, ok := calls[0].value.(datapoint.UpdatedPayload)
	if !ok {
		t.Fatalf("publisher value must be UpdatedPayload, got %T", calls[0].value)
	}
	if payload.OldValue != 0.25 {
		t.Fatalf("OldValue=%v want 0.25", payload.OldValue)
	}
	if payload.NewValue != 0.75 {
		t.Fatalf("NewValue=%v want 0.75", payload.NewValue)
	}
}

// TestPublishDataPointUpdatedEventNilPublisherSafe verifies that
// PublishDataPointUpdatedEvent is a silent no-op when no publisher is
// installed (nil-safe invariant).
func TestPublishDataPointUpdatedEventNilPublisherSafe(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	// must not panic
	b.PublishDataPointUpdatedEvent(context.Background(), 1, 2)
}

// TestPublishDataPointUpdatedEventStampsPublishedEventAt verifies that
// PublishDataPointUpdatedEvent updates the PublishedEventAt timestamp
// (parity with PublishUpdate).
func TestPublishDataPointUpdatedEventStampsPublishedEventAt(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "A:1", "LEVEL")
	pub := &fakePublisher{}
	b.SetPublisher(pub)
	if !b.PublishedEventAt().IsZero() {
		t.Fatal("PublishedEventAt() must be zero before any publish")
	}
	b.PublishDataPointUpdatedEvent(context.Background(), nil, "hello")
	if b.PublishedEventAt().IsZero() {
		t.Fatal("PublishedEventAt() must be non-zero after PublishDataPointUpdatedEvent")
	}
}

// TestBaseDataPointAvailable verifies that Available() delegates to
// the installed provider and defaults to true when none is set.
func TestBaseDataPointAvailable(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu", "VCU:1", "LEVEL")

	// No provider installed: always available.
	if !b.Available() {
		t.Error("Available() without provider should return true")
	}

	// Install a provider that returns false.
	reachable := false
	b.SetAvailabilityProvider(func() bool { return reachable })
	if b.Available() {
		t.Error("Available() with false provider should return false")
	}

	// Flip the provider to return true.
	reachable = true
	if !b.Available() {
		t.Error("Available() with true provider should return true")
	}

	// Clear the provider: back to always available.
	b.SetAvailabilityProvider(nil)
	if !b.Available() {
		t.Error("Available() after clearing provider should return true")
	}
}

// ─── Central / Address / KeyName accessors ──────────────────────────────────

func TestBaseDataPointCentralAddressKeyName(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu-prod", "VCU0123:1", "LEVEL")
	if got := b.Central(); got != "ccu-prod" {
		t.Errorf("Central() = %q, want %q", got, "ccu-prod")
	}
	if got := b.Address(); got != "VCU0123:1" {
		t.Errorf("Address() = %q, want %q", got, "VCU0123:1")
	}
	if got := b.KeyName(); got != "LEVEL" {
		t.Errorf("KeyName() = %q, want %q", got, "LEVEL")
	}
}

// ─── MarkForcedSensor / IsForcedSensor ──────────────────────────────────────

func TestBaseDataPointMarkForcedSensor(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "VCU1:1", "STATE")
	if b.IsForcedSensor() {
		t.Fatal("IsForcedSensor() must be false on fresh BaseDataPointFields")
	}
	b.MarkForcedSensor()
	if !b.IsForcedSensor() {
		t.Fatal("IsForcedSensor() must be true after MarkForcedSensor()")
	}
	// UniqueID must append "_sensor" suffix when forced.
	uid := b.UniqueID()
	if uid == "" {
		t.Fatal("UniqueID() must not be empty")
	}
	if len(uid) < 7 || uid[len(uid)-7:] != "_sensor" {
		t.Errorf("UniqueID() = %q, want _sensor suffix after MarkForcedSensor()", uid)
	}
}

// ─── MarkUnIgnored / IsUnIgnored ────────────────────────────────────────────

func TestBaseDataPointMarkUnIgnored(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "VCU1:1", "STATE")
	if b.IsUnIgnored() {
		t.Fatal("IsUnIgnored() must be false on fresh BaseDataPointFields")
	}
	b.MarkUnIgnored()
	if !b.IsUnIgnored() {
		t.Fatal("IsUnIgnored() must be true after MarkUnIgnored()")
	}
}

// ─── InFlightCommandsCount ────────────────────────────────────────────

// TestInFlightCommandsCountIncDec verifies that Inc/Dec round-trips correctly
// and that the counter floors at zero.
func TestInFlightCommandsCountIncDec(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "VCU1:1", "STATE")
	if got := b.InFlightCommandsCount(); got != 0 {
		t.Fatalf("fresh counter = %d, want 0", got)
	}

	b.IncInFlightCommands()
	b.IncInFlightCommands()
	if got := b.InFlightCommandsCount(); got != 2 {
		t.Fatalf("after 2 Inc: count = %d, want 2", got)
	}

	b.DecInFlightCommands()
	if got := b.InFlightCommandsCount(); got != 1 {
		t.Fatalf("after 1 Dec: count = %d, want 1", got)
	}

	b.DecInFlightCommands()
	if got := b.InFlightCommandsCount(); got != 0 {
		t.Fatalf("after 2 Dec: count = %d, want 0", got)
	}

	// Extra Dec must not go below zero.
	b.DecInFlightCommands()
	if got := b.InFlightCommandsCount(); got != 0 {
		t.Fatalf("Dec below zero: count = %d, want 0", got)
	}
}

// TestInFlightCommandsCountConcurrent verifies that concurrent Inc/Dec
// operations do not race or corrupt the counter.
func TestInFlightCommandsCountConcurrent(t *testing.T) {
	t.Parallel()
	b := datapoint.NewBaseDataPointFields("ccu", "VCU1:1", "STATE")

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	var incCount int64
	for range goroutines {
		go func() {
			defer wg.Done()
			b.IncInFlightCommands()
			atomic.AddInt64(&incCount, 1)
		}()
	}
	wg.Wait()

	if got := b.InFlightCommandsCount(); got != goroutines {
		t.Fatalf("after %d concurrent Inc: count = %d, want %d", goroutines, got, goroutines)
	}

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			b.DecInFlightCommands()
		}()
	}
	wg.Wait()

	if got := b.InFlightCommandsCount(); got != 0 {
		t.Fatalf("after %d concurrent Dec: count = %d, want 0", goroutines, got)
	}
}

// ─── unconfirmedLastValueSend map ─────────────────────────────────────────────

func TestUnconfirmedValueForKeyRoundtrip(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu", "VCU1:1", "STATE")
	key := hmtypes.DataPointKey{ChannelAddress: "VCU1:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "STATE"}

	// Before any write: no entry.
	if _, ok := b.UnconfirmedValueForKey(key); ok {
		t.Fatal("UnconfirmedValueForKey: must return ok=false before any write")
	}

	b.WriteUnconfirmedValueForKey(key, true)
	v, ok := b.UnconfirmedValueForKey(key)
	if !ok {
		t.Fatal("UnconfirmedValueForKey: must return ok=true after WriteUnconfirmedValueForKey")
	}
	if v != true {
		t.Fatalf("UnconfirmedValueForKey: got %v, want true", v)
	}

	b.ConfirmUnconfirmedValueForKey(key)
	if _, ok := b.UnconfirmedValueForKey(key); ok {
		t.Fatal("UnconfirmedValueForKey: must return ok=false after ConfirmUnconfirmedValueForKey")
	}
}

func TestUnconfirmedValueForKeyMultipleKeys(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu", "VCU2:1", "LEVEL")
	k1 := hmtypes.DataPointKey{ChannelAddress: "VCU2:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "LEVEL"}
	k2 := hmtypes.DataPointKey{ChannelAddress: "VCU2:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "STATE"}

	b.WriteUnconfirmedValueForKey(k1, 0.5)
	b.WriteUnconfirmedValueForKey(k2, false)

	// Confirm only k1; k2 must survive.
	b.ConfirmUnconfirmedValueForKey(k1)

	if _, ok := b.UnconfirmedValueForKey(k1); ok {
		t.Fatal("k1 must be absent after confirm")
	}
	v, ok := b.UnconfirmedValueForKey(k2)
	if !ok || v != false {
		t.Fatalf("k2 must still be present with value false, got %v ok=%v", v, ok)
	}
}

func TestUnconfirmedValueForKeyConcurrentWriteRead(t *testing.T) {
	t.Parallel()

	b := datapoint.NewBaseDataPointFields("ccu", "VCU3:1", "LEVEL")
	key := hmtypes.DataPointKey{ChannelAddress: "VCU3:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "LEVEL"}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			b.WriteUnconfirmedValueForKey(key, i)
			// Concurrent read must not race.
			_, _ = b.UnconfirmedValueForKey(key)
		}()
	}
	wg.Wait()

	// After all goroutines finish a value must be present (last writer wins).
	if _, ok := b.UnconfirmedValueForKey(key); !ok {
		t.Fatal("after concurrent writes a value must be present")
	}
}
