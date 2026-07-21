// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

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

func (f *fakeHubIndex) SerialSuffix(central string) string {
	if central != "" {
		return "vccu0000000"
	}
	return ""
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
	ifaces   *fakeInterfaceIndex
}

func newHubRouter(t *testing.T) *hubHarness {
	t.Helper()
	h := hub.NewHub("ccu-01")
	pw := &fakeProgramWriter{}
	sw := &fakeSysvarWriter{}
	h.PutProgram(&hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Morning"}, ID: "P1", Writer: pw})
	h.PutSysvar(&hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "PartyMode"}, ValueType: hmenum.HubValueTypeLogic, Writer: sw})

	iface := &fakeInterfaceIndex{states: map[string]handlers.InterfaceState{
		"HmIP-RF": {ID: "HmIP-RF", Name: "HmIP radio", Connected: true, Interface: "HmIP-RF"},
	}}
	r := NewRouter(Deps{
		Hub:        &fakeHubIndex{h: h},
		Interfaces: iface,
	})
	return &hubHarness{handler: r, hub: h, programs: pw, sysvars: sw, ifaces: iface}
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

// fakeBulkAck implements hub.BulkMessageAcknowledger with a fixed count for
// both message classes, used by the router-precedence tests below.
type fakeBulkAck struct{ n int }

func (f fakeBulkAck) AcknowledgeAllServiceMessages(context.Context) (int, error) { return f.n, nil }
func (f fakeBulkAck) AcknowledgeAllAlarmMessages(context.Context) (int, error)   { return f.n, nil }

// TestAckAllAlarmMessagesRouteTakesPrecedenceOverSingleAckWildcard pins the
// router's static-vs-wildcard precedence: `POST /alarm-messages/ack-all`
// must dispatch to the bulk handler, not be swallowed by the
// `/alarm-messages/{id}/ack` single-message route with id="ack-all".
// Routing this to the wrong handler would 404 (no further "/ack" segment)
// or silently try to acknowledge a message literally named "ack-all".
func TestAckAllAlarmMessagesRouteTakesPrecedenceOverSingleAckWildcard(t *testing.T) {
	harness := newHubRouter(t)
	harness.hub.Messages.SetAcknowledgers(nil, fakeBulkAck{n: 2})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm-messages/ack-all", http.NoBody)
	rr := httptest.NewRecorder()
	harness.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got handlers.AckAllResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Acknowledged != 2 {
		t.Fatalf("acknowledged=%d, want 2", got.Acknowledged)
	}
}

// TestAckAllServiceMessagesRouteTakesPrecedenceOverSingleAckWildcard mirrors
// the alarm-side routing-precedence pin for the service-messages endpoint.
func TestAckAllServiceMessagesRouteTakesPrecedenceOverSingleAckWildcard(t *testing.T) {
	harness := newHubRouter(t)
	harness.hub.ServiceMessages.SetAcknowledgers(nil, fakeBulkAck{n: 5})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/service-messages/ack-all", http.NoBody)
	rr := httptest.NewRecorder()
	harness.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got handlers.AckAllResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Acknowledged != 5 {
		t.Fatalf("acknowledged=%d, want 5", got.Acknowledged)
	}
}
