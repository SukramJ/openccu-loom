// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cachereset

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// ── fakes ────────────────────────────────────────────────────────────────────

type (
	clearCall  struct{ central, iface string }
	deleteCall struct{ central, iface, address string }
)

type fakeDevices struct {
	mu          sync.Mutex
	clearCalls  []clearCall
	deleteCalls []deleteCall
	clearN      int64
	clearErr    error
	deleteN     int64
	deleteErr   error
}

func (f *fakeDevices) Clear(_ context.Context, c, i string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearCalls = append(f.clearCalls, clearCall{c, i})
	return f.clearN, f.clearErr
}

func (f *fakeDevices) Delete(_ context.Context, c, i, a string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls = append(f.deleteCalls, deleteCall{c, i, a})
	return f.deleteN, f.deleteErr
}

type (
	paramsetClearCall  struct{ central, iface string }
	paramsetDeleteCall struct{ central, iface, device string }
)

type fakeParamsets struct {
	mu          sync.Mutex
	clearCalls  []paramsetClearCall
	deleteCalls []paramsetDeleteCall
	clearN      int64
	clearErr    error
	deleteN     int64
	deleteErr   error
}

func (f *fakeParamsets) ClearForInterface(_ context.Context, c, i string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearCalls = append(f.clearCalls, paramsetClearCall{c, i})
	return f.clearN, f.clearErr
}

func (f *fakeParamsets) DeleteDevice(_ context.Context, c, i, d string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls = append(f.deleteCalls, paramsetDeleteCall{c, i, d})
	return f.deleteN, f.deleteErr
}

type (
	ifaceCall  struct{ central, iface string }
	deviceCall struct{ central, iface, device string }
)

type fakeValues struct {
	mu          sync.Mutex
	ifaceCalls  []ifaceCall
	deviceCalls []deviceCall
	ifaceN      int64
	ifaceErr    error
	deviceErr   error
}

func (f *fakeValues) DeleteForInterface(_ context.Context, c, i string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ifaceCalls = append(f.ifaceCalls, ifaceCall{c, i})
	return f.ifaceN, f.ifaceErr
}

func (f *fakeValues) DeleteDevice(_ context.Context, c, i, d string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deviceCalls = append(f.deviceCalls, deviceCall{c, i, d})
	return f.deviceErr
}

type fakeMaster struct {
	mu          sync.Mutex
	ifaceCalls  []ifaceCall
	deviceCalls []deviceCall
	ifaceN      int64
	ifaceErr    error
	deviceErr   error
}

func (f *fakeMaster) DeleteForInterface(_ context.Context, c, i string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ifaceCalls = append(f.ifaceCalls, ifaceCall{c, i})
	return f.ifaceN, f.ifaceErr
}

func (f *fakeMaster) DeleteDevice(_ context.Context, c, i, d string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deviceCalls = append(f.deviceCalls, deviceCall{c, i, d})
	return f.deviceErr
}

type fakeTopology struct {
	centrals   []string
	interfaces map[string][]string
}

func (f *fakeTopology) Centrals() []string { return f.centrals }
func (f *fakeTopology) Interfaces(c string) []string {
	if f.interfaces == nil {
		return nil
	}
	return f.interfaces[c]
}

type reinitCall struct{ central string }

type fakeReiniter struct {
	mu    sync.Mutex
	calls []reinitCall
	ok    bool
}

func (f *fakeReiniter) ReinitCentral(_ context.Context, c string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, reinitCall{c})
	return f.ok
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestScopeValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		scope   Scope
		wantErr bool
	}{
		{"global ok", Scope{Kind: ScopeGlobal}, false},
		{"central ok", Scope{Kind: ScopeCentral, Central: "ccu"}, false},
		{"central missing central", Scope{Kind: ScopeCentral}, true},
		{"interface ok", Scope{Kind: ScopeInterface, Central: "ccu", Interface: "HmIP-RF"}, false},
		{"interface missing interface", Scope{Kind: ScopeInterface, Central: "ccu"}, true},
		{"interface missing central", Scope{Kind: ScopeInterface, Interface: "HmIP-RF"}, true},
		{"device ok", Scope{Kind: ScopeDevice, Central: "ccu", Interface: "HmIP-RF", Device: "ABC:1"}, false},
		{"device missing device", Scope{Kind: ScopeDevice, Central: "ccu", Interface: "HmIP-RF"}, true},
		{"unknown kind", Scope{Kind: "unknown"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.scope.Validate()
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestScopeString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		scope Scope
		want  string
	}{
		{Scope{Kind: ScopeGlobal}, "global"},
		{Scope{Kind: ScopeCentral, Central: "ccu"}, "central:ccu"},
		{Scope{Kind: ScopeInterface, Central: "ccu", Interface: "HmIP-RF"}, "interface:ccu/HmIP-RF"},
		{Scope{Kind: ScopeDevice, Central: "ccu", Interface: "HmIP-RF", Device: "ABC:1"}, "device:ccu/HmIP-RF/ABC:1"},
		{Scope{Kind: "unknown"}, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := tc.scope.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClearInterfaceScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	devs := &fakeDevices{clearN: 3}
	params := &fakeParamsets{clearN: 2}
	vals := &fakeValues{ifaceN: 5}
	master := &fakeMaster{ifaceN: 4}
	reiniter := &fakeReiniter{ok: true}

	var cacheMu sync.Mutex
	var cacheCleared []string
	var auditCalls int

	svc := New(Deps{
		Devices:   devs,
		Paramsets: params,
		Values:    vals,
		Master:    master,
		Topology:  &fakeTopology{},
		Reiniter:  reiniter,
		ClearValueCache: func(c string) {
			cacheMu.Lock()
			cacheCleared = append(cacheCleared, c)
			cacheMu.Unlock()
		},
		Audit: func(_ context.Context, _ Scope, _ Report) { auditCalls++ },
	})

	scope := Scope{Kind: ScopeInterface, Central: "ccu", Interface: "HmIP-RF"}
	rep, err := svc.Clear(ctx, scope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Devices.Clear called once, Delete not called
	if len(devs.clearCalls) != 1 || devs.clearCalls[0] != (clearCall{"ccu", "HmIP-RF"}) {
		t.Errorf("Devices.Clear calls = %v, want [{ccu HmIP-RF}]", devs.clearCalls)
	}
	if len(devs.deleteCalls) != 0 {
		t.Errorf("Devices.Delete should not be called, got %d calls", len(devs.deleteCalls))
	}

	// Paramsets.ClearForInterface called once, DeleteDevice not called
	if len(params.clearCalls) != 1 || params.clearCalls[0] != (paramsetClearCall{"ccu", "HmIP-RF"}) {
		t.Errorf("Paramsets.ClearForInterface calls = %v", params.clearCalls)
	}
	if len(params.deleteCalls) != 0 {
		t.Errorf("Paramsets.DeleteDevice should not be called")
	}

	// Values.DeleteForInterface called once, DeleteDevice not called
	if len(vals.ifaceCalls) != 1 {
		t.Errorf("Values.DeleteForInterface calls = %d, want 1", len(vals.ifaceCalls))
	}
	if len(vals.deviceCalls) != 0 {
		t.Errorf("Values.DeleteDevice should not be called")
	}

	// Master.DeleteForInterface called once, DeleteDevice not called
	if len(master.ifaceCalls) != 1 {
		t.Errorf("Master.DeleteForInterface calls = %d, want 1", len(master.ifaceCalls))
	}
	if len(master.deviceCalls) != 0 {
		t.Errorf("Master.DeleteDevice should not be called")
	}

	// Report counts
	if rep.Devices != 3 {
		t.Errorf("Report.Devices = %d, want 3", rep.Devices)
	}
	if rep.Paramsets != 2 {
		t.Errorf("Report.Paramsets = %d, want 2", rep.Paramsets)
	}
	if rep.Values != 5 {
		t.Errorf("Report.Values = %d, want 5", rep.Values)
	}
	if rep.Master != 4 {
		t.Errorf("Report.Master = %d, want 4", rep.Master)
	}

	// ClearValueCache called once for "ccu"
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if len(cacheCleared) != 1 || cacheCleared[0] != "ccu" {
		t.Errorf("ClearValueCache calls = %v, want [ccu]", cacheCleared)
	}

	// Audit called once
	if auditCalls != 1 {
		t.Errorf("Audit calls = %d, want 1", auditCalls)
	}

	// Reiniter called once for "ccu"
	if len(reiniter.calls) != 1 || reiniter.calls[0].central != "ccu" {
		t.Errorf("Reiniter calls = %v, want [{ccu}]", reiniter.calls)
	}
	if len(rep.CentralsReinit) != 1 || rep.CentralsReinit[0] != "ccu" {
		t.Errorf("Report.CentralsReinit = %v, want [ccu]", rep.CentralsReinit)
	}
}

func TestClearDeviceScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	devs := &fakeDevices{deleteN: 1}
	params := &fakeParamsets{deleteN: 5}
	vals := &fakeValues{}
	master := &fakeMaster{}
	reiniter := &fakeReiniter{ok: true}

	svc := New(Deps{
		Devices:   devs,
		Paramsets: params,
		Values:    vals,
		Master:    master,
		Reiniter:  reiniter,
	})

	scope := Scope{Kind: ScopeDevice, Central: "ccu", Interface: "HmIP-RF", Device: "ABC:1"}
	rep, err := svc.Clear(ctx, scope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Devices.Delete called, Clear not called
	if len(devs.deleteCalls) != 1 || devs.deleteCalls[0] != (deleteCall{"ccu", "HmIP-RF", "ABC:1"}) {
		t.Errorf("Devices.Delete calls = %v", devs.deleteCalls)
	}
	if len(devs.clearCalls) != 0 {
		t.Errorf("Devices.Clear should not be called")
	}

	// Paramsets.DeleteDevice called, ClearForInterface not called
	if len(params.deleteCalls) != 1 || params.deleteCalls[0] != (paramsetDeleteCall{"ccu", "HmIP-RF", "ABC:1"}) {
		t.Errorf("Paramsets.DeleteDevice calls = %v", params.deleteCalls)
	}
	if len(params.clearCalls) != 0 {
		t.Errorf("Paramsets.ClearForInterface should not be called")
	}

	// Values.DeleteDevice called, DeleteForInterface not called
	if len(vals.deviceCalls) != 1 {
		t.Errorf("Values.DeleteDevice calls = %d, want 1", len(vals.deviceCalls))
	}
	if len(vals.ifaceCalls) != 0 {
		t.Errorf("Values.DeleteForInterface should not be called")
	}

	// Master.DeleteDevice called, DeleteForInterface not called
	if len(master.deviceCalls) != 1 {
		t.Errorf("Master.DeleteDevice calls = %d, want 1", len(master.deviceCalls))
	}
	if len(master.ifaceCalls) != 0 {
		t.Errorf("Master.DeleteForInterface should not be called")
	}

	// Values and Master report -1 for device scope
	if rep.Values != -1 {
		t.Errorf("Report.Values = %d, want -1", rep.Values)
	}
	if rep.Master != -1 {
		t.Errorf("Report.Master = %d, want -1", rep.Master)
	}

	if rep.Devices != 1 {
		t.Errorf("Report.Devices = %d, want 1", rep.Devices)
	}
	if rep.Paramsets != 5 {
		t.Errorf("Report.Paramsets = %d, want 5", rep.Paramsets)
	}
}

func TestClearCentralScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	devs := &fakeDevices{}
	params := &fakeParamsets{}
	vals := &fakeValues{}
	master := &fakeMaster{}
	topo := &fakeTopology{
		interfaces: map[string][]string{
			"ccu": {"HmIP-RF", "BidCos-RF"},
		},
	}
	reiniter := &fakeReiniter{ok: true}

	var cacheMu sync.Mutex
	var cacheCleared []string

	svc := New(Deps{
		Devices:   devs,
		Paramsets: params,
		Values:    vals,
		Master:    master,
		Topology:  topo,
		Reiniter:  reiniter,
		ClearValueCache: func(c string) {
			cacheMu.Lock()
			cacheCleared = append(cacheCleared, c)
			cacheMu.Unlock()
		},
	})

	_, err := svc.Clear(ctx, Scope{Kind: ScopeCentral, Central: "ccu"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(devs.clearCalls) != 2 {
		t.Errorf("Devices.Clear calls = %d, want 2", len(devs.clearCalls))
	}
	if len(params.clearCalls) != 2 {
		t.Errorf("Paramsets.ClearForInterface calls = %d, want 2", len(params.clearCalls))
	}
	if len(vals.ifaceCalls) != 2 {
		t.Errorf("Values.DeleteForInterface calls = %d, want 2", len(vals.ifaceCalls))
	}
	if len(master.ifaceCalls) != 2 {
		t.Errorf("Master.DeleteForInterface calls = %d, want 2", len(master.ifaceCalls))
	}

	cacheMu.Lock()
	defer cacheMu.Unlock()
	if len(cacheCleared) != 1 || cacheCleared[0] != "ccu" {
		t.Errorf("ClearValueCache calls = %v, want [ccu] (deduped)", cacheCleared)
	}

	if len(reiniter.calls) != 1 || reiniter.calls[0].central != "ccu" {
		t.Errorf("Reiniter calls = %v, want [{ccu}]", reiniter.calls)
	}
}

func TestClearGlobalScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	devs := &fakeDevices{}
	reiniter := &fakeReiniter{ok: true}
	topo := &fakeTopology{
		centrals: []string{"ccu1", "ccu2"},
		interfaces: map[string][]string{
			"ccu1": {"HmIP-RF"},
			"ccu2": {"BidCos-RF"},
		},
	}

	var cacheMu sync.Mutex
	var cacheCleared []string

	svc := New(Deps{
		Devices:  devs,
		Topology: topo,
		Reiniter: reiniter,
		ClearValueCache: func(c string) {
			cacheMu.Lock()
			cacheCleared = append(cacheCleared, c)
			cacheMu.Unlock()
		},
	})

	rep, err := svc.Clear(ctx, Scope{Kind: ScopeGlobal})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(devs.clearCalls) != 2 {
		t.Errorf("Devices.Clear calls = %d, want 2", len(devs.clearCalls))
	}

	cacheMu.Lock()
	defer cacheMu.Unlock()
	if len(cacheCleared) != 2 || cacheCleared[0] != "ccu1" || cacheCleared[1] != "ccu2" {
		t.Errorf("ClearValueCache calls = %v, want [ccu1 ccu2]", cacheCleared)
	}

	if len(reiniter.calls) != 2 {
		t.Errorf("Reiniter calls = %d, want 2", len(reiniter.calls))
	}

	want := []string{"ccu1", "ccu2"}
	if len(rep.CentralsReinit) != len(want) {
		t.Fatalf("CentralsReinit = %v, want %v", rep.CentralsReinit, want)
	}
	for i, c := range want {
		if rep.CentralsReinit[i] != c {
			t.Errorf("CentralsReinit[%d] = %q, want %q", i, rep.CentralsReinit[i], c)
		}
	}
}

func TestClearErrorAccumulation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	devs := &fakeDevices{clearErr: errors.New("disk full")}
	params := &fakeParamsets{}
	vals := &fakeValues{}
	master := &fakeMaster{}
	reiniter := &fakeReiniter{ok: true}
	topo := &fakeTopology{
		interfaces: map[string][]string{"ccu": {"HmIP-RF"}},
	}

	svc := New(Deps{
		Devices:   devs,
		Paramsets: params,
		Values:    vals,
		Master:    master,
		Topology:  topo,
		Reiniter:  reiniter,
	})

	rep, err := svc.Clear(ctx, Scope{Kind: ScopeInterface, Central: "ccu", Interface: "HmIP-RF"})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(rep.Errors) != 1 {
		t.Errorf("Report.Errors = %d entries, want 1", len(rep.Errors))
	}

	// Other stores still called
	if len(params.clearCalls) != 1 {
		t.Errorf("Paramsets.ClearForInterface calls = %d, want 1 (continues on error)", len(params.clearCalls))
	}
	if len(vals.ifaceCalls) != 1 {
		t.Errorf("Values.DeleteForInterface calls = %d, want 1", len(vals.ifaceCalls))
	}
	if len(master.ifaceCalls) != 1 {
		t.Errorf("Master.DeleteForInterface calls = %d, want 1", len(master.ifaceCalls))
	}

	// Reiniter still runs
	if len(reiniter.calls) != 1 {
		t.Errorf("Reiniter calls = %d, want 1 (reinit runs despite store errors)", len(reiniter.calls))
	}
}

func TestClearNilDeps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	svc := New(Deps{})
	rep, err := svc.Clear(ctx, Scope{Kind: ScopeGlobal})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.Devices != 0 || rep.Paramsets != 0 || rep.Values != 0 || rep.Master != 0 {
		t.Errorf("expected zeroed report, got %+v", rep)
	}
	if rep.CentralsReinit != nil {
		t.Errorf("CentralsReinit should be nil, got %v", rep.CentralsReinit)
	}
}

func TestClearInvalidScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	devs := &fakeDevices{}
	reiniter := &fakeReiniter{}

	svc := New(Deps{
		Devices:  devs,
		Reiniter: reiniter,
	})

	_, err := svc.Clear(ctx, Scope{Kind: "unknown"})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}

	if len(devs.clearCalls) != 0 || len(devs.deleteCalls) != 0 {
		t.Error("no store method should be called for invalid scope")
	}
	if len(reiniter.calls) != 0 {
		t.Error("reiniter should not be called for invalid scope")
	}
}
