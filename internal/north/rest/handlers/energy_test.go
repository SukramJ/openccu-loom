// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// stubEnergyService is an inline stub for EnergyService. It records the
// last query it received so tests can assert parameter parsing.
type stubEnergyService struct {
	resp EnergyResponse
	err  error
	gotQ EnergyQuery
}

func (s *stubEnergyService) Energy(_ context.Context, q EnergyQuery) (EnergyResponse, error) {
	s.gotQ = q
	return s.resp, s.err
}

// validEnergyURL builds a request URL with every required parameter set.
func validEnergyURL() string {
	return "/api/v1/energy?central=Home&from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z"
}

func TestGetEnergy_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubEnergyService{resp: EnergyResponse{
		Group: "day",
		Devices: []EnergyDevice{
			{Address: "DEV1", Name: "Bücherregal", TotalConsumedWh: 100},
		},
		TotalConsumedWh: 100,
	}}
	req := httptest.NewRequest(http.MethodGet, validEnergyURL()+"&group=hour&device=DEV1", http.NoBody)
	w := httptest.NewRecorder()
	GetEnergy(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body EnergyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Devices) != 1 || body.TotalConsumedWh != 100 {
		t.Fatalf("unexpected body: %+v", body)
	}
	if svc.gotQ.Central != "Home" || svc.gotQ.Device != "DEV1" || svc.gotQ.Group != "hour" {
		t.Fatalf("query not parsed as expected: %+v", svc.gotQ)
	}
}

func TestGetEnergy_DefaultGroupIsDay(t *testing.T) {
	t.Parallel()
	svc := &stubEnergyService{}
	req := httptest.NewRequest(http.MethodGet, validEnergyURL(), http.NoBody)
	GetEnergy(svc).ServeHTTP(httptest.NewRecorder(), req)
	if svc.gotQ.Group != "day" {
		t.Fatalf("expected default group day, got %q", svc.gotQ.Group)
	}
}

func TestGetEnergy_EmptyDevicesIsJSONArray(t *testing.T) {
	t.Parallel()
	svc := &stubEnergyService{resp: EnergyResponse{Group: "day"}}
	req := httptest.NewRequest(http.MethodGet, validEnergyURL(), http.NoBody)
	w := httptest.NewRecorder()
	GetEnergy(svc).ServeHTTP(w, req)
	var body EnergyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Devices == nil {
		t.Fatalf("expected non-nil empty devices slice in decoded body")
	}
}

func TestGetEnergy_NilServiceIsUnavailable(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, validEnergyURL(), http.NoBody)
	w := httptest.NewRecorder()
	GetEnergy(nil).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGetEnergy_ServiceErrorIs500(t *testing.T) {
	t.Parallel()
	svc := &stubEnergyService{err: errors.New("boom")}
	req := httptest.NewRequest(http.MethodGet, validEnergyURL(), http.NoBody)
	w := httptest.NewRecorder()
	GetEnergy(svc).ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestGetEnergy_MissingRequiredParams(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"no central": "/api/v1/energy?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z",
		"no from":    "/api/v1/energy?central=Home&to=2026-06-02T00:00:00Z",
		"no to":      "/api/v1/energy?central=Home&from=2026-06-01T00:00:00Z",
	}
	for name, url := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := &stubEnergyService{}
			req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
			w := httptest.NewRecorder()
			GetEnergy(svc).ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: expected 400, got %d", name, w.Code)
			}
		})
	}
}

func TestGetEnergy_BadTimestampsAndRange(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"bad from":       "/api/v1/energy?central=Home&from=not-a-time&to=2026-06-02T00:00:00Z",
		"bad to":         "/api/v1/energy?central=Home&from=2026-06-01T00:00:00Z&to=not-a-time",
		"to equal from":  "/api/v1/energy?central=Home&from=2026-06-01T00:00:00Z&to=2026-06-01T00:00:00Z",
		"to before from": "/api/v1/energy?central=Home&from=2026-06-02T00:00:00Z&to=2026-06-01T00:00:00Z",
	}
	for name, url := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := &stubEnergyService{}
			req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
			w := httptest.NewRecorder()
			GetEnergy(svc).ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: expected 400, got %d", name, w.Code)
			}
		})
	}
}

func TestGetEnergy_BadGroup(t *testing.T) {
	t.Parallel()
	svc := &stubEnergyService{}
	req := httptest.NewRequest(http.MethodGet, validEnergyURL()+"&group=week", http.NoBody)
	w := httptest.NewRecorder()
	GetEnergy(svc).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetEnergy_ValidGroups(t *testing.T) {
	t.Parallel()
	for _, g := range []string{"hour", "day", "month"} {
		t.Run(g, func(t *testing.T) {
			t.Parallel()
			svc := &stubEnergyService{}
			req := httptest.NewRequest(http.MethodGet, validEnergyURL()+"&group="+g, http.NoBody)
			GetEnergy(svc).ServeHTTP(httptest.NewRecorder(), req)
			if svc.gotQ.Group != g {
				t.Fatalf("expected group %q, got %q", g, svc.gotQ.Group)
			}
		})
	}
}

// ── FoldEnergyRows ───────────────────────────────────────────────────────

func bucketTS(t time.Time) time.Time { return t.UTC() }

func TestFoldEnergyRows_PowerAvgAndPeak(t *testing.T) {
	t.Parallel()
	ts := bucketTS(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	rows := []EnergyRawRow{
		{ChannelAddress: "DEV1:4", Parameter: "POWER", BucketTS: ts, Sum: 300, Count: 3, Max: 240},
	}
	resp := FoldEnergyRows(EnergyQuery{Group: "day"}, rows, nil)
	if len(resp.Devices) != 1 {
		t.Fatalf("devices = %d, want 1", len(resp.Devices))
	}
	d := resp.Devices[0]
	if d.Address != "DEV1" {
		t.Errorf("address = %q, want DEV1", d.Address)
	}
	if len(d.Buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(d.Buckets))
	}
	b := d.Buckets[0]
	if b.AvgPowerW != 100 {
		t.Errorf("avg_power_w = %v, want 100 (300/3)", b.AvgPowerW)
	}
	if b.PeakPowerW != 240 {
		t.Errorf("peak_power_w = %v, want 240", b.PeakPowerW)
	}
}

func TestFoldEnergyRows_EnergyCounterDelta(t *testing.T) {
	t.Parallel()
	ts := bucketTS(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	rows := []EnergyRawRow{
		{ChannelAddress: "DEV1:4", Parameter: "ENERGY_COUNTER", BucketTS: ts, First: 1000, Last: 1412},
	}
	resp := FoldEnergyRows(EnergyQuery{Group: "day"}, rows, nil)
	b := resp.Devices[0].Buckets[0]
	if b.ConsumedWh != 412 {
		t.Errorf("consumed_wh = %v, want 412", b.ConsumedWh)
	}
	if b.Reset {
		t.Errorf("reset = true, want false for a normal increasing counter")
	}
	if resp.Devices[0].TotalConsumedWh != 412 || resp.TotalConsumedWh != 412 {
		t.Errorf("totals not propagated: device=%v response=%v",
			resp.Devices[0].TotalConsumedWh, resp.TotalConsumedWh)
	}
}

// TestFoldEnergyRows_CounterResetNeverNegative is the correctness-critical
// case: a meter that reset within the bucket (last < first) must report
// consumed_wh = last (energy since reset) and reset = true — never a
// negative delta.
func TestFoldEnergyRows_CounterResetNeverNegative(t *testing.T) {
	t.Parallel()
	ts := bucketTS(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	rows := []EnergyRawRow{
		{ChannelAddress: "DEV1:4", Parameter: "ENERGY_COUNTER", BucketTS: ts, First: 5000, Last: 120},
	}
	resp := FoldEnergyRows(EnergyQuery{Group: "day"}, rows, nil)
	b := resp.Devices[0].Buckets[0]
	if b.ConsumedWh != 120 {
		t.Errorf("consumed_wh = %v, want 120 (last, energy since reset)", b.ConsumedWh)
	}
	if b.ConsumedWh < 0 {
		t.Fatalf("consumed_wh must never be negative, got %v", b.ConsumedWh)
	}
	if !b.Reset {
		t.Errorf("reset = false, want true when last < first")
	}
}

func TestFoldEnergyRows_FeedInSameDeltaLogic(t *testing.T) {
	t.Parallel()
	ts := bucketTS(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	rows := []EnergyRawRow{
		{ChannelAddress: "DEV1:4", Parameter: "ENERGY_COUNTER_FEED_IN", BucketTS: ts, First: 200, Last: 50},
	}
	resp := FoldEnergyRows(EnergyQuery{Group: "day"}, rows, nil)
	b := resp.Devices[0].Buckets[0]
	if b.FeedInWh != 50 || !b.Reset {
		t.Errorf("feed_in_wh/reset = %v/%v, want 50/true", b.FeedInWh, b.Reset)
	}
}

func TestFoldEnergyRows_MultiDeviceTotalsAndNames(t *testing.T) {
	t.Parallel()
	ts := bucketTS(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	rows := []EnergyRawRow{
		{ChannelAddress: "DEV1:4", Parameter: "ENERGY_COUNTER", BucketTS: ts, First: 0, Last: 100},
		{ChannelAddress: "DEV2:4", Parameter: "ENERGY_COUNTER", BucketTS: ts, First: 0, Last: 250},
		{ChannelAddress: "DEV2:4", Parameter: "ENERGY_COUNTER_FEED_IN", BucketTS: ts, First: 0, Last: 30},
	}
	names := map[string]string{"DEV1": "Bücherregal", "DEV2": "Keller"}
	resp := FoldEnergyRows(EnergyQuery{Group: "day"}, rows, func(addr string) (string, bool) {
		n, ok := names[addr]
		return n, ok
	})
	if len(resp.Devices) != 2 {
		t.Fatalf("devices = %d, want 2", len(resp.Devices))
	}
	// Sorted by address: DEV1 then DEV2.
	if resp.Devices[0].Address != "DEV1" || resp.Devices[0].Name != "Bücherregal" {
		t.Errorf("device[0] = %+v", resp.Devices[0])
	}
	if resp.Devices[1].Address != "DEV2" || resp.Devices[1].Name != "Keller" {
		t.Errorf("device[1] = %+v", resp.Devices[1])
	}
	if resp.Devices[1].TotalConsumedWh != 250 || resp.Devices[1].TotalFeedInWh != 30 {
		t.Errorf("DEV2 totals = consumed=%v feed_in=%v, want 250/30",
			resp.Devices[1].TotalConsumedWh, resp.Devices[1].TotalFeedInWh)
	}
	if resp.TotalConsumedWh != 350 || resp.TotalFeedInWh != 30 {
		t.Errorf("response totals = consumed=%v feed_in=%v, want 350/30",
			resp.TotalConsumedWh, resp.TotalFeedInWh)
	}
}

func TestFoldEnergyRows_UnknownDeviceFallsBackToAddress(t *testing.T) {
	t.Parallel()
	ts := bucketTS(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	rows := []EnergyRawRow{
		{ChannelAddress: "DEV1:4", Parameter: "POWER", BucketTS: ts, Sum: 10, Count: 1, Max: 10},
	}
	resp := FoldEnergyRows(EnergyQuery{Group: "day"}, rows, func(string) (string, bool) { return "", false })
	if resp.Devices[0].Name != "DEV1" {
		t.Errorf("name = %q, want fallback to address DEV1", resp.Devices[0].Name)
	}
}

func TestFoldEnergyRows_BucketsSortedChronologically(t *testing.T) {
	t.Parallel()
	t1 := bucketTS(time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC))
	t2 := bucketTS(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	rows := []EnergyRawRow{
		{ChannelAddress: "DEV1:4", Parameter: "POWER", BucketTS: t1, Sum: 10, Count: 1, Max: 10},
		{ChannelAddress: "DEV1:4", Parameter: "POWER", BucketTS: t2, Sum: 20, Count: 1, Max: 20},
	}
	resp := FoldEnergyRows(EnergyQuery{Group: "day"}, rows, nil)
	bs := resp.Devices[0].Buckets
	if len(bs) != 2 || !bs[0].TS.Before(bs[1].TS) {
		t.Fatalf("buckets not chronologically sorted: %+v", bs)
	}
}

func TestFoldEnergyRows_MonthGroupPassesThroughToResponse(t *testing.T) {
	t.Parallel()
	resp := FoldEnergyRows(EnergyQuery{Group: "month"}, nil, nil)
	if resp.Group != "month" {
		t.Errorf("group = %q, want month", resp.Group)
	}
	if len(resp.Devices) != 0 {
		t.Errorf("expected no devices for empty rows, got %+v", resp.Devices)
	}
}

// TestFoldEnergyRows_RangeTotalIncludesInterBucketConsumption is the
// correctness guard for finding #4: a device total must be the range total
// of the cumulative counter (last of the last bucket minus first of the
// first bucket), NOT a sum of per-bucket last-first deltas — the latter
// drops everything consumed between one bucket's last reading and the next
// bucket's first.
func TestFoldEnergyRows_RangeTotalIncludesInterBucketConsumption(t *testing.T) {
	t.Parallel()
	t1 := bucketTS(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	t2 := bucketTS(time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC))
	// Bucket 1: 100 -> 150 (per-bucket delta 50).
	// Bucket 2: 210 -> 260 (per-bucket delta 50).
	// The 60 Wh consumed between 150 (b1.last) and 210 (b2.first) is what a
	// naive Σ per-bucket delta (=100) drops. Range total = 260 - 100 = 160.
	rows := []EnergyRawRow{
		{ChannelAddress: "DEV1:4", Parameter: "ENERGY_COUNTER", BucketTS: t2, First: 210, Last: 260},
		{ChannelAddress: "DEV1:4", Parameter: "ENERGY_COUNTER", BucketTS: t1, First: 100, Last: 150},
	}
	resp := FoldEnergyRows(EnergyQuery{Group: "day"}, rows, nil)
	if len(resp.Devices) != 1 {
		t.Fatalf("devices = %d, want 1", len(resp.Devices))
	}
	if got := resp.Devices[0].TotalConsumedWh; got != 160 {
		t.Errorf("device total = %v, want 160 (range total, incl. inter-bucket 60)", got)
	}
	if resp.TotalConsumedWh != 160 {
		t.Errorf("response total = %v, want 160", resp.TotalConsumedWh)
	}
	// Per-bucket breakdown still reports the per-bucket deltas for the chart.
	var perBucketSum float64
	for _, b := range resp.Devices[0].Buckets {
		perBucketSum += b.ConsumedWh
	}
	if perBucketSum != 100 {
		t.Errorf("per-bucket ConsumedWh sum = %v, want 100 (chart deltas unchanged)", perBucketSum)
	}
}

// TestFoldEnergyRows_RangeTotalSegmentsOnReset verifies the reset-aware
// segmentation across buckets: a decrease between readings is treated as a
// meter reset (consumption since the reset = the post-reset value), never a
// negative contribution.
func TestFoldEnergyRows_RangeTotalSegmentsOnReset(t *testing.T) {
	t.Parallel()
	t1 := bucketTS(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	t2 := bucketTS(time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC))
	// Readings flatten to 100,150, 20,60. Segments: 100->150 = 50,
	// 150->20 (reset) = 20, 20->60 = 40. Total = 110.
	rows := []EnergyRawRow{
		{ChannelAddress: "DEV1:4", Parameter: "ENERGY_COUNTER", BucketTS: t1, First: 100, Last: 150},
		{ChannelAddress: "DEV1:4", Parameter: "ENERGY_COUNTER", BucketTS: t2, First: 20, Last: 60},
	}
	resp := FoldEnergyRows(EnergyQuery{Group: "day"}, rows, nil)
	if got := resp.Devices[0].TotalConsumedWh; got != 110 {
		t.Errorf("device total = %v, want 110 (reset-segmented range total)", got)
	}
	if got := resp.TotalConsumedWh; got < 0 {
		t.Fatalf("response total must never be negative, got %v", got)
	}
}

// TestFoldEnergyRows_RangeTotalPerChannelIndependent verifies that two
// channels on one device are totalled as independent counters (each
// segmented on its own), then summed — never concatenated into one series
// (which would fabricate a reset between the two channels).
func TestFoldEnergyRows_RangeTotalPerChannelIndependent(t *testing.T) {
	t.Parallel()
	ts := bucketTS(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	next := bucketTS(time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC))
	rows := []EnergyRawRow{
		// Channel :4 over two buckets: 0->100, 100->300 → range 300.
		{ChannelAddress: "DEV1:4", Parameter: "ENERGY_COUNTER", BucketTS: ts, First: 0, Last: 100},
		{ChannelAddress: "DEV1:4", Parameter: "ENERGY_COUNTER", BucketTS: next, First: 100, Last: 300},
		// Channel :5 single bucket: 1000->1050 → range 50. If the two
		// channels were concatenated, 300 vs 1000 would look like a jump.
		{ChannelAddress: "DEV1:5", Parameter: "ENERGY_COUNTER", BucketTS: ts, First: 1000, Last: 1050},
	}
	resp := FoldEnergyRows(EnergyQuery{Group: "day"}, rows, nil)
	if got := resp.Devices[0].TotalConsumedWh; got != 350 {
		t.Errorf("device total = %v, want 350 (300 + 50, per-channel)", got)
	}
}
