// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

type fakeHubIndex struct{ h *hub.Hub }

func (f *fakeHubIndex) Hub() *hub.Hub { return f.h }

func (f *fakeHubIndex) Hubs() []handlers.NamedHub {
	if f.h == nil {
		return nil
	}
	return []handlers.NamedHub{{Central: "test-ccu", Hub: f.h}}
}

func (f *fakeHubIndex) HubFor(centralName string) *hub.Hub {
	if centralName == "test-ccu" {
		return f.h
	}
	return nil
}

type fakeProgramWriter struct {
	calls atomic.Int32
	id    atomic.Value
}

func (f *fakeProgramWriter) ExecuteProgram(_ context.Context, id string) error {
	f.calls.Add(1)
	f.id.Store(id)
	return nil
}

func (f *fakeProgramWriter) SetProgramEnabled(_ context.Context, _ string, _ bool) error {
	return nil
}

type fakeSysvarWriter struct {
	calls atomic.Int32
	last  struct {
		name string
		val  any
	}
}

func (f *fakeSysvarWriter) SetSysvar(_ context.Context, name string, v any) error {
	f.calls.Add(1)
	f.last.name = name
	f.last.val = v
	return nil
}

type fakeInstallMode struct {
	active    atomic.Bool
	remaining atomic.Int64
	writeErr  error
	lastDur   atomic.Int64
}

func (f *fakeInstallMode) InstallModeState() (bool, time.Duration) {
	return f.active.Load(), time.Duration(f.remaining.Load())
}

func (f *fakeInstallMode) SetInstallMode(_ context.Context, on bool, dur time.Duration) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.active.Store(on)
	f.lastDur.Store(int64(dur))
	if on {
		f.remaining.Store(int64(dur))
	} else {
		f.remaining.Store(0)
	}
	return nil
}

type fakeInterfaceIndex struct {
	states     map[string]handlers.InterfaceState
	reconnects atomic.Int32
}

func (f *fakeInterfaceIndex) Interfaces() []handlers.InterfaceState {
	out := make([]handlers.InterfaceState, 0, len(f.states))
	for _, s := range f.states {
		out = append(out, s)
	}
	return out
}

func (f *fakeInterfaceIndex) Interface(id string) (handlers.InterfaceState, bool) {
	s, ok := f.states[id]
	return s, ok
}

func (f *fakeInterfaceIndex) Reconnect(_ context.Context, _ string) error {
	f.reconnects.Add(1)
	return nil
}

type hubHarness struct {
	handler  http.Handler
	hub      *hub.Hub
	programs *fakeProgramWriter
	sysvars  *fakeSysvarWriter
	install  *fakeInstallMode
	ifaces   *fakeInterfaceIndex
}

func newHubRouter(t *testing.T) *hubHarness {
	t.Helper()
	h := hub.NewHub("ccu-01")
	pw := &fakeProgramWriter{}
	sw := &fakeSysvarWriter{}
	h.PutProgram(&hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Morning"}, ID: "P1", Writer: pw})
	h.PutSysvar(&hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "PartyMode"}, ValueType: hmenum.HubValueTypeLogic, Writer: sw})

	im := &fakeInstallMode{}
	iface := &fakeInterfaceIndex{states: map[string]handlers.InterfaceState{
		"HmIP-RF": {ID: "HmIP-RF", Name: "HmIP radio", Connected: true, Interface: "HmIP-RF"},
	}}
	r := NewRouter(Deps{
		Hub:         &fakeHubIndex{h: h},
		InstallMode: im,
		Interfaces:  iface,
	})
	return &hubHarness{handler: r, hub: h, programs: pw, sysvars: sw, install: im, ifaces: iface}
}

func TestListPrograms(t *testing.T) {
	h := newHubRouter(t)
	r := h.handler
	req := httptest.NewRequest(http.MethodGet, "/api/v1/programs", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var body []handlers.ProgramSummary
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body) != 1 || body[0].ID != "P1" {
		t.Fatalf("body=%+v", body)
	}
}

func TestExecuteProgram(t *testing.T) {
	h := newHubRouter(t)
	r := h.handler
	pw := h.programs
	req := httptest.NewRequest(http.MethodPost, "/api/v1/programs/P1/execute", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if pw.calls.Load() != 1 {
		t.Fatalf("calls=%d", pw.calls.Load())
	}
}

func TestExecuteProgramNotFound(t *testing.T) {
	h := newHubRouter(t)
	r := h.handler
	req := httptest.NewRequest(http.MethodPost, "/api/v1/programs/missing/execute", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != 404 {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestPutSysvar(t *testing.T) {
	h := newHubRouter(t)
	r := h.handler
	sw := h.sysvars
	req := httptest.NewRequest(http.MethodPut, "/api/v1/sysvars/PartyMode",
		strings.NewReader(`{"value": true}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if sw.calls.Load() != 1 {
		t.Fatalf("writer calls=%d", sw.calls.Load())
	}
	if sw.last.name != "PartyMode" || sw.last.val != true {
		t.Fatalf("last=%+v", sw.last)
	}
}

func TestGetSysvarReturnsObservedValue(t *testing.T) {
	harness := newHubRouter(t)
	r := harness.handler
	h := harness.hub
	sv, _ := h.Sysvar("PartyMode")
	sv.OnValue(hmtypes.BoolValue(true))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sysvars/PartyMode", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d", rr.Code)
	}
	var sum handlers.SysvarSummary
	_ = json.Unmarshal(rr.Body.Bytes(), &sum)
	if !sum.Observed || sum.Value != true {
		t.Fatalf("sum=%+v", sum)
	}
}

func TestGetInstallMode(t *testing.T) {
	h := newHubRouter(t)
	r := h.handler
	im := h.install
	im.active.Store(true)
	im.remaining.Store(int64(42 * time.Second))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/install-mode", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d", rr.Code)
	}
	var body handlers.InstallModeState
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if !body.Active || body.Seconds != 42 {
		t.Fatalf("body=%+v", body)
	}
}

func TestPostInstallMode(t *testing.T) {
	h := newHubRouter(t)
	r := h.handler
	im := h.install
	req := httptest.NewRequest(http.MethodPost, "/api/v1/install-mode",
		strings.NewReader(`{"active":true,"seconds":60}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d", rr.Code)
	}
	if !im.active.Load() || im.lastDur.Load() != int64(60*time.Second) {
		t.Fatalf("state=%v dur=%v", im.active.Load(), time.Duration(im.lastDur.Load()))
	}
}

func TestPostInstallModeErrorMaps502(t *testing.T) {
	im := &fakeInstallMode{writeErr: errors.New("boom")}
	r := NewRouter(Deps{InstallMode: im})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/install-mode", strings.NewReader(`{"active":true}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestListInterfaces(t *testing.T) {
	h := newHubRouter(t)
	r := h.handler
	iface := h.ifaces
	_ = iface
	req := httptest.NewRequest(http.MethodGet, "/api/v1/interfaces", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestReconnectInterface(t *testing.T) {
	h := newHubRouter(t)
	r := h.handler
	iface := h.ifaces
	req := httptest.NewRequest(http.MethodPost, "/api/v1/interfaces/HmIP-RF/reconnect", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d", rr.Code)
	}
	if iface.reconnects.Load() != 1 {
		t.Fatalf("reconnects=%d", iface.reconnects.Load())
	}
}
