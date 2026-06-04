// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// Test fixtures (typed schedule I/O)
// ---------------------------------------------------------------------------

// scheduleIOFakeBackend is a minimal stub of [paramsetBackend] /
// [backends.Operations] that records every GetParamset / PutParamset
// call and returns a configurable raw paramset. It embeds
// [paramsetFakeOps] so we don't need to re-implement the full
// backends.Operations surface.
type scheduleIOFakeBackend struct {
	paramsetFakeOps

	mu       sync.Mutex
	getCalls int
	putCalls int

	// putValues is a map keyed by channelAddr → values written via
	// PutParamset. The most recent write wins.
	putValues map[string]map[string]any
}

func (b *scheduleIOFakeBackend) recordPut(addr string, values map[string]any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.putValues == nil {
		b.putValues = make(map[string]map[string]any)
	}
	cp := make(map[string]any, len(values))
	maps.Copy(cp, values)
	b.putValues[addr] = cp
	b.putCalls++
}

func (b *scheduleIOFakeBackend) lastPut(addr string) map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.putValues[addr]
}

func (b *scheduleIOFakeBackend) getCallCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.getCalls
}

func (b *scheduleIOFakeBackend) putCallCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.putCalls
}

// fixtureClimateRawP1Monday returns a minimal MASTER paramset for one
// climate channel with one profile and one weekday (MONDAY) that
// covers 18 °C all day. Used to seed the GetParamset path of the fake.
func fixtureClimateRawP1Monday() map[string]any {
	out := map[string]any{}
	for slot := 1; slot <= 13; slot++ {
		out[fmt.Sprintf("P1_ENDTIME_MONDAY_%d", slot)] = 1440
		out[fmt.Sprintf("P1_TEMPERATURE_MONDAY_%d", slot)] = 18.0
	}
	// Other paramset keys must not break parsing.
	out["GLOBAL_BUTTON_LOCK"] = false
	return out
}

// buildScheduleIOFixture constructs a SchedulesDomain wired to a
// recording backend for one device on a single CCU. The backend's
// getParamsetFn returns the supplied raw paramset; putParamsetFn
// records every write.
func buildScheduleIOFixture(t *testing.T, raw map[string]any) (
	domain *SchedulesDomain,
	backend *scheduleIOFakeBackend,
) {
	t.Helper()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
		Model:       "HmIP-eTRV",
		Name:        "Wohnzimmer",
	})
	c.ModelRegistry.Put(dev)

	backend = &scheduleIOFakeBackend{}
	backend.getParamsetFn = func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
		backend.mu.Lock()
		backend.getCalls++
		backend.mu.Unlock()
		// Hand back a copy so callers can mutate without affecting
		// subsequent reads.
		out := make(map[string]any, len(raw))
		maps.Copy(out, raw)
		return out, nil
	}
	backend.putParamsetFn = func(_ context.Context, address string, _ hmenum.ParamsetKey, values map[string]any) error {
		backend.recordPut(address, values)
		return nil
	}

	w := client.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", backend)

	domain = NewSchedulesDomain(reg, w)
	return domain, backend
}

// ---------------------------------------------------------------------------
// GetSchedule
// ---------------------------------------------------------------------------

func TestGetScheduleReturnsClimateWeekdayBaseTemperature(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	got, err := domain.GetSchedule(t.Context(), "0001ABCD", 1, false)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if got == nil {
		t.Fatal("nil schedule")
	}
	prof, ok := got.Profiles["P1"]
	if !ok {
		t.Fatalf("P1 missing: profiles=%v", got.Profiles)
	}
	day := prof.Days[schedule.WeekdayMonday]
	if day.BaseTemperature != 18.0 {
		t.Errorf("base temp: got %v, want 18.0", day.BaseTemperature)
	}
	if backend.getCallCount() != 1 {
		t.Errorf("backend Get calls: got %d, want 1", backend.getCallCount())
	}
}

func TestGetScheduleHonoursCacheOnSecondCall(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	if _, err := domain.GetSchedule(t.Context(), "0001ABCD", 1, false); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := domain.GetSchedule(t.Context(), "0001ABCD", 1, false); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got := backend.getCallCount(); got != 1 {
		t.Errorf("backend Get calls: got %d, want 1 (second call must hit cache)", got)
	}
}

func TestGetScheduleForceBypassesCache(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	if _, err := domain.GetSchedule(t.Context(), "0001ABCD", 1, false); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := domain.GetSchedule(t.Context(), "0001ABCD", 1, true); err != nil {
		t.Fatalf("force call: %v", err)
	}
	if got := backend.getCallCount(); got != 2 {
		t.Errorf("backend Get calls: got %d, want 2 (force=true must re-fetch)", got)
	}
}

func TestGetScheduleNoBackendReturnsError(t *testing.T) {
	t.Parallel()
	domain := NewSchedulesDomain(nil, client.NewValueWriter())
	_, err := domain.GetSchedule(t.Context(), "0001ABCD", 1, false)
	if !errors.Is(err, ErrNoScheduleBackend) {
		t.Errorf("got %v, want ErrNoScheduleBackend", err)
	}
}

func TestGetScheduleCancelledContextPropagates(t *testing.T) {
	t.Parallel()
	domain, _ := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := domain.GetSchedule(ctx, "0001ABCD", 1, false)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestGetScheduleNoScheduleParamsReturnsErrNoSchedule(t *testing.T) {
	t.Parallel()
	domain, _ := buildScheduleIOFixture(t, map[string]any{
		"GLOBAL_BUTTON_LOCK":    false,
		"TEMPERATUREFALL_MODUS": 2,
	})
	_, err := domain.GetSchedule(t.Context(), "0001ABCD", 1, false)
	if !errors.Is(err, ErrNoSchedule) {
		t.Errorf("got %v, want ErrNoSchedule", err)
	}
}

// ---------------------------------------------------------------------------
// SetSchedule
// ---------------------------------------------------------------------------

// makeClimateP1MondayHeatedAt returns a Climate carrying P1.MONDAY
// with three full-day-coverage periods (base + heated stretch + base).
// The schedule.ClimateWeekday model enforces gap-free 24-h coverage —
// these fixtures use the canonical wire-shape rather than the
// DTO/simple form (where gaps default to base temperature).
func makeClimateP1MondayHeatedAt(base float64) *schedule.Climate {
	c := schedule.NewClimate()
	prof := schedule.NewClimateProfile()
	prof.Days[schedule.WeekdayMonday] = schedule.ClimateWeekday{
		BaseTemperature: base,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "00:00", EndTime: "06:00", Temperature: base},
			{StartTime: "06:00", EndTime: "08:00", Temperature: 21.0},
			{StartTime: "08:00", EndTime: "24:00", Temperature: base},
		},
	}
	c.Profiles["P1"] = prof
	return c
}

func TestSetScheduleWritesMasterParamset(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	in := makeClimateP1MondayHeatedAt(18.0)
	if err := domain.SetSchedule(t.Context(), "0001ABCD", 1, in); err != nil {
		t.Fatalf("SetSchedule: %v", err)
	}
	if backend.putCallCount() != 1 {
		t.Errorf("Put calls: got %d, want 1", backend.putCallCount())
	}
	written := backend.lastPut("0001ABCD:1")
	// Must contain at least one P1 ENDTIME and one TEMPERATURE key.
	if _, ok := written["P1_ENDTIME_MONDAY_1"]; !ok {
		t.Errorf("missing P1_ENDTIME_MONDAY_1 in %v", written)
	}
	if _, ok := written["P1_TEMPERATURE_MONDAY_1"]; !ok {
		t.Errorf("missing P1_TEMPERATURE_MONDAY_1 in %v", written)
	}
}

func TestSetScheduleNilPayloadRejected(t *testing.T) {
	t.Parallel()
	domain, _ := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())
	if err := domain.SetSchedule(t.Context(), "0001ABCD", 1, nil); err == nil {
		t.Fatal("expected error for nil schedule")
	}
}

func TestSetScheduleInvalidatesCache(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	// Warm cache.
	if _, err := domain.GetSchedule(t.Context(), "0001ABCD", 1, false); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if got := backend.getCallCount(); got != 1 {
		t.Fatalf("warm Get calls: %d", got)
	}

	// Write — must invalidate.
	if err := domain.SetSchedule(t.Context(), "0001ABCD", 1, makeClimateP1MondayHeatedAt(19.0)); err != nil {
		t.Fatalf("SetSchedule: %v", err)
	}

	// Next GetSchedule must hit the backend again.
	if _, err := domain.GetSchedule(t.Context(), "0001ABCD", 1, false); err != nil {
		t.Fatalf("post-set: %v", err)
	}
	if got := backend.getCallCount(); got != 2 {
		t.Errorf("post-set Get calls: got %d, want 2", got)
	}
}

func TestSetScheduleCancelledContextPropagates(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := domain.SetSchedule(ctx, "0001ABCD", 1, makeClimateP1MondayHeatedAt(18.0))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
	if got := backend.putCallCount(); got != 0 {
		t.Errorf("backend Put calls: got %d, want 0 (cancelled)", got)
	}
}

// ---------------------------------------------------------------------------
// ReloadAndCacheSchedule
// ---------------------------------------------------------------------------

func TestReloadAndCacheScheduleAlwaysHitsBackend(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	if _, err := domain.ReloadAndCacheSchedule(t.Context(), "0001ABCD", 1); err != nil {
		t.Fatalf("first reload: %v", err)
	}
	if _, err := domain.ReloadAndCacheSchedule(t.Context(), "0001ABCD", 1); err != nil {
		t.Fatalf("second reload: %v", err)
	}
	if got := backend.getCallCount(); got != 2 {
		t.Errorf("backend Get calls: got %d, want 2", got)
	}
}

func TestReloadAndCacheScheduleSeedsCache(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	if _, err := domain.ReloadAndCacheSchedule(t.Context(), "0001ABCD", 1); err != nil {
		t.Fatalf("reload: %v", err)
	}
	// Subsequent non-force GetSchedule must hit the cache.
	if _, err := domain.GetSchedule(t.Context(), "0001ABCD", 1, false); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := backend.getCallCount(); got != 1 {
		t.Errorf("backend Get calls: got %d, want 1 (cache should serve second)", got)
	}
}

// ---------------------------------------------------------------------------
// CopyScheduleTo
// ---------------------------------------------------------------------------

// buildTwoDeviceFixture wires two devices on the same CCU sharing one
// fake backend so a copy from src → dst can be observed end-to-end.
func buildTwoDeviceFixture(t *testing.T, srcRaw map[string]any) (
	domain *SchedulesDomain,
	backend *scheduleIOFakeBackend,
) {
	t.Helper()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	srcDev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "SRCDEV001",
		Model:       "HmIP-eTRV",
	})
	dstDev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "DSTDEV002",
		Model:       "HmIP-eTRV",
	})
	c.ModelRegistry.Put(srcDev)
	c.ModelRegistry.Put(dstDev)

	backend = &scheduleIOFakeBackend{}
	backend.getParamsetFn = func(_ context.Context, address string, _ hmenum.ParamsetKey) (map[string]any, error) {
		backend.mu.Lock()
		backend.getCalls++
		backend.mu.Unlock()
		// Only the SRC channel address returns the schedule.
		if address != "SRCDEV001:1" {
			return map[string]any{"GLOBAL_BUTTON_LOCK": false}, nil
		}
		out := make(map[string]any, len(srcRaw))
		maps.Copy(out, srcRaw)
		return out, nil
	}
	backend.putParamsetFn = func(_ context.Context, address string, _ hmenum.ParamsetKey, values map[string]any) error {
		backend.recordPut(address, values)
		return nil
	}

	w := client.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", backend)
	domain = NewSchedulesDomain(reg, w)
	return domain, backend
}

func TestCopyScheduleToWritesSourceToTarget(t *testing.T) {
	t.Parallel()
	domain, backend := buildTwoDeviceFixture(t, fixtureClimateRawP1Monday())

	if err := domain.CopyScheduleTo(t.Context(),
		"SRCDEV001", 1, "DSTDEV002", 1); err != nil {
		t.Fatalf("CopyScheduleTo: %v", err)
	}
	written := backend.lastPut("DSTDEV002:1")
	if written == nil {
		t.Fatal("nothing written to destination")
	}
	if _, ok := written["P1_ENDTIME_MONDAY_1"]; !ok {
		t.Errorf("destination missing P1_ENDTIME_MONDAY_1 in %v", written)
	}
}

func TestCopyScheduleToSelfRejected(t *testing.T) {
	t.Parallel()
	domain, _ := buildTwoDeviceFixture(t, fixtureClimateRawP1Monday())

	err := domain.CopyScheduleTo(t.Context(),
		"SRCDEV001", 1, "SRCDEV001", 1)
	if !errors.Is(err, ErrCopyToSelf) {
		t.Errorf("got %v, want ErrCopyToSelf", err)
	}
}

func TestCopyScheduleToCancelledContext(t *testing.T) {
	t.Parallel()
	domain, backend := buildTwoDeviceFixture(t, fixtureClimateRawP1Monday())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := domain.CopyScheduleTo(ctx, "SRCDEV001", 1, "DSTDEV002", 1)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
	if got := backend.putCallCount(); got != 0 {
		t.Errorf("backend Put calls: got %d, want 0 (cancelled)", got)
	}
}

func TestCopyScheduleToEmptySourceRejected(t *testing.T) {
	t.Parallel()
	// Build a fixture whose source has no schedule keys at all.
	domain, _ := buildTwoDeviceFixture(t, map[string]any{
		"GLOBAL_BUTTON_LOCK": false,
	})
	err := domain.CopyScheduleTo(t.Context(),
		"SRCDEV001", 1, "DSTDEV002", 1)
	if err == nil {
		t.Fatal("expected error when source schedule is empty")
	}
}

// ---------------------------------------------------------------------------
// CopyProfileTo
// ---------------------------------------------------------------------------

// fixtureClimateRawP1P2 builds a paramset with two profiles (P1, P2)
// each carrying a non-trivial Monday schedule. P1 base = 18°C all day,
// P2 base = 21°C all day. The CopyProfileTo tests exercise the
// "single-slot lift" — only the source profile's keys must end up in
// the write under the target profile name.
func fixtureClimateRawP1P2() map[string]any {
	out := map[string]any{}
	for slot := 1; slot <= 13; slot++ {
		out[fmt.Sprintf("P1_ENDTIME_MONDAY_%d", slot)] = 1440
		out[fmt.Sprintf("P1_TEMPERATURE_MONDAY_%d", slot)] = 18.0
		out[fmt.Sprintf("P2_ENDTIME_MONDAY_%d", slot)] = 1440
		out[fmt.Sprintf("P2_TEMPERATURE_MONDAY_%d", slot)] = 21.0
	}
	return out
}

func TestCopyProfileToWritesOnlyTargetProfileKeys(t *testing.T) {
	t.Parallel()
	domain, backend := buildTwoDeviceFixture(t, fixtureClimateRawP1P2())

	// Copy SRC.P1 -> DST.P3
	if err := domain.CopyProfileTo(
		t.Context(),
		"SRCDEV001", 1, "P1",
		"DSTDEV002", 1, "P3",
	); err != nil {
		t.Fatalf("CopyProfileTo: %v", err)
	}

	written := backend.lastPut("DSTDEV002:1")
	if written == nil {
		t.Fatal("nothing written to destination")
	}
	// Every key must start with "P3_" — no P1 / P2 leakage allowed.
	for k := range written {
		if len(k) < 3 || k[:3] != "P3_" {
			t.Errorf("unexpected key %q in destination payload (want P3_*)", k)
		}
	}
	// P3.MONDAY temperatures must equal the source P1's (18°C).
	if got, ok := written["P3_TEMPERATURE_MONDAY_1"]; !ok {
		t.Errorf("missing P3_TEMPERATURE_MONDAY_1")
	} else if got != 18.0 {
		t.Errorf("P3_TEMPERATURE_MONDAY_1: got %v, want 18.0", got)
	}
}

func TestCopyProfileToSelfRejected(t *testing.T) {
	t.Parallel()
	domain, _ := buildTwoDeviceFixture(t, fixtureClimateRawP1P2())

	err := domain.CopyProfileTo(
		t.Context(),
		"SRCDEV001", 1, "P1",
		"SRCDEV001", 1, "P1",
	)
	if !errors.Is(err, ErrCopyToSelf) {
		t.Errorf("got %v, want ErrCopyToSelf", err)
	}
}

func TestCopyProfileToInvalidProfileIDs(t *testing.T) {
	t.Parallel()
	domain, _ := buildTwoDeviceFixture(t, fixtureClimateRawP1P2())

	cases := []struct {
		name    string
		src     string
		dst     string
		wantErr bool
	}{
		{"bad-src", "X1", "P2", true},
		{"bad-dst", "P1", "P9", true},
		{"both-valid", "P1", "P2", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := domain.CopyProfileTo(
				t.Context(),
				"SRCDEV001", 1, tc.src,
				"DSTDEV002", 1, tc.dst,
			)
			if (err != nil) != tc.wantErr {
				t.Errorf("err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestCopyProfileToMissingSourceProfile(t *testing.T) {
	t.Parallel()
	// Source only has P1 + P2.
	domain, _ := buildTwoDeviceFixture(t, fixtureClimateRawP1P2())

	err := domain.CopyProfileTo(
		t.Context(),
		"SRCDEV001", 1, "P5",
		"DSTDEV002", 1, "P3",
	)
	if err == nil {
		t.Fatal("expected error for missing source profile P5")
	}
}

func TestCopyProfileToCancelledContext(t *testing.T) {
	t.Parallel()
	domain, backend := buildTwoDeviceFixture(t, fixtureClimateRawP1P2())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := domain.CopyProfileTo(
		ctx,
		"SRCDEV001", 1, "P1",
		"DSTDEV002", 1, "P2",
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
	if got := backend.putCallCount(); got != 0 {
		t.Errorf("Put calls: got %d, want 0 (cancelled)", got)
	}
}

// ---------------------------------------------------------------------------
// CopyProfileTo — partial source coverage (wire-form)
// ---------------------------------------------------------------------------

// TestCopyProfileToPartialSourceAccepted verifies that CopyProfileTo
// succeeds when the source profile read from the wire form does not
// produce periods covering the full 24h day. The bypass that previously
// skipped validation entirely is replaced by ValidateWire(), which
// accepts partial-day sets.
func TestCopyProfileToPartialSourceAccepted(t *testing.T) {
	t.Parallel()

	// Inject a partial climate directly into the cache so the test does
	// not depend on weekprofile parsing producing the exact same partial
	// shape. We build a Climate with a partial MONDAY schedule.
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	srcDev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "SRCPARTIAL",
		Model:       "HmIP-eTRV",
	})
	dstDev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "DSTPARTIAL",
		Model:       "HmIP-eTRV",
	})
	c.ModelRegistry.Put(srcDev)
	c.ModelRegistry.Put(dstDev)

	// Build a partial ClimateWeekday: one slot, not covering 00:00→24:00.
	partialClimate := schedule.NewClimate()
	partialProf := schedule.NewClimateProfile()
	partialProf.Days[schedule.WeekdayMonday] = schedule.ClimateWeekday{
		BaseTemperature: 18,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "06:00", EndTime: "22:00", Temperature: 21},
		},
	}
	partialClimate.Profiles["P1"] = partialProf

	var putCalled bool
	backend := &scheduleIOFakeBackend{}
	backend.getParamsetFn = func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
		// Should not be called — we pre-seed the cache.
		return map[string]any{}, nil
	}
	backend.putParamsetFn = func(_ context.Context, address string, _ hmenum.ParamsetKey, values map[string]any) error {
		putCalled = true
		backend.recordPut(address, values)
		return nil
	}

	w := client.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", backend)
	domain := NewSchedulesDomain(reg, w)

	// Pre-seed the schedule cache with the partial Climate so CopyProfileTo
	// reads it without hitting the backend.
	domain.climateCache().put("SRCPARTIAL:1", partialClimate)

	if err := domain.CopyProfileTo(
		t.Context(),
		"SRCPARTIAL", 1, "P1",
		"DSTPARTIAL", 1, "P2",
	); err != nil {
		t.Fatalf("CopyProfileTo with partial source: %v", err)
	}
	if !putCalled {
		t.Fatal("expected a PutParamset call on the destination backend")
	}
	written := backend.lastPut("DSTPARTIAL:1")
	for k := range written {
		if len(k) < 3 || k[:3] != "P2_" {
			t.Errorf("unexpected key %q — want P2_* only", k)
		}
	}
}

// TestCopyProfileToBrokenSlotRejected verifies that CopyProfileTo
// rejects a source profile whose periods have endtime < starttime even
// though no 24h-coverage check is applied.
func TestCopyProfileToBrokenSlotRejected(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	srcDev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "SRCBROKEN",
		Model:       "HmIP-eTRV",
	})
	dstDev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "DSTBROKEN",
		Model:       "HmIP-eTRV",
	})
	c.ModelRegistry.Put(srcDev)
	c.ModelRegistry.Put(dstDev)

	// Build a broken ClimateWeekday: endtime < starttime.
	brokenClimate := schedule.NewClimate()
	brokenProf := schedule.NewClimateProfile()
	brokenProf.Days[schedule.WeekdayMonday] = schedule.ClimateWeekday{
		Periods: []schedule.ClimatePeriod{
			{StartTime: "10:00", EndTime: "08:00", Temperature: 21}, // broken
		},
	}
	brokenClimate.Profiles["P1"] = brokenProf

	backend := &scheduleIOFakeBackend{}
	backend.putParamsetFn = func(_ context.Context, address string, _ hmenum.ParamsetKey, values map[string]any) error {
		backend.recordPut(address, values)
		return nil
	}

	w := client.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", backend)
	domain := NewSchedulesDomain(reg, w)
	domain.climateCache().put("SRCBROKEN:1", brokenClimate)

	err = domain.CopyProfileTo(
		t.Context(),
		"SRCBROKEN", 1, "P1",
		"DSTBROKEN", 1, "P2",
	)
	if err == nil {
		t.Fatal("CopyProfileTo must reject a source profile with broken slots")
	}
	if backend.putCallCount() != 0 {
		t.Errorf("Put calls: got %d, want 0 (rejected before write)", backend.putCallCount())
	}
}

// ---------------------------------------------------------------------------
// Multi-CCU
// ---------------------------------------------------------------------------

// TestGetScheduleResolvesAcrossMultipleCentrals seeds two CCUs each
// with a different device, and checks that the domain resolves to the
// correct (central, device) tuple based on the device address.
func TestGetScheduleResolvesAcrossMultipleCentrals(t *testing.T) {
	t.Parallel()

	c1, err := central.New(central.Config{Name: "ccu-A"})
	if err != nil {
		t.Fatalf("central A: %v", err)
	}
	c2, err := central.New(central.Config{Name: "ccu-B"})
	if err != nil {
		t.Fatalf("central B: %v", err)
	}
	reg := central.NewRegistry()
	for _, c := range []*central.Unit{c1, c2} {
		if err := reg.Register(c); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}

	devA := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "AAA00001",
		Model:       "HmIP-eTRV",
	})
	devB := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "BBB00001",
		Model:       "HmIP-eTRV",
	})
	c1.ModelRegistry.Put(devA)
	c2.ModelRegistry.Put(devB)

	// Each central gets its own backend so we can prove the domain
	// routes correctly. The backends key the response off the channel
	// address.
	backendA := &scheduleIOFakeBackend{}
	backendA.getParamsetFn = func(_ context.Context, address string, _ hmenum.ParamsetKey) (map[string]any, error) {
		if address != "AAA00001:1" {
			return map[string]any{}, nil
		}
		return fixtureClimateRawP1Monday(), nil
	}
	backendB := &scheduleIOFakeBackend{}
	backendB.getParamsetFn = func(_ context.Context, address string, _ hmenum.ParamsetKey) (map[string]any, error) {
		if address != "BBB00001:1" {
			return map[string]any{}, nil
		}
		// Different temperature so we can distinguish.
		out := map[string]any{}
		for slot := 1; slot <= 13; slot++ {
			out[fmt.Sprintf("P1_ENDTIME_MONDAY_%d", slot)] = 1440
			out[fmt.Sprintf("P1_TEMPERATURE_MONDAY_%d", slot)] = 22.5
		}
		return out, nil
	}

	w := client.NewValueWriter()
	w.Register("ccu-A", "HmIP-RF", backendA)
	w.Register("ccu-B", "HmIP-RF", backendB)
	domain := NewSchedulesDomain(reg, w)

	// Read each schedule and assert the right backend was hit.
	gotA, err := domain.GetSchedule(t.Context(), "AAA00001", 1, false)
	if err != nil {
		t.Fatalf("GetSchedule A: %v", err)
	}
	if temp := gotA.Profiles["P1"].Days[schedule.WeekdayMonday].BaseTemperature; temp != 18.0 {
		t.Errorf("ccu-A base temp: got %v, want 18.0", temp)
	}

	gotB, err := domain.GetSchedule(t.Context(), "BBB00001", 1, false)
	if err != nil {
		t.Fatalf("GetSchedule B: %v", err)
	}
	if temp := gotB.Profiles["P1"].Days[schedule.WeekdayMonday].BaseTemperature; temp != 22.5 {
		t.Errorf("ccu-B base temp: got %v, want 22.5", temp)
	}
}

// ---------------------------------------------------------------------------
// Misc
// ---------------------------------------------------------------------------

func TestScheduleCacheConcurrentReadsAndWrites(t *testing.T) {
	t.Parallel()
	cache := &scheduleCache{}
	const channels = 8
	const iterations = 200
	var wg sync.WaitGroup
	for i := range channels {

		wg.Add(2)
		go func() {
			defer wg.Done()
			for range iterations {
				cache.put(fmt.Sprintf("addr%d:1", i), schedule.NewClimate())
			}
		}()
		go func() {
			defer wg.Done()
			for range iterations {
				_, _ = cache.get(fmt.Sprintf("addr%d:1", i))
			}
		}()
	}
	wg.Wait()
}

func TestScheduleCacheInvalidateRemovesEntry(t *testing.T) {
	t.Parallel()
	cache := &scheduleCache{}
	cache.put("X:1", schedule.NewClimate())
	if _, ok := cache.get("X:1"); !ok {
		t.Fatal("expected entry to exist")
	}
	cache.invalidate("X:1")
	if _, ok := cache.get("X:1"); ok {
		t.Fatal("entry should have been invalidated")
	}
}

// roundTripSnapshot ensures the structured Climate read via GetSchedule
// recovers the canonical 18.0 base + day-long pad shape (per
// fixtureClimateRawP1Monday).
func TestGetScheduleRoundTripStructure(t *testing.T) {
	t.Parallel()
	domain, _ := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	got, err := domain.GetSchedule(t.Context(), "0001ABCD", 1, false)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want := schedule.WeekdayMonday
	wd, ok := got.Profiles["P1"].Days[want]
	if !ok {
		t.Fatalf("MONDAY missing")
	}
	// Constant 18°C all day → base temp 18, no explicit periods.
	if wd.BaseTemperature != 18.0 {
		t.Errorf("base: got %v want 18.0", wd.BaseTemperature)
	}
	if len(wd.Periods) != 0 {
		t.Errorf("periods: got %v want []", wd.Periods)
	}
	// Sanity: profile keys are stable.
	if !reflect.DeepEqual(got.Keys(), []string{"P1"}) {
		t.Errorf("keys: got %v want [P1]", got.Keys())
	}
}

// ---------------------------------------------------------------------------
// MaxProfilesForDevice + per-device cap validation
// ---------------------------------------------------------------------------

// buildFixtureWithProfileCap constructs a SchedulesDomain wired to a
// recording backend. The device in the registry has an ACTIVE_PROFILE
// VALUES data point whose Max is set to capMax (e.g. 3 → P1..P3). Pass
// capMax == 0 to skip adding the ACTIVE_PROFILE dp (simulates a device
// with no such parameter — default cap applies).
func buildFixtureWithProfileCap(t *testing.T, deviceAddress string, capMax int) (
	domain *SchedulesDomain,
	backend *scheduleIOFakeBackend,
) {
	t.Helper()

	c, err := central.New(central.Config{Name: "ccu-cap"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     deviceAddress,
		Model:       "HmIP-eTRV",
	})
	ch := dev.AddChannel(deviceAddress+":1", 1, "CLIMATECONTROL_RT_TRANSCEIVER", hmenum.ParamsetKeyValues)

	if capMax > 0 {
		// Add the ACTIVE_PROFILE VALUES dp with the given Max.
		apDP := generic.NewInteger(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: deviceAddress + ":1",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(hmenum.ParameterActiveProfile),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeInteger,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
				Min:        json.RawMessage("1"),
				Max:        json.RawMessage(strconv.Itoa(capMax)),
			},
		})
		apDP.OnEvent(1)
		ch.Put(apDP)
	}

	c.ModelRegistry.Put(dev)

	backend = &scheduleIOFakeBackend{}
	backend.getParamsetFn = func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
		return fixtureClimateRawP1P2(), nil
	}
	backend.putParamsetFn = func(_ context.Context, addr string, _ hmenum.ParamsetKey, values map[string]any) error {
		backend.recordPut(addr, values)
		return nil
	}

	w := client.NewValueWriter()
	w.Register("ccu-cap", "HmIP-RF", backend)
	domain = NewSchedulesDomain(reg, w)
	return domain, backend
}

// buildFixtureWithRFProfileCap constructs a SchedulesDomain with a device
// that has WEEK_PROGRAM_POINTER (0-based, RF) instead of ACTIVE_PROFILE.
// rfMax is the 0-based maximum (e.g. rfMax=2 → 3 profiles P1..P3).
func buildFixtureWithRFProfileCap(t *testing.T, deviceAddress string, rfMax int) (
	domain *SchedulesDomain,
	backend *scheduleIOFakeBackend,
) {
	t.Helper()

	c, err := central.New(central.Config{Name: "ccu-rf"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     deviceAddress,
		Model:       "HM-CC-RT-DN",
	})
	ch := dev.AddChannel(deviceAddress+":1", 1, "CLIMATECONTROL_RT_TRANSCEIVER", hmenum.ParamsetKeyValues)

	wpDP := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: deviceAddress + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterWeekProgramPointer),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage("0"),
			Max:        json.RawMessage(strconv.Itoa(rfMax)),
		},
	})
	wpDP.OnEvent(0)
	ch.Put(wpDP)

	c.ModelRegistry.Put(dev)

	backend = &scheduleIOFakeBackend{}
	backend.getParamsetFn = func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
		return fixtureClimateRawP1P2(), nil
	}
	backend.putParamsetFn = func(_ context.Context, addr string, _ hmenum.ParamsetKey, values map[string]any) error {
		backend.recordPut(addr, values)
		return nil
	}

	w := client.NewValueWriter()
	w.Register("ccu-rf", "BidCos-RF", backend)
	domain = NewSchedulesDomain(reg, w)
	return domain, backend
}

// TestMaxProfilesForDeviceActiveProfileCap3 verifies that a device with
// ACTIVE_PROFILE Max=3 reports a cap of 3.
func TestMaxProfilesForDeviceActiveProfileCap3(t *testing.T) {
	t.Parallel()
	domain, _ := buildFixtureWithProfileCap(t, "CADEV001", 3)

	got, err := domain.MaxProfilesForDevice(t.Context(), "CADEV001")
	if err != nil {
		t.Fatalf("MaxProfilesForDevice: %v", err)
	}
	if got != 3 {
		t.Errorf("cap: got %d, want 3", got)
	}
}

// TestMaxProfilesForDeviceActiveProfileCap6 verifies that Max=6 round-trips.
func TestMaxProfilesForDeviceActiveProfileCap6(t *testing.T) {
	t.Parallel()
	domain, _ := buildFixtureWithProfileCap(t, "CADEV002", 6)

	got, err := domain.MaxProfilesForDevice(t.Context(), "CADEV002")
	if err != nil {
		t.Fatalf("MaxProfilesForDevice: %v", err)
	}
	if got != 6 {
		t.Errorf("cap: got %d, want 6", got)
	}
}

// TestMaxProfilesForDeviceRFCap3 verifies that a device with
// WEEK_PROGRAM_POINTER Max=2 (0-based) returns a cap of 3.
func TestMaxProfilesForDeviceRFCap3(t *testing.T) {
	t.Parallel()
	domain, _ := buildFixtureWithRFProfileCap(t, "RFDEV001", 2)

	got, err := domain.MaxProfilesForDevice(t.Context(), "RFDEV001")
	if err != nil {
		t.Fatalf("MaxProfilesForDevice: %v", err)
	}
	if got != 3 {
		t.Errorf("cap: got %d, want 3 (RF 0-based Max=2)", got)
	}
}

// TestMaxProfilesForDeviceUnknownDeviceDefaultCap verifies that querying
// an address that is not registered in any central returns the default
// cap (6) and no error.
func TestMaxProfilesForDeviceUnknownDeviceDefaultCap(t *testing.T) {
	t.Parallel()
	// Empty registry — device will not be found.
	domain := NewSchedulesDomain(central.NewRegistry(), client.NewValueWriter())

	got, err := domain.MaxProfilesForDevice(t.Context(), "UNKNOWN999")
	if err != nil {
		t.Fatalf("MaxProfilesForDevice: unexpected error: %v", err)
	}
	if got != defaultProfileCap {
		t.Errorf("cap: got %d, want %d (default)", got, defaultProfileCap)
	}
}

// TestMaxProfilesForDeviceNoActiveProfileDPDefaultCap verifies that a
// device in the registry but without ACTIVE_PROFILE / WEEK_PROGRAM_POINTER
// also returns the default cap.
func TestMaxProfilesForDeviceNoActiveProfileDPDefaultCap(t *testing.T) {
	t.Parallel()
	// capMax=0 → no ACTIVE_PROFILE dp added.
	domain, _ := buildFixtureWithProfileCap(t, "NODP001", 0)

	got, err := domain.MaxProfilesForDevice(t.Context(), "NODP001")
	if err != nil {
		t.Fatalf("MaxProfilesForDevice: %v", err)
	}
	if got != defaultProfileCap {
		t.Errorf("cap: got %d, want %d (default, no dp)", got, defaultProfileCap)
	}
}

// TestCopyProfileToCapRejectsBeyondCap verifies that CopyProfileTo returns
// ErrInvalidProfileID when the source profile exceeds the device's cap.
// Cap=3 → P4 must be rejected.
func TestCopyProfileToCapRejectsBeyondCap(t *testing.T) {
	t.Parallel()
	domain, _ := buildFixtureWithProfileCap(t, "SRCCP001", 3)

	err := domain.CopyProfileTo(
		t.Context(),
		"SRCCP001", 1, "P4", // P4 exceeds cap of 3
		"SRCCP001", 1, "P2",
	)
	if !errors.Is(err, ErrInvalidProfileID) {
		t.Errorf("got %v, want ErrInvalidProfileID (P4 > cap 3)", err)
	}
}

// TestCopyProfileToCapAcceptsWithinCap verifies that P3 is accepted when
// the device cap is 3.
func TestCopyProfileToCapAcceptsWithinCap(t *testing.T) {
	t.Parallel()
	domain, backend := buildFixtureWithProfileCap(t, "SRCCP002", 3)

	// Extend backend to return P1+P2+P3 so GetSchedule returns a Climate
	// with P3 available in the source.
	rawSrc := map[string]any{}
	for slot := 1; slot <= 13; slot++ {
		rawSrc[fmt.Sprintf("P1_ENDTIME_MONDAY_%d", slot)] = 1440
		rawSrc[fmt.Sprintf("P1_TEMPERATURE_MONDAY_%d", slot)] = 18.0
		rawSrc[fmt.Sprintf("P2_ENDTIME_MONDAY_%d", slot)] = 1440
		rawSrc[fmt.Sprintf("P2_TEMPERATURE_MONDAY_%d", slot)] = 21.0
		rawSrc[fmt.Sprintf("P3_ENDTIME_MONDAY_%d", slot)] = 1440
		rawSrc[fmt.Sprintf("P3_TEMPERATURE_MONDAY_%d", slot)] = 20.0
	}
	backend.getParamsetFn = func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
		out := make(map[string]any, len(rawSrc))
		maps.Copy(out, rawSrc)
		return out, nil
	}

	err := domain.CopyProfileTo(
		t.Context(),
		"SRCCP002", 1, "P1", // P1 ≤ cap of 3 → valid src
		"SRCCP002", 1, "P3", // P3 ≤ cap of 3 → valid dst (different from src)
	)
	if err != nil {
		t.Fatalf("CopyProfileTo P1→P3 with cap=3: unexpected error: %v", err)
	}
	if backend.putCallCount() != 1 {
		t.Errorf("Put calls: got %d, want 1", backend.putCallCount())
	}
}

// TestSetActiveProfileCapRejectsBeyondCap verifies that SetActiveProfile
// returns ErrInvalidProfileID when the profile exceeds the device's cap.
func TestSetActiveProfileCapRejectsBeyondCap(t *testing.T) {
	t.Parallel()
	domain, _ := buildFixtureWithProfileCap(t, "SETAP001", 3)

	err := domain.SetActiveProfile(t.Context(), "SETAP001", 1, "P4")
	if !errors.Is(err, ErrInvalidProfileID) {
		t.Errorf("got %v, want ErrInvalidProfileID (P4 > cap 3)", err)
	}
}

// TestSetScheduleCapRejectsBeyondCap verifies that SetSchedule returns
// ErrInvalidProfileID when the schedule contains a profile key that
// exceeds the device's cap.
func TestSetScheduleCapRejectsBeyondCap(t *testing.T) {
	t.Parallel()
	domain, backend := buildFixtureWithProfileCap(t, "SETSC001", 3)

	// Build a Climate with P4 which must be rejected for a cap-3 device.
	sched := schedule.NewClimate()
	prof := schedule.NewClimateProfile()
	prof.Days[schedule.WeekdayMonday] = schedule.ClimateWeekday{
		BaseTemperature: 20.0,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "00:00", EndTime: "24:00", Temperature: 20.0},
		},
	}
	sched.Profiles["P4"] = prof

	err := domain.SetSchedule(t.Context(), "SETSC001", 1, sched)
	if !errors.Is(err, ErrInvalidProfileID) {
		t.Errorf("got %v, want ErrInvalidProfileID (P4 > cap 3)", err)
	}
	if backend.putCallCount() != 0 {
		t.Errorf("Put calls: got %d, want 0 (rejected before write)", backend.putCallCount())
	}
}

// ---------------------------------------------------------------------------
// SetProfile
// ---------------------------------------------------------------------------

// makeFullDayProfile returns a ClimateProfile with Monday set to a constant
// temperature (one period covering 00:00→24:00) at the given temperature.
// This satisfies the full 24-hour coverage rule required by SetProfile.
func makeFullDayProfile(temp float64) *schedule.ClimateProfile {
	prof := schedule.NewClimateProfile()
	prof.Days[schedule.WeekdayMonday] = schedule.ClimateWeekday{
		BaseTemperature: temp,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "00:00", EndTime: "24:00", Temperature: temp},
		},
	}
	return prof
}

// TestSetProfileWritesOnlyTargetProfileKeys is the key regression test:
// SetProfile("P2", ...) must write ONLY keys prefixed with "P2_" to the
// MASTER paramset. No P1_, P3_, … keys may appear in the payload.
func TestSetProfileWritesOnlyTargetProfileKeys(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	prof := makeFullDayProfile(22.0)
	if err := domain.SetProfile(t.Context(), "0001ABCD", 1, "P2", prof); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}
	if backend.putCallCount() != 1 {
		t.Errorf("Put calls: got %d, want 1", backend.putCallCount())
	}
	written := backend.lastPut("0001ABCD:1")
	if written == nil {
		t.Fatal("nothing written to backend")
	}
	// Every written key must start with "P2_" — no P1_, P3_, … leakage.
	for k := range written {
		if len(k) < 3 || k[:3] != "P2_" {
			t.Errorf("unexpected key %q in payload (want P2_* only)", k)
		}
	}
	// Spot-check: the temperature key must reflect the supplied value.
	if got, ok := written["P2_TEMPERATURE_MONDAY_1"]; !ok {
		t.Errorf("missing P2_TEMPERATURE_MONDAY_1")
	} else if got != 22.0 {
		t.Errorf("P2_TEMPERATURE_MONDAY_1: got %v, want 22.0", got)
	}
}

// TestSetProfileInvalidatesCache verifies that a successful SetProfile call
// invalidates the schedule cache so the next GetSchedule re-fetches from
// the backend.
func TestSetProfileInvalidatesCache(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	// Warm the cache with one GetSchedule call.
	if _, err := domain.GetSchedule(t.Context(), "0001ABCD", 1, false); err != nil {
		t.Fatalf("warm GetSchedule: %v", err)
	}
	if got := backend.getCallCount(); got != 1 {
		t.Fatalf("warm Get calls: got %d, want 1", got)
	}

	// Write a profile — must invalidate the cache.
	if err := domain.SetProfile(t.Context(), "0001ABCD", 1, "P1", makeFullDayProfile(19.0)); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}

	// Next GetSchedule must hit the backend again (cache was invalidated).
	if _, err := domain.GetSchedule(t.Context(), "0001ABCD", 1, false); err != nil {
		t.Fatalf("post-set GetSchedule: %v", err)
	}
	if got := backend.getCallCount(); got != 2 {
		t.Errorf("Get calls after SetProfile: got %d, want 2 (cache must be invalidated)", got)
	}
}

// TestSetProfileNilProfileRejected ensures a nil profile argument returns an
// error before any backend write.
func TestSetProfileNilProfileRejected(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	if err := domain.SetProfile(t.Context(), "0001ABCD", 1, "P1", nil); err == nil {
		t.Fatal("expected error for nil profile")
	}
	if backend.putCallCount() != 0 {
		t.Errorf("Put calls: got %d, want 0 (rejected before write)", backend.putCallCount())
	}
}

// TestSetProfileInvalidKeyRejected verifies that invalid profile keys
// ("P0", "P9", "X1") are rejected before any backend write.
func TestSetProfileInvalidKeyRejected(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	for _, key := range []string{"P0", "P9", "X1", "", "P"} {
		if err := domain.SetProfile(t.Context(), "0001ABCD", 1, key, makeFullDayProfile(20.0)); err == nil {
			t.Errorf("SetProfile(%q): expected error, got nil", key)
		}
	}
	if backend.putCallCount() != 0 {
		t.Errorf("Put calls: got %d, want 0 (all writes must be rejected)", backend.putCallCount())
	}
}

// TestSetProfileCapRejectsBeyondCap verifies that SetProfile returns
// ErrInvalidProfileID when the profile key exceeds the device's cap.
func TestSetProfileCapRejectsBeyondCap(t *testing.T) {
	t.Parallel()
	domain, backend := buildFixtureWithProfileCap(t, "SPDEV001", 3)

	err := domain.SetProfile(t.Context(), "SPDEV001", 1, "P4", makeFullDayProfile(20.0))
	if !errors.Is(err, ErrInvalidProfileID) {
		t.Errorf("got %v, want ErrInvalidProfileID (P4 > cap 3)", err)
	}
	if backend.putCallCount() != 0 {
		t.Errorf("Put calls: got %d, want 0 (rejected before write)", backend.putCallCount())
	}
}

// TestSetProfileCancelledContextPropagates verifies that a cancelled context
// is returned before any backend write occurs.
func TestSetProfileCancelledContextPropagates(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := domain.SetProfile(ctx, "0001ABCD", 1, "P1", makeFullDayProfile(20.0))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
	if backend.putCallCount() != 0 {
		t.Errorf("Put calls: got %d, want 0 (cancelled)", backend.putCallCount())
	}
}
