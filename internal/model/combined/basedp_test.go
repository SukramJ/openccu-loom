// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package combined

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Compile-time guards: combined DPs satisfy [datapoint.BaseDataPoint]
// via the embedded [datapoint.BaseDataPointFields]. The runtime tests
// below additionally exercise the promoted methods so the contract is
// not purely static.
var (
	_ datapoint.BaseDataPoint = (*HSColor)(nil)
	_ datapoint.BaseDataPoint = (*WeekProfile)(nil)
	_ datapoint.BaseDataPoint = (*LevelCombined)(nil)
)

// TestHSColorSatisfiesBaseDataPoint exercises the promoted
// UniqueID / Visible / EnabledByDefault surface on a constructed
// [HSColor] when treated as a [datapoint.BaseDataPoint] interface
// value.
//
// HSColor defaults to NoCreate (mirrors
// CombinedDataPoint.__init__(visible=False) which gives NO_CREATE
// usage). Visible() and EnabledByDefault() must therefore default to
// false.
func TestHSColorSatisfiesBaseDataPoint(t *testing.T) {
	t.Parallel()
	var iface datapoint.BaseDataPoint = NewHSColorWithCentral(
		"ccu-prod", "VCU0123:3", &stubWriter{},
		hmenum.ParameterHue, hmenum.ParameterSaturation,
	)
	if got, want := iface.UniqueID(), "ccu-prod:VCU0123:3:COMBINED/HSCOLOR"; got != want {
		t.Fatalf("iface.UniqueID() = %q, want %q", got, want)
	}
	// Default-NoCreate: the combined DP is owned by its parent custom DP
	// And must not surface independently — mirrors
	// CombinedDataPoint(visible=False) default (hs_color.py:31).
	if iface.Visible() {
		t.Fatal("iface.Visible() must be false by default (NoCreate by construction)")
	}
	if iface.EnabledByDefault() {
		t.Fatal("iface.EnabledByDefault() must be false by default (NoCreate by construction)")
	}
}

// TestHSColorUniqueID pins the canonical
// "<central>:<channelAddress>:COMBINED/HSCOLOR" UniqueID format and
// verifies multi-CCU scoping (ADR 0002) — two centrals with the same
// channel address must produce different identifiers.
func TestHSColorUniqueID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		centralName string
		address     string
		want        string
	}{
		{
			name:        "production multi-CCU dp",
			centralName: "ccu-prod",
			address:     "VCU0123:3",
			want:        "ccu-prod:VCU0123:3:COMBINED/HSCOLOR",
		},
		{
			name:        "second central, same address",
			centralName: "ccu-secondary",
			address:     "VCU0123:3",
			want:        "ccu-secondary:VCU0123:3:COMBINED/HSCOLOR",
		},
		{
			name:        "legacy fixture (no central)",
			centralName: "",
			address:     "A:1",
			want:        ":A:1:COMBINED/HSCOLOR",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := NewHSColorWithCentral(
				tc.centralName, tc.address, &stubWriter{},
				hmenum.ParameterHue, hmenum.ParameterSaturation,
			)
			if got := c.UniqueID(); got != tc.want {
				t.Fatalf("UniqueID() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHSColorLegacyConstructorDoesNotPanic verifies that the backward-
// compatible [NewHSColor] (no central) still produces a usable DP — its
// UniqueID lacks the central segment but the canonical
// COMBINED/HSCOLOR suffix is preserved so north-bound adapters can
// still dispatch by family.
func TestHSColorLegacyConstructorDoesNotPanic(t *testing.T) {
	t.Parallel()
	c := NewHSColor("VCU9:3", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	id := c.UniqueID()
	if !strings.HasSuffix(id, ":COMBINED/HSCOLOR") {
		t.Fatalf("legacy UniqueID() = %q, must end with COMBINED/HSCOLOR", id)
	}
}

// TestCombinedWeekProfileUniqueID pins the canonical
// "<central>:<channelAddress>:COMBINED/WEEKPROFILE" UniqueID format
// for [NewCombinedWeekProfile] and verifies multi-CCU scoping.
func TestCombinedWeekProfileUniqueID(t *testing.T) {
	t.Parallel()
	saver := &stubSaver{}
	prof := weekprofile.NewClimate(nil, saver)
	wp := NewCombinedWeekProfile(
		"ccu-prod", "0001ABCD:1", &fakeWriter{}, prof, "WEEK_PROFILE",
	)
	defer wp.Close()
	if got, want := wp.UniqueID(), "ccu-prod:0001ABCD:1:COMBINED/WEEKPROFILE"; got != want {
		t.Fatalf("UniqueID() = %q, want %q", got, want)
	}
	// Legacy NewWeekProfile must still produce the family suffix
	// even without a central segment.
	legacy := NewWeekProfile("0001ABCD:1", &fakeWriter{}, nil, "WEEK_PROFILE")
	if !strings.HasSuffix(legacy.UniqueID(), ":COMBINED/WEEKPROFILE") {
		t.Fatalf("legacy UniqueID() = %q must end with COMBINED/WEEKPROFILE", legacy.UniqueID())
	}
}

// TestCombinedWeekProfileSatisfiesBaseDataPoint exercises the
// promotion at runtime, not just at compile time. After PR-30 the
// constructor force-marks NoCreate so Visible / EnabledByDefault
// Default to false (mirrors
// CombinedDataPoint(visible=False) default — week profiles are
// internal-only DPs unless explicitly opted in).
func TestCombinedWeekProfileSatisfiesBaseDataPoint(t *testing.T) {
	t.Parallel()
	saver := &stubSaver{}
	prof := weekprofile.NewClimate(nil, saver)
	var iface datapoint.BaseDataPoint = NewCombinedWeekProfile(
		"ccu-prod", "0001ABCD:1", &fakeWriter{}, prof, "WEEK_PROFILE",
	)
	if iface.UniqueID() == "" {
		t.Fatal("iface.UniqueID() must not be empty")
	}
	if iface.Visible() {
		t.Fatal("iface.Visible() must default to false (NoCreate by construction)")
	}
	if iface.EnabledByDefault() {
		t.Fatal("iface.EnabledByDefault() must default to false")
	}
}

// TestCombinedSetForcedUsage verifies that the promoted
// [datapoint.BaseDataPointFields.SetForcedUsage] flips Visible() and
// EnabledByDefault() on combined DPs — both [HSColor] and [WeekProfile]
// share the foundation behaviour because neither shadows Visible.
func TestCombinedSetForcedUsage(t *testing.T) {
	t.Parallel()

	t.Run("HSColor defaults NoCreate, opt-in flips Visible", func(t *testing.T) {
		t.Parallel()
		// HSColor now defaults to NoCreate (mirrors
		// Py:31).
		c := NewHSColorWithCentral(
			"ccu-prod", "VCU0123:3", &stubWriter{},
			hmenum.ParameterHue, hmenum.ParameterSaturation,
		)
		if c.Visible() {
			t.Fatal("HSColor must default to Visible() = false (NoCreate by construction)")
		}
		// Explicit opt-in to DataPoint usage makes it visible.
		c.SetForcedUsage(hmenum.DataPointUsageDataPoint)
		if !c.Visible() {
			t.Fatal("after SetForcedUsage(DataPoint), Visible() must be true")
		}
		// Flip back to NoCreate hides it.
		c.SetForcedUsage(hmenum.DataPointUsageNoCreate)
		if c.Visible() {
			t.Fatal("after SetForcedUsage(NoCreate), Visible() must be false")
		}
		if c.EnabledByDefault() {
			t.Fatal("after SetForcedUsage(NoCreate), EnabledByDefault() must be false")
		}
	})

	t.Run("WeekProfile defaults NoCreate, opt-in flips Visible", func(t *testing.T) {
		t.Parallel()
		wp := NewCombinedWeekProfile(
			"ccu-prod", "0001ABCD:1", &fakeWriter{}, nil, "WEEK_PROFILE",
		)
		// After PR-30 WeekProfile defaults to NoCreate (mirrors
		if wp.Visible() {
			t.Fatal("WeekProfile must default to Visible() = false (NoCreate)")
		}
		wp.SetForcedUsage(hmenum.DataPointUsageDataPoint)
		if !wp.Visible() {
			t.Fatal("after SetForcedUsage(DataPoint), Visible() must be true")
		}
		if !wp.EnabledByDefault() {
			t.Fatal("after SetForcedUsage(DataPoint), EnabledByDefault() must be true")
		}
		wp.SetForcedUsage(hmenum.DataPointUsageNoCreate)
		if wp.Visible() {
			t.Fatal("after SetForcedUsage(NoCreate), Visible() must be false again")
		}
	})
}

// TestCombinedPublishUpdateViaPromotion exercises the promoted
// [datapoint.BaseDataPointFields.PublishUpdate] surface — a north-bound
// MQTT bridge or REST broadcaster can register a publisher against a
// combined DP without poking at internals. The forwarded key must
// match the DP's UniqueID so subscribers can dispatch by identity.
func TestCombinedPublishUpdateViaPromotion(t *testing.T) {
	t.Parallel()

	c := NewHSColorWithCentral(
		"ccu-prod", "VCU0123:3", &stubWriter{},
		hmenum.ParameterHue, hmenum.ParameterSaturation,
	)
	pub := &basedpCapturingPublisher{}
	c.SetPublisher(pub)
	c.PublishUpdate(context.Background(), HS{Hue: 120, Saturation: 50})

	calls := pub.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 publisher call, got %d", len(calls))
	}
	if got, want := calls[0].key, "ccu-prod:VCU0123:3:COMBINED/HSCOLOR"; got != want {
		t.Fatalf("publisher key=%q want %q", got, want)
	}
}

// TestCombinedDataPointEmbedsBaseDataPointFields verifies that combined
// DPs expose the promoted Central / Address / KeyName accessors of the
// embedded [datapoint.BaseDataPointFields].
func TestCombinedDataPointEmbedsBaseDataPointFields(t *testing.T) {
	t.Parallel()

	c := NewHSColorWithCentral(
		"ccu-prod", "VCU0123:3", &stubWriter{},
		hmenum.ParameterHue, hmenum.ParameterSaturation,
	)
	if got, want := c.Central(), "ccu-prod"; got != want {
		t.Fatalf("HSColor.Central()=%q want %q", got, want)
	}
	if got, want := c.BaseDataPointFields.Address(), "VCU0123:3"; got != want {
		t.Fatalf("HSColor.Address()=%q want %q", got, want)
	}
	if got, want := c.KeyName(), "COMBINED/HSCOLOR"; got != want {
		t.Fatalf("HSColor.KeyName()=%q want %q", got, want)
	}

	wp := NewCombinedWeekProfile(
		"ccu-prod", "0001ABCD:1", &fakeWriter{}, nil, "WEEK_PROFILE",
	)
	if got, want := wp.Central(), "ccu-prod"; got != want {
		t.Fatalf("WeekProfile.Central()=%q want %q", got, want)
	}
	if got, want := wp.BaseDataPointFields.Address(), "0001ABCD:1"; got != want {
		t.Fatalf("WeekProfile.Address()=%q want %q", got, want)
	}
	if got, want := wp.KeyName(), "COMBINED/WEEKPROFILE"; got != want {
		t.Fatalf("WeekProfile.KeyName()=%q want %q", got, want)
	}
}

// TestCombinedConcurrentAfterMigration race-tests the migrated
// combined DPs: the embedded BaseDataPointFields' lock and the DP's own
// `mu` are independent locks, so concurrent forced-usage churn,
// publisher updates, and value ingestion must not race or deadlock.
func TestCombinedConcurrentAfterMigration(t *testing.T) {
	t.Parallel()

	c := NewHSColorWithCentral(
		"ccu-prod", "VCU0123:3", &stubWriter{},
		hmenum.ParameterHue, hmenum.ParameterSaturation,
	)
	pub := &basedpCountingPublisher{}
	c.SetPublisher(pub)

	saver := &stubSaver{}
	prof := weekprofile.NewClimate(nil, saver)
	wp := NewCombinedWeekProfile(
		"ccu-prod", "0001ABCD:1", &fakeWriter{}, prof, "WEEK_PROFILE",
	)
	defer wp.Close()

	const (
		writers    = 4
		readers    = 4
		iterations = 100
	)

	usages := []hmenum.DataPointUsage{
		hmenum.DataPointUsageCDPPrimary,
		hmenum.DataPointUsageCDPVisible,
		hmenum.DataPointUsageDataPoint,
		hmenum.DataPointUsageEvent,
		hmenum.DataPointUsageNoCreate,
	}

	var wg sync.WaitGroup

	for w := 0; w < writers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for i := 0; i < iterations; i++ {
				c.SetForcedUsage(usages[(w+i)%len(usages)])
				wp.SetForcedUsage(usages[(w+i)%len(usages)])
				c.PublishUpdate(ctx, i)
				wp.PublishUpdate(ctx, i)
				c.OnHue(int32(i % 360))
				c.OnSaturation(float64(i%100) / 100.0)
				_ = wp.Set(ctx, schedule.NewClimate(), hmenum.CommandPriorityHigh)
			}
		}()
	}

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = c.UniqueID()
				_ = c.Visible()
				_ = c.EnabledByDefault()
				_, _ = c.ForcedUsage()
				_, _ = c.Value()
				_ = wp.UniqueID()
				_ = wp.Visible()
				_, _ = wp.Value()
			}
		}()
	}

	wg.Wait()

	// Sanity: at least the combined-DP publisher saw every writer's
	// PublishUpdate (each writer publishes twice per iteration: once
	// to c, once to wp).
	if got, want := pub.count(), int64(writers*iterations); got != want {
		t.Fatalf("HSColor publisher saw %d calls, want %d", got, want)
	}
}

// TestLevelCombinedSatisfiesBaseDataPoint pins the V4 fix from PR-32:
// [LevelCombined] embeds [datapoint.BaseDataPointFields] and the
// constructor force-marks NoCreate so the combined DP does not
// surface as a top-level entity (it is consumed internally by
// [cover.Cover]).
func TestLevelCombinedSatisfiesBaseDataPoint(t *testing.T) {
	t.Parallel()
	var iface datapoint.BaseDataPoint = NewLevelCombinedWithCentral(
		"ccu-prod", "VCU0123:4", &stubWriter{},
		hmenum.ParameterLevel, hmenum.ParameterLevel2, hmenum.ParameterLevelCombined,
	)
	if got, want := iface.UniqueID(), "ccu-prod:VCU0123:4:COMBINED/LEVEL_COMBINED"; got != want {
		t.Fatalf("iface.UniqueID() = %q, want %q", got, want)
	}
	if iface.Visible() {
		t.Fatal("iface.Visible() must default to false (NoCreate by construction)")
	}
	if iface.EnabledByDefault() {
		t.Fatal("iface.EnabledByDefault() must default to false")
	}
}

// TestLevelCombinedLegacyConstructor verifies the backward-compatible
// [NewLevelCombined] (no central) still produces a usable DP whose
// UniqueID ends with the canonical family suffix.
func TestLevelCombinedLegacyConstructor(t *testing.T) {
	t.Parallel()
	lc := NewLevelCombined("VCU9:4", &stubWriter{},
		hmenum.ParameterLevel, hmenum.ParameterLevel2, hmenum.ParameterLevelCombined)
	if !strings.HasSuffix(lc.UniqueID(), ":COMBINED/LEVEL_COMBINED") {
		t.Fatalf("legacy UniqueID() = %q must end with COMBINED/LEVEL_COMBINED", lc.UniqueID())
	}
}

// basedpCapturingPublisher captures every PublishUpdate invocation
// for the combined-DP promotion test. Suffix-named to avoid colliding
// with similarly named publishers in sibling tests.
type basedpCapturingPublisher struct {
	mu    sync.Mutex
	calls []basedpCapturedCall
}

type basedpCapturedCall struct {
	key   string
	value any
}

func (c *basedpCapturingPublisher) PublishUpdate(_ context.Context, key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, basedpCapturedCall{key: key, value: value})
}

func (c *basedpCapturingPublisher) snapshot() []basedpCapturedCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]basedpCapturedCall, len(c.calls))
	copy(out, c.calls)
	return out
}

// basedpCountingPublisher is the lightweight counting variant for the
// race test.
type basedpCountingPublisher struct {
	n atomic.Int64
}

func (c *basedpCountingPublisher) PublishUpdate(_ context.Context, _ string, _ any) {
	c.n.Add(1)
}

func (c *basedpCountingPublisher) count() int64 { return c.n.Load() }
