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
)

// stubParameterDeterminer is an inline stub for ParameterDeterminer that
// records the arguments the handler forwards.
type stubParameterDeterminer struct {
	result any
	err    error

	calls          int
	gotInterfaceID string
	gotChannel     string
	gotParameter   string
}

func (s *stubParameterDeterminer) DetermineParameter(_ context.Context, interfaceID, channelAddress, parameterID string) (any, error) {
	s.calls++
	s.gotInterfaceID = interfaceID
	s.gotChannel = channelAddress
	s.gotParameter = parameterID
	return s.result, s.err
}

func determineRequestFor(t *testing.T, addr, no, key, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/devices/"+addr+"/channels/"+no+"/paramsets/"+key+"/determine",
		strings.NewReader(body))
	return req.WithContext(chiContext(req, map[string]string{"addr": addr, "no": no, "key": key}))
}

func TestDetermineParameter_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubParameterDeterminer{result: 21.5}
	req := determineRequestFor(t, "DEV001", "1", "MASTER", `{"parameter":"TEMPERATURE"}`)
	w := httptest.NewRecorder()
	DetermineParameter(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Value any `json:"value"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Value != 21.5 {
		t.Fatalf("value = %v; want 21.5", body.Value)
	}
	if svc.calls != 1 {
		t.Fatalf("determine calls = %d; want 1", svc.calls)
	}
	// The handler composes the channel address from addr:no and forwards an
	// empty interfaceID (resolved from the registry by the implementation).
	if svc.gotChannel != "DEV001:1" {
		t.Fatalf("channel = %q; want DEV001:1", svc.gotChannel)
	}
	if svc.gotParameter != "TEMPERATURE" {
		t.Fatalf("parameter = %q; want TEMPERATURE", svc.gotParameter)
	}
	if svc.gotInterfaceID != "" {
		t.Fatalf("interfaceID = %q; want empty", svc.gotInterfaceID)
	}
}

func TestDetermineParameter_MissingParameter(t *testing.T) {
	t.Parallel()
	svc := &stubParameterDeterminer{result: 1}
	req := determineRequestFor(t, "DEV001", "1", "MASTER", `{}`)
	w := httptest.NewRecorder()
	DetermineParameter(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 0 {
		t.Fatalf("backend must not be reached on a bad request; calls = %d", svc.calls)
	}
}

func TestDetermineParameter_InvalidKey(t *testing.T) {
	t.Parallel()
	svc := &stubParameterDeterminer{result: 1}
	req := determineRequestFor(t, "DEV001", "1", "BOGUS", `{"parameter":"TEMPERATURE"}`)
	w := httptest.NewRecorder()
	DetermineParameter(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 0 {
		t.Fatalf("backend must not be reached on an invalid key; calls = %d", svc.calls)
	}
}

func TestDetermineParameter_UpstreamError(t *testing.T) {
	t.Parallel()
	svc := &stubParameterDeterminer{err: errors.New("ccu unreachable")}
	req := determineRequestFor(t, "DEV001", "1", "MASTER", `{"parameter":"TEMPERATURE"}`)
	w := httptest.NewRecorder()
	DetermineParameter(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDetermineParameter_NilService(t *testing.T) {
	t.Parallel()
	req := determineRequestFor(t, "DEV001", "1", "MASTER", `{"parameter":"TEMPERATURE"}`)
	w := httptest.NewRecorder()
	DetermineParameter(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDetermineParameter_MalformedJSON(t *testing.T) {
	t.Parallel()
	svc := &stubParameterDeterminer{result: 1}
	req := determineRequestFor(t, "DEV001", "1", "MASTER", `{"parameter":`)
	w := httptest.NewRecorder()
	DetermineParameter(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 0 {
		t.Fatalf("backend must not be reached on malformed JSON; calls = %d", svc.calls)
	}
}

func TestDetermineParameter_EmptyChannelAddress(t *testing.T) {
	t.Parallel()
	svc := &stubParameterDeterminer{result: 1}
	// addr present but no (channel number) missing — the handler validates
	// both path segments before ever touching the backend.
	req := determineRequestFor(t, "DEV001", "", "MASTER", `{"parameter":"TEMPERATURE"}`)
	w := httptest.NewRecorder()
	DetermineParameter(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 0 {
		t.Fatalf("backend must not be reached with an empty channel number; calls = %d", svc.calls)
	}
}
