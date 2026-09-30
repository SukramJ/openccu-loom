// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

type stubRSSIMatrix struct {
	centrals   []interfaces.RSSIMatrixCentral
	proposals  []interfaces.ReceiverProposal
	err        error
	lastMargin int
	calls      int
}

func (s *stubRSSIMatrix) RSSIMatrix(context.Context) ([]interfaces.RSSIMatrixCentral, error) {
	s.calls++
	return s.centrals, s.err
}

func (s *stubRSSIMatrix) ReceiverProposal(_ context.Context, margin int) ([]interfaces.ReceiverProposal, error) {
	s.calls++
	s.lastMargin = margin
	return s.proposals, s.err
}

func hp(v int) *int { return &v }

// TestDiagnosticsRSSIMatrix_Shape verifies the wire body: items per
// central, snake_case keys, and a missing reading as an explicit null.
func TestDiagnosticsRSSIMatrix_Shape(t *testing.T) {
	t.Parallel()
	svc := &stubRSSIMatrix{centrals: []interfaces.RSSIMatrixCentral{{
		Central:     "ccu-01",
		InterfaceID: "ccu-01-BidCos-RF",
		Interfaces:  []interfaces.RSSIMatrixInterface{{Address: "GW1", Description: "CCU", Connected: true, Default: true, DutyCycle: 4}},
		Devices: []interfaces.RSSIMatrixDevice{{
			Address: "DEV", Name: "Flur",
			Partners: []interfaces.RSSIMatrixPartner{{Address: "GW1", RxDBm: hp(-60), TxDBm: nil}},
		}},
	}, {
		Central:     "ccu-02",
		InterfaceID: "ccu-02-BidCos-RF",
		Interfaces:  []interfaces.RSSIMatrixInterface{},
		Devices:     []interfaces.RSSIMatrixDevice{},
		Error:       "read matrix: unreachable",
	}}}
	w := httptest.NewRecorder()
	DiagnosticsRSSIMatrix(svc).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body map[string][]map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	items := body["items"]
	if len(items) != 2 || items[0]["central"] != "ccu-01" || items[0]["interface_id"] != "ccu-01-BidCos-RF" {
		t.Fatalf("items = %v", items)
	}
	if _, ok := items[0]["error"]; ok {
		t.Errorf("a healthy central must omit error: %v", items[0])
	}
	if items[1]["error"] != "read matrix: unreachable" || len(items[1]["devices"].([]any)) != 0 ||
		len(items[1]["interfaces"].([]any)) != 0 {
		t.Errorf("faulty central = %v", items[1])
	}
	gw := items[0]["interfaces"].([]any)[0].(map[string]any)
	if gw["address"] != "GW1" || gw["duty_cycle"] != float64(4) || gw["default"] != true || gw["connected"] != true {
		t.Errorf("interface = %v", gw)
	}
	dev := items[0]["devices"].([]any)[0].(map[string]any)
	partner := dev["partners"].([]any)[0].(map[string]any)
	if dev["name"] != "Flur" || partner["rx_dbm"] != float64(-60) {
		t.Errorf("device = %v", dev)
	}
	if v, ok := partner["tx_dbm"]; !ok || v != nil {
		t.Errorf("tx_dbm must be an explicit null, got %v (present=%v)", v, ok)
	}
}

// TestDiagnosticsRSSIMatrix_ErrorsAndUnwired verifies an empty list is
// items:[], a service fault 502 and a nil service 503.
func TestDiagnosticsRSSIMatrix_ErrorsAndUnwired(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	DiagnosticsRSSIMatrix(&stubRSSIMatrix{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Errorf("empty: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	DiagnosticsRSSIMatrix(&stubRSSIMatrix{err: errors.New("x")}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if w.Code != http.StatusBadGateway {
		t.Errorf("fault: status = %d", w.Code)
	}
	w = httptest.NewRecorder()
	DiagnosticsRSSIMatrix(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("unwired: status = %d", w.Code)
	}
}

// TestReceiverProposalHandler_ShapeAndDefaultMargin verifies the default
// margin 6 reaches the service when the parameter is absent, and the wire
// keys of one verdict.
func TestReceiverProposalHandler_ShapeAndDefaultMargin(t *testing.T) {
	t.Parallel()
	svc := &stubRSSIMatrix{proposals: []interfaces.ReceiverProposal{{
		Address: "DEV", Name: "Flur", Central: "ccu-01", CurrentInterface: "GW1", BestInterface: "GW2",
		CurrentRxDBm: nil, BestRxDBm: hp(-70), Verdict: interfaces.ReceiverSwitch,
	}}}
	w := httptest.NewRecorder()
	ReceiverProposalHandler(svc).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastMargin != 6 {
		t.Errorf("margin = %d, want default 6", svc.lastMargin)
	}
	var body map[string][]map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	p := body["items"][0]
	if p["verdict"] != "switch" || p["current_interface"] != "GW1" || p["best_interface"] != "GW2" ||
		p["best_rx_dbm"] != float64(-70) || p["roaming"] != false || p["central"] != "ccu-01" {
		t.Errorf("proposal = %v", p)
	}
	if v, ok := p["current_rx_dbm"]; !ok || v != nil {
		t.Errorf("current_rx_dbm must be an explicit null, got %v (present=%v)", v, ok)
	}
}

// TestReceiverProposalHandler_MarginValidation verifies 0 and 30 pass
// through while negatives, values above 30 and non-integers answer 400
// without reaching the service.
func TestReceiverProposalHandler_MarginValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		q    string
		code int
		want int
	}{
		{"0", http.StatusOK, 0},
		{"30", http.StatusOK, 30},
		{"-1", http.StatusBadRequest, 0},
		{"31", http.StatusBadRequest, 0},
		{"abc", http.StatusBadRequest, 0},
	} {
		svc := &stubRSSIMatrix{}
		w := httptest.NewRecorder()
		ReceiverProposalHandler(svc).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?margin_db="+tc.q, http.NoBody))
		if w.Code != tc.code {
			t.Errorf("margin_db=%s: status = %d, want %d", tc.q, w.Code, tc.code)
		}
		if tc.code == http.StatusOK && svc.lastMargin != tc.want {
			t.Errorf("margin_db=%s: service saw %d", tc.q, svc.lastMargin)
		}
		if tc.code != http.StatusOK && svc.calls != 0 {
			t.Errorf("margin_db=%s: service must not be called", tc.q)
		}
	}
}

type stubRFAssign struct {
	err             error
	addr, iface     string
	roaming, called bool
}

func (s *stubRFAssign) AssignRFInterface(_ context.Context, addr, iface string, roaming bool) error {
	s.called, s.addr, s.iface, s.roaming = true, addr, iface, roaming
	return s.err
}

func postRFAssign(t *testing.T, svc DeviceRFInterfacePort, rec audit.Recorder, addr, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req = req.WithContext(chiContext(req, map[string]string{"addr": addr}))
	w := httptest.NewRecorder()
	AssignRFInterface(svc, rec).ServeHTTP(w, req)
	return w
}

// TestAssignRFInterface_HappyPath_Returns204AndRecordsAudit verifies the
// body reaches the service field for field and one audit row is written.
func TestAssignRFInterface_HappyPath_Returns204AndRecordsAudit(t *testing.T) {
	t.Parallel()
	svc := &stubRFAssign{}
	rec := &captureRecorder{}
	w := postRFAssign(t, svc, rec, "DEV", `{"interface_address":"GW2","roaming":true}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if svc.addr != "DEV" || svc.iface != "GW2" || !svc.roaming {
		t.Errorf("service saw %+v", svc)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionDeviceRFInterfaceAssign ||
		rec.entries[0].DeviceAddress != "DEV" || rec.entries[0].Note != "interface GW2, roaming true" {
		t.Errorf("audit = %+v", rec.entries)
	}
}

// TestAssignRFInterface_ErrorMapping verifies 400 on an empty interface,
// a missing roaming flag or malformed JSON (service untouched), 422 for an
// unsupported interface, 502 for an upstream fault and 503 unwired — none
// of them audited.
func TestAssignRFInterface_ErrorMapping(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		err        error
		body       string
		code       int
		wantCalled bool
	}{
		{"empty interface", nil, `{"interface_address":"  ","roaming":false}`, http.StatusBadRequest, false},
		{"missing roaming", nil, `{"interface_address":"GW1"}`, http.StatusBadRequest, false},
		{"malformed", nil, `{`, http.StatusBadRequest, false},
		{"unknown field", nil, `{"interface_address":"GW1","roaming":false,"x":1}`, http.StatusBadRequest, false},
		{"unsupported", backends.ErrUnsupported, `{"interface_address":"GW1","roaming":false}`, http.StatusUnprocessableEntity, true},
		{"upstream", errors.New("fault"), `{"interface_address":"GW1","roaming":false}`, http.StatusBadGateway, true},
	} {
		svc := &stubRFAssign{err: tc.err}
		rec := &captureRecorder{}
		w := postRFAssign(t, svc, rec, "DEV", tc.body)
		if w.Code != tc.code {
			t.Errorf("%s: status = %d, want %d body=%s", tc.name, w.Code, tc.code, w.Body.String())
		}
		if svc.called != tc.wantCalled {
			t.Errorf("%s: called = %v, want %v", tc.name, svc.called, tc.wantCalled)
		}
		if len(rec.entries) != 0 {
			t.Errorf("%s: audited %+v", tc.name, rec.entries)
		}
	}
	if w := postRFAssign(t, nil, nil, "DEV", `{}`); w.Code != http.StatusServiceUnavailable {
		t.Errorf("unwired: status = %d", w.Code)
	}
}
