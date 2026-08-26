// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Compile-time guard: *ProfileDataPoint satisfies the
// [datapoint.BaseDataPoint] contract via the embedded
// [datapoint.BaseDataPointFields].
var _ datapoint.BaseDataPoint = (*ProfileDataPoint)(nil)

// TestProfileDataPointEmbedsBaseDataPointFields verifies that the
// promoted Central / Address / KeyName accessors match the values the
// constructor took. Multi-CCU correctness (ADR 0002) hinges on the
// central segment being present so two CCUs cannot collide on the same
// channel address.
func TestProfileDataPointEmbedsBaseDataPointFields(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		CentralName:    "ccu-prod",
		ChannelAddress: "VCU0123:1",
		ScheduleType:   ScheduleTypeClimate,
		ProfileCount:   3,
	})
	if got, want := dp.Central(), "ccu-prod"; got != want {
		t.Fatalf("Central()=%q want %q", got, want)
	}
	// Address() is promoted from the embedded BaseDataPointFields.
	// *ProfileDataPoint does not declare a competing Address field, so
	// the promoted accessor is unambiguous.
	if got, want := dp.Address(), "VCU0123:1"; got != want {
		t.Fatalf("Address()=%q want %q", got, want)
	}
	if got, want := dp.KeyName(), "WEEKPROFILE"; got != want {
		t.Fatalf("KeyName()=%q want %q", got, want)
	}
}

// TestProfileDataPointUniqueID pins the canonical
// "<central>:<channelAddress>:WEEKPROFILE" UniqueID format and
// verifies multi-CCU scoping.
func TestProfileDataPointUniqueID(t *testing.T) {
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
			address:     "VCU0123:1",
			want:        "ccu-prod:VCU0123:1:WEEKPROFILE",
		},
		{
			name:        "second central, same address",
			centralName: "ccu-secondary",
			address:     "VCU0123:1",
			want:        "ccu-secondary:VCU0123:1:WEEKPROFILE",
		},
		{
			name:        "legacy fixture (no identity)",
			centralName: "",
			address:     "",
			want:        "::WEEKPROFILE",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dp := NewProfileDataPoint(ProfileDataPointConfig{
				CentralName:    tc.centralName,
				ChannelAddress: tc.address,
				ScheduleType:   ScheduleTypeClimate,
				ProfileCount:   3,
			})
			if got := dp.UniqueID(); got != tc.want {
				t.Fatalf("UniqueID() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestProfileDataPointSatisfiesBaseDataPoint exercises the promotion at
// runtime: when treated as a [datapoint.BaseDataPoint], the
// ProfileDataPoint exposes a non-empty UniqueID. After PR-30 the
// constructor force-marks NoCreate so Visible / EnabledByDefault
// Default to false (mirrors
// CombinedDataPoint(visible=False) default — week profiles are
// internal-only DPs unless explicitly opted in).
func TestProfileDataPointSatisfiesBaseDataPoint(t *testing.T) {
	t.Parallel()
	var iface datapoint.BaseDataPoint = NewProfileDataPoint(ProfileDataPointConfig{
		CentralName:    "ccu-prod",
		ChannelAddress: "VCU0123:1",
		ScheduleType:   ScheduleTypeDefault,
	})
	if iface.UniqueID() == "" {
		t.Fatal("iface.UniqueID() must not be empty")
	}
	if !strings.HasSuffix(iface.UniqueID(), ":WEEKPROFILE") {
		t.Fatalf("iface.UniqueID() = %q must end with :WEEKPROFILE", iface.UniqueID())
	}
	if iface.Visible() {
		t.Fatal("iface.Visible() must default to false (NoCreate by construction)")
	}
	if iface.EnabledByDefault() {
		t.Fatal("iface.EnabledByDefault() must default to false")
	}
}

// TestProfileDataPointSetForcedUsage verifies that the promoted
// SetForcedUsage flips Visible and EnabledByDefault. After PR-30 the
// constructor defaults to NoCreate; opting in to a visible usage
// must promote the DP back to Visible/EnabledByDefault=true.
func TestProfileDataPointSetForcedUsage(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		CentralName:    "ccu-prod",
		ChannelAddress: "VCU0123:1",
		ScheduleType:   ScheduleTypeClimate,
		ProfileCount:   3,
	})
	if dp.Visible() {
		t.Fatal("Visible() must default to false (NoCreate by construction)")
	}
	dp.SetForcedUsage(hmenum.DataPointUsageDataPoint)
	if !dp.Visible() {
		t.Fatal("after SetForcedUsage(DataPoint), Visible() must be true")
	}
	if !dp.EnabledByDefault() {
		t.Fatal("after SetForcedUsage(DataPoint), EnabledByDefault() must be true")
	}
	dp.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	if dp.Visible() {
		t.Fatal("after SetForcedUsage(NoCreate), Visible() must be false again")
	}
}

// TestProfileDataPointPublishUpdateUsesProfileKey verifies that the
// promoted [datapoint.BaseDataPointFields.PublishUpdate] forwards the
// profile DP's UniqueID — north-bound subscribers can dispatch by
// identity without resolving the DP instance.
func TestProfileDataPointPublishUpdateUsesProfileKey(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		CentralName:    "ccu-prod",
		ChannelAddress: "VCU0123:1",
		ScheduleType:   ScheduleTypeClimate,
		ProfileCount:   3,
	})
	pub := &profileCapturingPublisher{}
	dp.SetPublisher(pub)
	dp.PublishUpdate(context.Background(), "P2")

	calls := pub.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 publisher call, got %d", len(calls))
	}
	if got, want := calls[0].key, "ccu-prod:VCU0123:1:WEEKPROFILE"; got != want {
		t.Fatalf("publisher key=%q want %q", got, want)
	}
}

// TestProfileDataPointConcurrentBaseDataPoint races the promoted
// BaseDataPointFields surface against ProfileDataPoint's own mu-
// protected mutators (SetCurrentProfile, SetScheduleEnabled). The two
// locks are independent — the test confirms they can be exercised in
// parallel without races or deadlocks.
func TestProfileDataPointConcurrentBaseDataPoint(t *testing.T) {
	t.Parallel()
	dp := NewProfileDataPoint(ProfileDataPointConfig{
		CentralName:    "ccu-prod",
		ChannelAddress: "VCU0123:1",
		ScheduleType:   ScheduleTypeClimate,
		ProfileCount:   6,
	})
	dp.RegisterChannel("1_1", true)

	pub := &profileCountingPublisher{}
	dp.SetPublisher(pub)

	const (
		writers    = 4
		readers    = 4
		iterations = 100
	)

	usages := []hmenum.DataPointUsage{
		hmenum.DataPointUsageCDPPrimary,
		hmenum.DataPointUsageDataPoint,
		hmenum.DataPointUsageNoCreate,
	}

	var wg sync.WaitGroup

	for w := range writers {
		wg.Go(func() {
			ctx := context.Background()
			for i := range iterations {
				dp.SetForcedUsage(usages[(w+i)%len(usages)])
				dp.PublishUpdate(ctx, i)
				_ = dp.SetCurrentProfile([]string{"P1", "P2", "P3"}[i%3])
				_ = dp.SetScheduleEnabled(ctx, "1_1", i%2 == 0, hmenum.CommandPriorityHigh)
			}
		})
	}

	for range readers {
		wg.Go(func() {
			for range iterations {
				_ = dp.UniqueID()
				_ = dp.Visible()
				_ = dp.EnabledByDefault()
				_, _ = dp.ForcedUsage()
				_ = dp.CurrentProfile()
				_ = dp.ScheduleEnabled()
			}
		})
	}

	wg.Wait()

	if got, want := pub.count(), int64(writers*iterations); got != want {
		t.Fatalf("publisher saw %d calls, want %d", got, want)
	}
}

// profileCapturingPublisher records every PublishUpdate invocation for
// the promotion test.
type profileCapturingPublisher struct {
	mu    sync.Mutex
	calls []profileCapturedCall
}

type profileCapturedCall struct {
	key   string
	value any
}

func (c *profileCapturingPublisher) PublishUpdate(_ context.Context, key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, profileCapturedCall{key: key, value: value})
}

func (c *profileCapturingPublisher) snapshot() []profileCapturedCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]profileCapturedCall, len(c.calls))
	copy(out, c.calls)
	return out
}

// profileCountingPublisher is the lightweight counting variant for the
// race test.
type profileCountingPublisher struct {
	n atomic.Int64
}

func (c *profileCountingPublisher) PublishUpdate(_ context.Context, _ string, _ any) {
	c.n.Add(1)
}

func (c *profileCountingPublisher) count() int64 { return c.n.Load() }
