// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

type stubDeviceTeam struct {
	candidates []hmapi.TeamCandidate
	candErr    error
	setErr     error
	lastAddr   string
	lastNo     int
	lastTeam   string
}

func (s *stubDeviceTeam) TeamCandidates(_ context.Context, addr string, no int) ([]hmapi.TeamCandidate, error) {
	s.lastAddr, s.lastNo = addr, no
	return s.candidates, s.candErr
}

func (s *stubDeviceTeam) SetChannelTeam(_ context.Context, addr string, no int, team string) error {
	s.lastAddr, s.lastNo, s.lastTeam = addr, no, team
	return s.setErr
}

func teamReq(method string, params map[string]string, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "/", http.NoBody)
	} else {
		r = httptest.NewRequest(method, "/", strings.NewReader(body))
	}
	return r.WithContext(chiContext(r, params))
}

func TestGetDeviceTeamCandidates_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceTeam{candidates: []hmapi.TeamCandidate{{Address: "TEAM:1", Current: true}}}
	w := httptest.NewRecorder()
	GetDeviceTeamCandidates(svc).ServeHTTP(w, teamReq(http.MethodGet, map[string]string{"addr": "SD001", "no": "1"}, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "TEAM:1") {
		t.Errorf("body missing candidate: %s", w.Body.String())
	}
	if svc.lastNo != 1 {
		t.Errorf("channel not threaded: %d", svc.lastNo)
	}
}

func TestGetDeviceTeamCandidates_NilNormalizedToEmpty(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceTeam{candidates: nil}
	w := httptest.NewRecorder()
	GetDeviceTeamCandidates(svc).ServeHTTP(w, teamReq(http.MethodGet, map[string]string{"addr": "SD001", "no": "1"}, ""))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "[]") {
		t.Fatalf("expected 200 with empty array, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetDeviceTeamCandidates_Unsupported422(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceTeam{candErr: backends.ErrUnsupported}
	w := httptest.NewRecorder()
	GetDeviceTeamCandidates(svc).ServeHTTP(w, teamReq(http.MethodGet, map[string]string{"addr": "SD001", "no": "1"}, ""))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}
}

func TestGetDeviceTeamCandidates_NilServed503(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	GetDeviceTeamCandidates(nil).ServeHTTP(w, teamReq(http.MethodGet, map[string]string{"addr": "SD001", "no": "1"}, ""))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGetDeviceTeamCandidates_BadChannel400(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceTeam{}
	w := httptest.NewRecorder()
	GetDeviceTeamCandidates(svc).ServeHTTP(w, teamReq(http.MethodGet, map[string]string{"addr": "SD001", "no": "x"}, ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSetDeviceChannelTeam_HappyPathRecordsAudit(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceTeam{}
	rec := &captureRecorder{}
	w := httptest.NewRecorder()
	SetDeviceChannelTeam(svc, rec).ServeHTTP(w, teamReq(http.MethodPut, map[string]string{"addr": "SD001", "no": "1"}, `{"team":"TEAM:2"}`))
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastTeam != "TEAM:2" {
		t.Errorf("team not threaded: %q", svc.lastTeam)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionDeviceTeamSet {
		t.Fatalf("expected one device_team_set audit entry, got %+v", rec.entries)
	}
	if rec.entries[0].DeviceAddress != "SD001:1" {
		t.Errorf("audit device_address = %q, want SD001:1", rec.entries[0].DeviceAddress)
	}
}

func TestSetDeviceChannelTeam_ResetWithNullTeam(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceTeam{}
	w := httptest.NewRecorder()
	SetDeviceChannelTeam(svc, &captureRecorder{}).ServeHTTP(w, teamReq(http.MethodPut, map[string]string{"addr": "SD001", "no": "1"}, `{"team":null}`))
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}
	if svc.lastTeam != "" {
		t.Errorf("null team must reset to empty, got %q", svc.lastTeam)
	}
}

func TestSetDeviceChannelTeam_Unsupported422(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceTeam{setErr: backends.ErrUnsupported}
	w := httptest.NewRecorder()
	SetDeviceChannelTeam(svc, &captureRecorder{}).ServeHTTP(w, teamReq(http.MethodPut, map[string]string{"addr": "SD001", "no": "1"}, `{"team":"TEAM:2"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}
}

func TestSetDeviceChannelTeam_UpstreamError502(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceTeam{setErr: errors.New("ccu offline")}
	w := httptest.NewRecorder()
	SetDeviceChannelTeam(svc, &captureRecorder{}).ServeHTTP(w, teamReq(http.MethodPut, map[string]string{"addr": "SD001", "no": "1"}, `{"team":"TEAM:2"}`))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestSetDeviceChannelTeam_NilServed503(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	SetDeviceChannelTeam(nil, nil).ServeHTTP(w, teamReq(http.MethodPut, map[string]string{"addr": "SD001", "no": "1"}, `{"team":"TEAM:2"}`))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}
