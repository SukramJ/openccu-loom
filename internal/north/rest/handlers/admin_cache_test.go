// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/cachereset"
)

// fakeCacheResetService records the scope it was called with and returns a
// preset report/error pair.
type fakeCacheResetService struct {
	gotScope cachereset.Scope
	called   bool
	report   cachereset.Report
	err      error
}

func (f *fakeCacheResetService) Clear(_ context.Context, scope cachereset.Scope) (cachereset.Report, error) {
	f.called = true
	f.gotScope = scope
	return f.report, f.err
}

func postClearCache(t *testing.T, svc CacheResetService, body string) (*httptest.ResponseRecorder, *fakeCacheResetService) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/cache/clear", strings.NewReader(body))
	w := httptest.NewRecorder()
	ClearCache(svc).ServeHTTP(w, req)
	fake, _ := svc.(*fakeCacheResetService)
	return w, fake
}

// TestClearCache_ScopeMapping checks that every request kind maps onto the
// matching cachereset.Scope passed to Clear.
func TestClearCache_ScopeMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want cachereset.Scope
	}{
		{
			name: "global",
			body: `{"kind":"global"}`,
			want: cachereset.Scope{Kind: cachereset.ScopeGlobal},
		},
		{
			name: "central",
			body: `{"kind":"central","central":"ccu1"}`,
			want: cachereset.Scope{Kind: cachereset.ScopeCentral, Central: "ccu1"},
		},
		{
			name: "interface",
			body: `{"kind":"interface","central":"ccu1","interface":"HmIP-RF"}`,
			want: cachereset.Scope{Kind: cachereset.ScopeInterface, Central: "ccu1", Interface: "HmIP-RF"},
		},
		{
			name: "device",
			body: `{"kind":"device","central":"ccu1","interface":"HmIP-RF","device":"ABC123"}`,
			want: cachereset.Scope{Kind: cachereset.ScopeDevice, Central: "ccu1", Interface: "HmIP-RF", Device: "ABC123"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &fakeCacheResetService{report: cachereset.Report{Scope: tc.want}}
			w, fake := postClearCache(t, svc, tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
			}
			if !fake.called {
				t.Fatal("Clear was not called")
			}
			if fake.gotScope != tc.want {
				t.Errorf("scope mismatch: got %+v want %+v", fake.gotScope, tc.want)
			}
		})
	}
}

// TestClearCache_Success_ReturnsReport verifies a 200 carries the report JSON.
func TestClearCache_Success_ReturnsReport(t *testing.T) {
	t.Parallel()
	rep := cachereset.Report{
		Scope:          cachereset.Scope{Kind: cachereset.ScopeGlobal},
		Devices:        3,
		Paramsets:      4,
		Values:         5,
		Master:         6,
		CentralsReinit: []string{"ccu1"},
	}
	svc := &fakeCacheResetService{report: rep}
	w, _ := postClearCache(t, svc, `{"kind":"global"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var got cachereset.Report
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if got.Devices != 3 || got.Paramsets != 4 || got.Values != 5 || got.Master != 6 {
		t.Errorf("report counts mismatch: %+v", got)
	}
	if len(got.CentralsReinit) != 1 || got.CentralsReinit[0] != "ccu1" {
		t.Errorf("centrals_reinit mismatch: %+v", got.CentralsReinit)
	}
}

// TestClearCache_ValidationError_Returns400 verifies a scope that fails
// validation never reaches the service and yields a 400.
func TestClearCache_ValidationError_Returns400(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
	}{
		{name: "unknown kind", body: `{"kind":"bogus"}`},
		{name: "central missing name", body: `{"kind":"central"}`},
		{name: "device missing fields", body: `{"kind":"device","central":"ccu1"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &fakeCacheResetService{}
			w, fake := postClearCache(t, svc, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
			}
			if fake.called {
				t.Error("Clear must not be called for an invalid scope")
			}
		})
	}
}

// TestClearCache_InvalidJSON_Returns400 verifies a malformed body yields a 400.
func TestClearCache_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeCacheResetService{}
	w, fake := postClearCache(t, svc, `{not json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if fake.called {
		t.Error("Clear must not be called for invalid JSON")
	}
}

// TestClearCache_PartialError_ReturnsReportAndNon2xx verifies that a partial
// clear (Clear returns a report AND an error) yields a non-2xx status but the
// report is still in the body.
func TestClearCache_PartialError_ReturnsReportAndNon2xx(t *testing.T) {
	t.Parallel()
	rep := cachereset.Report{
		Scope:   cachereset.Scope{Kind: cachereset.ScopeGlobal},
		Devices: 2,
		Errors:  []string{"values[ccu1/HmIP-RF]: boom"},
	}
	svc := &fakeCacheResetService{report: rep, err: errors.New("cachereset global: values[ccu1/HmIP-RF]: boom")}
	w, _ := postClearCache(t, svc, `{"kind":"global"}`)
	if w.Code/100 == 2 {
		t.Fatalf("expected non-2xx on partial error, got %d", w.Code)
	}
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
	var got cachereset.Report
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if got.Devices != 2 {
		t.Errorf("expected report devices=2 in body, got %+v", got)
	}
	if len(got.Errors) != 1 {
		t.Errorf("expected errors in report body, got %+v", got.Errors)
	}
}

// TestClearCache_NilService_Returns503 verifies a nil service yields 503.
func TestClearCache_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/admin/cache/clear", strings.NewReader(`{"kind":"global"}`))
	w := httptest.NewRecorder()
	ClearCache(nil).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}
