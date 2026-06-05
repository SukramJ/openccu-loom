// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeHomegearBackend is a narrow stand-in for the Homegear XML-RPC
// sysvar surface used by loadHomegearSysvars / homegearSysvarWriter.
type fakeHomegearBackend struct {
	sysvars []map[string]any
	getErr  error
	sets    []hgSetCall
}

type hgSetCall struct {
	name  string
	value any
}

func (f *fakeHomegearBackend) GetAllSystemVariables(_ context.Context) ([]map[string]any, error) {
	return f.sysvars, f.getErr
}

func (f *fakeHomegearBackend) SetSystemVariable(_ context.Context, name string, value any) error {
	f.sets = append(f.sets, hgSetCall{name, value})
	return nil
}

// myNamedInt simulates a named transport type (e.g. xmlrpc.IntValue)
// that normalizeScalar must collapse to a native int.
type myNamedInt int64

func TestLoadHomegearSysvars_PopulatesWithInferredTypes(t *testing.T) {
	t.Parallel()
	backend := &fakeHomegearBackend{sysvars: []map[string]any{
		{"name": "Presence", "value": true},
		{"name": "Counter", "value": 7},
		{"name": "Temp", "value": 21.5},
		{"name": "Mode", "value": "eco"},
		{"name": "WireInt", "value": myNamedInt(42)},
	}}
	h := hub.NewHub("hg1")

	if err := loadHomegearSysvars(context.Background(), backend, h, &homegearSysvarWriter{backend: backend}); err != nil {
		t.Fatalf("loadHomegearSysvars: %v", err)
	}

	cases := []struct {
		name    string
		wantVT  hmenum.HubValueType
		wantVal any
	}{
		{"Presence", hmenum.HubValueTypeLogic, true},
		{"Counter", hmenum.HubValueTypeInteger, 7},
		{"Temp", hmenum.HubValueTypeFloat, 21.5},
		{"Mode", hmenum.HubValueTypeString, "eco"},
		{"WireInt", hmenum.HubValueTypeInteger, 42},
	}
	for _, tc := range cases {
		sv, ok := h.Sysvar(tc.name)
		if !ok {
			t.Errorf("sysvar %q not populated", tc.name)
			continue
		}
		if sv.ValueType != tc.wantVT {
			t.Errorf("sysvar %q ValueType = %q, want %q", tc.name, sv.ValueType, tc.wantVT)
		}
		pv, observed := sv.Value()
		if !observed {
			t.Errorf("sysvar %q has no observed value", tc.name)
			continue
		}
		if got := pv.Unwrap(); got != tc.wantVal {
			t.Errorf("sysvar %q value = %v (%T), want %v (%T)", tc.name, got, got, tc.wantVal, tc.wantVal)
		}
	}
}

func TestLoadHomegearSysvars_SkipsExcluded(t *testing.T) {
	t.Parallel()
	backend := &fakeHomegearBackend{sysvars: []map[string]any{
		{"name": "Keep", "value": 1},
		{"name": "PresenceOldVal", "value": 1}, // contains "OldVal"
		{"name": "pcCCUID", "value": "x"},
		{"name": "", "value": 1}, // empty name
	}}
	h := hub.NewHub("hg1")

	if err := loadHomegearSysvars(context.Background(), backend, h, &homegearSysvarWriter{backend: backend}); err != nil {
		t.Fatalf("loadHomegearSysvars: %v", err)
	}
	if _, ok := h.Sysvar("PresenceOldVal"); ok {
		t.Error("excluded sysvar (OldVal marker) was populated")
	}
	if _, ok := h.Sysvar("pcCCUID"); ok {
		t.Error("excluded sysvar (pcCCUID) was populated")
	}
	if got := len(h.Sysvars()); got != 1 {
		t.Errorf("populated %d sysvars, want 1 (only Keep)", got)
	}
}

func TestLoadHomegearSysvars_RemovesStaleOnRefresh(t *testing.T) {
	t.Parallel()
	backend := &fakeHomegearBackend{sysvars: []map[string]any{
		{"name": "A", "value": 1},
		{"name": "B", "value": 2},
	}}
	h := hub.NewHub("hg1")
	w := &homegearSysvarWriter{backend: backend}
	if err := loadHomegearSysvars(context.Background(), backend, h, w); err != nil {
		t.Fatalf("first load: %v", err)
	}
	if len(h.Sysvars()) != 2 {
		t.Fatalf("after first load: %d sysvars, want 2", len(h.Sysvars()))
	}
	// B disappears from the backend on the next refresh.
	backend.sysvars = []map[string]any{{"name": "A", "value": 9}}
	if err := loadHomegearSysvars(context.Background(), backend, h, w); err != nil {
		t.Fatalf("second load: %v", err)
	}
	if _, ok := h.Sysvar("B"); ok {
		t.Error("stale sysvar B was not removed on refresh")
	}
	a, ok := h.Sysvar("A")
	if !ok {
		t.Fatal("sysvar A removed unexpectedly")
	}
	if pv, _ := a.Value(); pv.Unwrap() != 9 {
		t.Errorf("sysvar A value not updated on refresh: got %v, want 9", pv.Unwrap())
	}
}

func TestLoadHomegearSysvars_PropagatesError(t *testing.T) {
	t.Parallel()
	backend := &fakeHomegearBackend{getErr: errors.New("boom")}
	h := hub.NewHub("hg1")
	if err := loadHomegearSysvars(context.Background(), backend, h, &homegearSysvarWriter{backend: backend}); err == nil {
		t.Fatal("expected error from backend, got nil")
	}
}

func TestHomegearSysvarWriter_RoutesToBackend(t *testing.T) {
	t.Parallel()
	backend := &fakeHomegearBackend{}
	w := &homegearSysvarWriter{backend: backend}
	if err := w.SetSysvar(context.Background(), "Light", true); err != nil {
		t.Fatalf("SetSysvar: %v", err)
	}
	if len(backend.sets) != 1 || backend.sets[0].name != "Light" || backend.sets[0].value != true {
		t.Errorf("SetSysvar did not route to backend.SetSystemVariable: %+v", backend.sets)
	}
}
