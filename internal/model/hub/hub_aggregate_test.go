// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"testing"
)

func TestHubRegistersPrograms(t *testing.T) {
	h := NewHub("ccu-01")
	if h.CentralName != "ccu-01" {
		t.Fatalf("central name: %s", h.CentralName)
	}
	h.PutProgram(&Program{HubDataPoint: HubDataPoint{Name: "One"}, ID: "P1"})
	h.PutProgram(&Program{HubDataPoint: HubDataPoint{Name: "Two"}, ID: "P2"})
	h.PutProgram(nil)              // ignored
	h.PutProgram(&Program{ID: ""}) // ignored

	if _, ok := h.Program("P1"); !ok {
		t.Fatal("P1 missing")
	}
	if _, ok := h.Program("missing"); ok {
		t.Fatal("unexpected hit")
	}
	list := h.Programs()
	if len(list) != 2 || list[0].ID != "P1" || list[1].ID != "P2" {
		t.Fatalf("programs: %+v", list)
	}
	if !h.RemoveProgram("P1") {
		t.Fatal("P1 should have been removed")
	}
	if h.RemoveProgram("P1") {
		t.Fatal("second remove should report false")
	}
}

func TestHubRegistersSysvars(t *testing.T) {
	h := NewHub("ccu-01")
	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: "Alpha"}})
	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: "Beta"}})
	h.PutSysvar(nil)
	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: ""}})

	if _, ok := h.Sysvar("Alpha"); !ok {
		t.Fatal("Alpha missing")
	}
	list := h.Sysvars()
	if len(list) != 2 || list[0].Name != "Alpha" {
		t.Fatalf("sysvars: %+v", list)
	}
}

func TestMetricsObserveAndDedup(t *testing.T) {
	m := NewMetrics()
	var fired int
	m.OnUpdate(MetricSystemHealth, func(MetricSample) { fired++ })
	if !m.Observe(MetricSystemHealth, 95) {
		t.Fatal("first observation should change")
	}
	if m.Observe(MetricSystemHealth, 95) {
		t.Fatal("identical observation must not change")
	}
	if !m.Observe(MetricSystemHealth, 90) {
		t.Fatal("different observation should change")
	}
	if fired != 2 {
		t.Fatalf("fired=%d, want 2", fired)
	}
	s, ok := m.Value(MetricSystemHealth)
	if !ok || s.Value != 90 {
		t.Fatalf("value=%+v ok=%v", s, ok)
	}
}

func TestMetricsOnAny(t *testing.T) {
	m := NewMetrics()
	var anyFired int
	m.OnAny(func(MetricSample) { anyFired++ })
	m.Observe(MetricSystemHealth, 1)
	m.Observe(MetricConnectionLatMs, 50)
	m.Observe(MetricLastEventAgeSecs, 3)
	if anyFired != 3 {
		t.Fatalf("any=%d", anyFired)
	}
	snap := m.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("snapshot=%d", len(snap))
	}
}

func TestInboxReplaceDedup(t *testing.T) {
	in := NewInbox()
	var fired int
	in.OnUpdate(func([]InboxDevice) { fired++ })

	d1 := InboxDevice{Address: "0001", Model: "HmIP-BROLL"}
	d2 := InboxDevice{Address: "0002", Model: "HmIP-STH"}
	in.Replace([]InboxDevice{d1, d2})
	if in.Count() != 2 {
		t.Fatalf("count=%d", in.Count())
	}
	if fired != 1 {
		t.Fatalf("fired=%d", fired)
	}
	// Same set again → no fire.
	in.Replace([]InboxDevice{d1, d2})
	if fired != 1 {
		t.Fatalf("identical replace fired=%d", fired)
	}
	// Different → fire.
	in.Replace([]InboxDevice{d1})
	if fired != 2 {
		t.Fatalf("shrink fired=%d", fired)
	}
	list := in.List()
	if len(list) != 1 || list[0].Address != "0001" {
		t.Fatalf("list=%+v", list)
	}
}
