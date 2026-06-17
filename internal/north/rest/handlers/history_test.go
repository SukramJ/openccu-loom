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

// stubHistoryService is an inline stub for HistoryService. It records the
// last query it received so tests can assert parameter parsing.
type stubHistoryService struct {
	buckets []HistoryBucket
	err     error
	gotQ    HistoryQuery
}

func (s *stubHistoryService) Query(_ context.Context, q HistoryQuery) ([]HistoryBucket, error) {
	s.gotQ = q
	return s.buckets, s.err
}

// validHistoryURL builds a request URL with every required parameter set.
func validHistoryURL() string {
	return "/api/v1/history?central=Home&interface_id=Home-HmIP-RF" +
		"&channel=ABC0000001:4&parameter=ACTUAL_TEMPERATURE" +
		"&from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z"
}

func TestGetHistory_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubHistoryService{buckets: []HistoryBucket{
		{TS: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), Avg: 21.5, Min: 20, Max: 23, Count: 4},
	}}
	req := httptest.NewRequest(http.MethodGet, validHistoryURL()+"&buckets=50", http.NoBody)
	w := httptest.NewRecorder()
	GetHistory(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []HistoryBucket
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].Avg != 21.5 || body[0].Count != 4 {
		t.Fatalf("unexpected body: %+v", body)
	}
	// Parameter parsing landed on the service query.
	if svc.gotQ.Central != "Home" || svc.gotQ.InterfaceID != "Home-HmIP-RF" ||
		svc.gotQ.ChannelAddress != "ABC0000001:4" || svc.gotQ.Parameter != "ACTUAL_TEMPERATURE" ||
		svc.gotQ.Buckets != 50 {
		t.Fatalf("query not parsed as expected: %+v", svc.gotQ)
	}
}

func TestGetHistory_EmptyResultIsJSONArray(t *testing.T) {
	t.Parallel()
	svc := &stubHistoryService{buckets: nil}
	req := httptest.NewRequest(http.MethodGet, validHistoryURL(), http.NoBody)
	w := httptest.NewRecorder()
	GetHistory(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != "[]\n" {
		t.Fatalf("expected empty JSON array, got %q", got)
	}
}

func TestGetHistory_NilServiceIsUnavailable(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, validHistoryURL(), http.NoBody)
	w := httptest.NewRecorder()
	GetHistory(nil).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGetHistory_ServiceErrorIs500(t *testing.T) {
	t.Parallel()
	svc := &stubHistoryService{err: errors.New("boom")}
	req := httptest.NewRequest(http.MethodGet, validHistoryURL(), http.NoBody)
	w := httptest.NewRecorder()
	GetHistory(svc).ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestGetHistory_MissingRequiredParams(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"no central":   "/api/v1/history?interface_id=i&channel=c&parameter=p&from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z",
		"no interface": "/api/v1/history?central=Home&channel=c&parameter=p&from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z",
		"no channel":   "/api/v1/history?central=Home&interface_id=i&parameter=p&from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z",
		"no parameter": "/api/v1/history?central=Home&interface_id=i&channel=c&from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z",
		"no from":      "/api/v1/history?central=Home&interface_id=i&channel=c&parameter=p&to=2026-06-02T00:00:00Z",
		"no to":        "/api/v1/history?central=Home&interface_id=i&channel=c&parameter=p&from=2026-06-01T00:00:00Z",
	}
	for name, url := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := &stubHistoryService{}
			req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
			w := httptest.NewRecorder()
			GetHistory(svc).ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: expected 400, got %d", name, w.Code)
			}
		})
	}
}

func TestGetHistory_BadTimestampsAndRange(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"bad from":       "/api/v1/history?central=Home&interface_id=i&channel=c&parameter=p&from=not-a-time&to=2026-06-02T00:00:00Z",
		"bad to":         "/api/v1/history?central=Home&interface_id=i&channel=c&parameter=p&from=2026-06-01T00:00:00Z&to=not-a-time",
		"to equal from":  "/api/v1/history?central=Home&interface_id=i&channel=c&parameter=p&from=2026-06-01T00:00:00Z&to=2026-06-01T00:00:00Z",
		"to before from": "/api/v1/history?central=Home&interface_id=i&channel=c&parameter=p&from=2026-06-02T00:00:00Z&to=2026-06-01T00:00:00Z",
		"bad buckets":    validHistoryURL() + "&buckets=abc",
	}
	for name, url := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := &stubHistoryService{}
			req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
			w := httptest.NewRecorder()
			GetHistory(svc).ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: expected 400, got %d", name, w.Code)
			}
		})
	}
}

func TestGetHistory_BucketsClamping(t *testing.T) {
	t.Parallel()
	// Above the max clamps down; absent uses the default.
	svc := &stubHistoryService{}
	req := httptest.NewRequest(http.MethodGet, validHistoryURL()+"&buckets=999999", http.NoBody)
	GetHistory(svc).ServeHTTP(httptest.NewRecorder(), req)
	if svc.gotQ.Buckets != historyMaxBuckets {
		t.Fatalf("expected clamp to %d, got %d", historyMaxBuckets, svc.gotQ.Buckets)
	}

	svc2 := &stubHistoryService{}
	req2 := httptest.NewRequest(http.MethodGet, validHistoryURL(), http.NoBody)
	GetHistory(svc2).ServeHTTP(httptest.NewRecorder(), req2)
	if svc2.gotQ.Buckets != historyDefaultBuckets {
		t.Fatalf("expected default %d, got %d", historyDefaultBuckets, svc2.gotQ.Buckets)
	}
}
