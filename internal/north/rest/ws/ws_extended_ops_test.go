// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/cachereset"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// Stubs for the new interfaces
// ---------------------------------------------------------------------------

type stubThrottleStats struct {
	stats []map[string]any
	err   error
}

func (s *stubThrottleStats) CommandThrottleStats(_ context.Context) ([]map[string]any, error) {
	return s.stats, s.err
}

type stubCacheClearer struct {
	lastScope cachereset.Scope
	report    cachereset.Report
	err       error
}

func (s *stubCacheClearer) ClearCache(_ context.Context, scope cachereset.Scope) (cachereset.Report, error) {
	s.lastScope = scope
	s.report.Scope = scope
	return s.report, s.err
}

type stubDeviceStats struct {
	data map[string]any
	err  error
}

func (s *stubDeviceStats) DeviceStatistics(_ context.Context) (map[string]any, error) {
	return s.data, s.err
}

type stubFirmwareRefresher struct {
	refreshed bool
	err       error
}

func (s *stubFirmwareRefresher) RefreshFirmwareData(_ context.Context) error {
	s.refreshed = true
	return s.err
}

type stubChangeHistoryClearer struct {
	cleared bool
	err     error
}

func (s *stubChangeHistoryClearer) ClearChangeHistory(_ context.Context) error {
	s.cleared = true
	return s.err
}

type stubIncidentClearer struct {
	cleared bool
	err     error
}

func (s *stubIncidentClearer) ClearIncidents(_ context.Context) error {
	s.cleared = true
	return s.err
}

type opsExtendedBundle struct {
	router *Router
	ts     *stubThrottleStats
	cc     *stubCacheClearer
	ds     *stubDeviceStats
	fr     *stubFirmwareRefresher
	chc    *stubChangeHistoryClearer
	ic     *stubIncidentClearer
}

func newRouterWithOpsExtended() *opsExtendedBundle {
	b := &opsExtendedBundle{
		router: NewRouter(),
		ts:     &stubThrottleStats{stats: []map[string]any{{"interface": "HmIP-RF", "in_flight": 0}}},
		cc:     &stubCacheClearer{},
		ds:     &stubDeviceStats{data: map[string]any{"total": 42}},
		fr:     &stubFirmwareRefresher{},
		chc:    &stubChangeHistoryClearer{},
		ic:     &stubIncidentClearer{},
	}
	RegisterExtendedCommands(b.router, ExtendedCommandsConfig{
		ThrottleStats:        b.ts,
		CacheClearer:         b.cc,
		DeviceStatistics:     b.ds,
		FirmwareRefresher:    b.fr,
		ChangeHistoryClearer: b.chc,
		IncidentClearer:      b.ic,
	})
	return b
}

func dispatchOps(t *testing.T, r *Router, name string) any {
	t.Helper()
	res := r.Dispatch(ctxForCommand(name), name, json.RawMessage(`{}`))
	if res.Error != nil {
		t.Fatalf("%s: dispatch err: %v", name, res.Error.Message)
	}
	return res.Data
}

func dispatchOpsExpectErr(t *testing.T, r *Router, name string) {
	t.Helper()
	res := r.Dispatch(ctxForCommand(name), name, json.RawMessage(`{}`))
	if res.Error == nil {
		t.Fatalf("%s: expected error, got data: %v", name, res.Data)
	}
}

// ---------------------------------------------------------------------------
// ccu.throttle_stats
// ---------------------------------------------------------------------------

func TestCCUThrottleStatsHandler(t *testing.T) {
	b := newRouterWithOpsExtended()
	out := dispatchOps(t, b.router, "ccu.throttle_stats").(map[string]any)
	throttles, ok := out["throttles"].([]map[string]any)
	if !ok || len(throttles) == 0 {
		t.Fatalf("expected throttles slice, got %v", out)
	}
}

func TestCCUThrottleStatsHandlerError(t *testing.T) {
	r := NewRouter()
	ts := &stubThrottleStats{err: errors.New("rpc failure")}
	RegisterExtendedCommands(r, ExtendedCommandsConfig{ThrottleStats: ts})
	dispatchOpsExpectErr(t, r, "ccu.throttle_stats")
}

// ---------------------------------------------------------------------------
// ccu.cache_clear
// ---------------------------------------------------------------------------

func dispatchCacheClear(t *testing.T, r *Router, raw string) any {
	t.Helper()
	res := r.Dispatch(adminCtx(), "ccu.cache_clear", json.RawMessage(raw))
	if res.Error != nil {
		t.Fatalf("ccu.cache_clear: dispatch err: %v", res.Error.Message)
	}
	return res.Data
}

func TestCCUCacheClearHandlerGlobal(t *testing.T) {
	// Omitting kind defaults to "global".
	b := newRouterWithOpsExtended()
	out := dispatchCacheClear(t, b.router, `{}`).(map[string]any)
	if out["scope"] != "global" {
		t.Fatalf("expected scope=global, got %v", out["scope"])
	}
	if b.cc.lastScope.Kind != cachereset.ScopeGlobal {
		t.Fatalf("expected ScopeGlobal, got %v", b.cc.lastScope.Kind)
	}
}

func TestCCUCacheClearHandlerCentral(t *testing.T) {
	b := newRouterWithOpsExtended()
	out := dispatchCacheClear(t, b.router, `{"kind":"central","central":"ccu1"}`).(map[string]any)
	if out["scope"] != "central" {
		t.Fatalf("expected scope=central, got %v", out["scope"])
	}
	if b.cc.lastScope.Kind != cachereset.ScopeCentral || b.cc.lastScope.Central != "ccu1" {
		t.Fatalf("unexpected scope: %+v", b.cc.lastScope)
	}
}

func TestCCUCacheClearHandlerInterface(t *testing.T) {
	b := newRouterWithOpsExtended()
	out := dispatchCacheClear(t, b.router, `{"kind":"interface","central":"ccu1","interface":"HmIP-RF"}`).(map[string]any)
	if out["scope"] != "interface" {
		t.Fatalf("expected scope=interface, got %v", out["scope"])
	}
	if b.cc.lastScope.Interface != "HmIP-RF" {
		t.Fatalf("unexpected scope: %+v", b.cc.lastScope)
	}
}

func TestCCUCacheClearHandlerDevice(t *testing.T) {
	b := newRouterWithOpsExtended()
	out := dispatchCacheClear(t, b.router, `{"kind":"device","central":"ccu1","interface":"HmIP-RF","device":"ABC0001"}`).(map[string]any)
	if out["scope"] != "device" {
		t.Fatalf("expected scope=device, got %v", out["scope"])
	}
	if b.cc.lastScope.Device != "ABC0001" {
		t.Fatalf("unexpected scope: %+v", b.cc.lastScope)
	}
}

func TestCCUCacheClearHandlerReportFields(t *testing.T) {
	// Verify that Report fields are forwarded to the client.
	r := NewRouter()
	cc := &stubCacheClearer{report: cachereset.Report{
		Devices:        3,
		Paramsets:      7,
		CentralsReinit: []string{"ccu1"},
	}}
	RegisterExtendedCommands(r, ExtendedCommandsConfig{CacheClearer: cc})
	res := r.Dispatch(adminCtx(), "ccu.cache_clear", json.RawMessage(`{}`))
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error.Message)
	}
	out := res.Data.(map[string]any)
	if out["devices"] != int64(3) {
		t.Fatalf("expected devices=3, got %v", out["devices"])
	}
	if out["paramsets"] != int64(7) {
		t.Fatalf("expected paramsets=7, got %v", out["paramsets"])
	}
}

func TestCCUCacheClearHandlerBadScope(t *testing.T) {
	// A central-scoped clear without providing the central name must return
	// a bad-request error.
	r := NewRouter()
	cc := &stubCacheClearer{}
	RegisterExtendedCommands(r, ExtendedCommandsConfig{CacheClearer: cc})
	res := r.Dispatch(adminCtx(), "ccu.cache_clear", json.RawMessage(`{"kind":"central"}`))
	if res.Error == nil {
		t.Fatal("expected error for missing central field, got none")
	}
}

func TestCCUCacheClearHandlerError(t *testing.T) {
	r := NewRouter()
	cc := &stubCacheClearer{err: errors.New("db error")}
	RegisterExtendedCommands(r, ExtendedCommandsConfig{CacheClearer: cc})
	dispatchOpsExpectErr(t, r, "ccu.cache_clear")
}

// ---------------------------------------------------------------------------
// ccu.device_statistics
// ---------------------------------------------------------------------------

func TestCCUDeviceStatisticsHandler(t *testing.T) {
	b := newRouterWithOpsExtended()
	out := dispatchOps(t, b.router, "ccu.device_statistics").(map[string]any)
	if out["total"] != 42 {
		t.Fatalf("expected total=42, got %v", out)
	}
}

func TestCCUDeviceStatisticsHandlerError(t *testing.T) {
	r := NewRouter()
	ds := &stubDeviceStats{err: errors.New("query fail")}
	RegisterExtendedCommands(r, ExtendedCommandsConfig{DeviceStatistics: ds})
	dispatchOpsExpectErr(t, r, "ccu.device_statistics")
}

// ---------------------------------------------------------------------------
// firmware.refresh
// ---------------------------------------------------------------------------

func TestFirmwareRefreshHandler(t *testing.T) {
	b := newRouterWithOpsExtended()
	out := dispatchOps(t, b.router, "firmware.refresh").(map[string]any)
	if out["success"] != true {
		t.Fatalf("expected success=true, got %v", out)
	}
	if !b.fr.refreshed {
		t.Fatal("expected RefreshFirmwareData to be called")
	}
}

func TestFirmwareRefreshHandlerError(t *testing.T) {
	r := NewRouter()
	fr := &stubFirmwareRefresher{err: errors.New("ccu down")}
	RegisterExtendedCommands(r, ExtendedCommandsConfig{FirmwareRefresher: fr})
	dispatchOpsExpectErr(t, r, "firmware.refresh")
}

// ---------------------------------------------------------------------------
// change_history.clear
// ---------------------------------------------------------------------------

func TestChangeHistoryClearHandler(t *testing.T) {
	b := newRouterWithOpsExtended()
	out := dispatchOps(t, b.router, "change_history.clear").(map[string]any)
	if out["success"] != true {
		t.Fatalf("expected success=true, got %v", out)
	}
	if !b.chc.cleared {
		t.Fatal("expected ClearChangeHistory to be called")
	}
}

func TestChangeHistoryClearHandlerError(t *testing.T) {
	r := NewRouter()
	chc := &stubChangeHistoryClearer{err: errors.New("db full")}
	RegisterExtendedCommands(r, ExtendedCommandsConfig{ChangeHistoryClearer: chc})
	dispatchOpsExpectErr(t, r, "change_history.clear")
}

// ---------------------------------------------------------------------------
// incidents.clear
// ---------------------------------------------------------------------------

func TestIncidentsClearHandler(t *testing.T) {
	b := newRouterWithOpsExtended()
	out := dispatchOps(t, b.router, "incidents.clear").(map[string]any)
	if out["success"] != true {
		t.Fatalf("expected success=true, got %v", out)
	}
	if !b.ic.cleared {
		t.Fatal("expected ClearIncidents to be called")
	}
}

func TestIncidentsClearHandlerError(t *testing.T) {
	r := NewRouter()
	ic := &stubIncidentClearer{err: errors.New("store error")}
	RegisterExtendedCommands(r, ExtendedCommandsConfig{IncidentClearer: ic})
	dispatchOpsExpectErr(t, r, "incidents.clear")
}

// ---------------------------------------------------------------------------
// client subscribe / unsubscribe
// ---------------------------------------------------------------------------

// makeTestClient creates a minimal client with a small out-buffer for testing.
func makeTestClient(bufSize int) *client {
	c := &client{
		out:    make(chan Event, bufSize),
		closed: make(chan struct{}),
	}
	return c
}

func TestClientSubscribeDeduplicated(t *testing.T) {
	c := makeTestClient(8)
	c.subscribe([]string{"device.*", "device.*", ""})
	// Empty string and duplicate should both be ignored.
	if len(c.topics) != 1 {
		t.Fatalf("expected 1 topic after dedup, got %d: %v", len(c.topics), c.topics)
	}
}

func TestClientUnsubscribe(t *testing.T) {
	c := makeTestClient(8)
	c.subscribe([]string{"device.*", "hub.*", "system.*"})
	if !c.matches("device.0001") {
		t.Fatal("expected match before unsubscribe")
	}
	c.unsubscribe([]string{"device.*"})
	if c.matches("device.0001") {
		t.Fatal("should not match after unsubscribe")
	}
	if !c.matches("hub.info") {
		t.Fatal("hub.* should still match")
	}
}

func TestClientUnsubscribeNonExistent(t *testing.T) {
	c := makeTestClient(8)
	c.subscribe([]string{"device.*"})
	// Unsubscribing a non-existent topic should be a no-op.
	c.unsubscribe([]string{"unknown.*"})
	if !c.matches("device.0001") {
		t.Fatal("device.* should still match")
	}
}

func TestClientEnqueueIntoBuffer(t *testing.T) {
	// Buffer size 4 — verify events land in the channel.
	c := makeTestClient(4)
	c.subscribe([]string{"*"})
	c.enqueue(Event{Topic: "t1"})
	c.enqueue(Event{Topic: "t2"})
	if len(c.out) != 2 {
		t.Fatalf("expected 2 events in buffer, got %d", len(c.out))
	}
}

// ---------------------------------------------------------------------------
// supportedOperationsForWS
// ---------------------------------------------------------------------------

func TestSupportedOperationsForWS(t *testing.T) {
	cases := []struct {
		cat  hmenum.DataPointCategory
		want string
	}{
		{hmenum.DataPointCategoryLight, "turn_on"},
		{hmenum.DataPointCategoryClimate, "set_temperature"},
		{hmenum.DataPointCategoryCover, "open"},
		{hmenum.DataPointCategoryLock, "lock"},
		{hmenum.DataPointCategorySiren, "turn_on"},
		{hmenum.DataPointCategoryTextDisplay, "write"},
		{hmenum.DataPointCategoryValve, "open"},
		{hmenum.DataPointCategorySwitch, "turn_on"},
	}
	for _, tc := range cases {
		ops := supportedOperationsForWS(tc.cat)
		if len(ops) == 0 {
			t.Errorf("category %v: expected operations, got none", tc.cat)
			continue
		}
		found := slices.Contains(ops, tc.want)
		if !found {
			t.Errorf("category %v: expected %q in %v", tc.cat, tc.want, ops)
		}
	}
}

func TestSupportedOperationsForWSUnknownCategory(t *testing.T) {
	ops := supportedOperationsForWS(hmenum.DataPointCategory("unknown"))
	if ops != nil {
		t.Fatalf("expected nil for unknown category, got %v", ops)
	}
}

// ---------------------------------------------------------------------------
// wsParsePriority
// ---------------------------------------------------------------------------

func TestWsParsePriority(t *testing.T) {
	if p := wsParsePriority("critical"); p != hmenum.CommandPriorityCritical {
		t.Fatalf("expected critical priority")
	}
	if p := wsParsePriority("low"); p != hmenum.CommandPriorityLow {
		t.Fatalf("expected low priority")
	}
	if p := wsParsePriority(""); p != hmenum.CommandPriorityHigh {
		t.Fatalf("expected high priority for empty string")
	}
	if p := wsParsePriority("unknown"); p != hmenum.CommandPriorityHigh {
		t.Fatalf("expected high priority for unknown string")
	}
}

// ---------------------------------------------------------------------------
// SystemStatusSubscriber
// ---------------------------------------------------------------------------

func TestSystemStatusTopic(t *testing.T) {
	want := "system.ccu1.status"
	got := SystemStatusTopic("ccu1")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestNewSystemStatusSubscriberNilSafe(t *testing.T) {
	// nil registry and hub should not panic
	sub := NewSystemStatusSubscriber(nil, nil)
	sub.Start()
	sub.Stop()
}
