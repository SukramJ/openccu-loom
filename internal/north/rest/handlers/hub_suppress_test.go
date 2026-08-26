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
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// noopSuppressor is a hub.ServiceMessageSuppressor that records calls and
// serves a per-channel suppressed-parameter list. It backs the
// permanent-suppression REST handler tests without a live CCU.
type noopSuppressor struct {
	live map[string][]string
}

func (s noopSuppressor) SuppressServiceMessage(_ context.Context, _, _, _ string, _ bool) error {
	return nil
}

func (s noopSuppressor) GetSuppressedServiceMessages(_ context.Context, _, channel string) ([]string, error) {
	return s.live[channel], nil
}

// TestListSuppressedServiceMessages_NilIndex_ReturnsEmptyArray asserts the
// endpoint answers 200 with [] when no hub is wired.
func TestListSuppressedServiceMessages_NilIndex_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()
	ListSuppressedServiceMessages(nil).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("body=%q, want []", w.Body.String())
	}
}

// TestListSuppressedServiceMessages_AfterDisable lists the message that a
// prior disable suppressed, reconciled against the CCU's live view.
func TestListSuppressedServiceMessages_AfterDisable(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.ServiceMessages = hub.NewServiceMessages(okMessageAcknowledger{})
	h.ServiceMessages.SetSuppressor(noopSuppressor{live: map[string][]string{"ABC:1": {"LOWBAT"}}})
	h.ServiceMessages.Replace([]hub.ServiceMessage{{
		ID: "S1", Address: "ABC:1", Parameter: "LOWBAT", DeviceName: "Sensor",
		InterfaceID: "HmIP-RF", Timestamp: time.Now(),
	}})
	if err := h.ServiceMessages.Disable(context.Background(), "S1"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()
	ListSuppressedServiceMessages(idx).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out []SuppressedServiceMessageDTO
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len=%d, want 1 (%s)", len(out), w.Body.String())
	}
	got := out[0]
	if got.Central != "test-ccu" || got.Channel != "ABC:1" || got.Parameter != "LOWBAT" || got.Interface != "HmIP-RF" {
		t.Errorf("entry=%+v, want central=test-ccu channel=ABC:1 parameter=LOWBAT interface=HmIP-RF", got)
	}
}

// TestUnsuppressServiceMessage_HappyPath clears a suppression by channel +
// parameter and returns 202.
func TestUnsuppressServiceMessage_HappyPath(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.ServiceMessages = hub.NewServiceMessages(okMessageAcknowledger{})
	h.ServiceMessages.SetSuppressor(noopSuppressor{})
	idx := &testHubIndex{h: h}
	body := `{"interface":"HmIP-RF","channel":"ABC:1","parameter":"LOWBAT"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()
	UnsuppressServiceMessage(idx).ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestUnsuppressServiceMessage_MissingChannel rejects a body without a
// channel with 422.
func TestUnsuppressServiceMessage_MissingChannel(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.ServiceMessages = hub.NewServiceMessages(okMessageAcknowledger{})
	h.ServiceMessages.SetSuppressor(noopSuppressor{})
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"parameter":"LOWBAT"}`))
	w := httptest.NewRecorder()
	UnsuppressServiceMessage(idx).ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestUnsuppressServiceMessage_NilIndex_Returns503 asserts the endpoint
// reports service-unready when no hub is wired.
func TestUnsuppressServiceMessage_NilIndex_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"channel":"ABC:1"}`))
	w := httptest.NewRecorder()
	UnsuppressServiceMessage(nil).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// errSuppressor is a hub.ServiceMessageSuppressor whose calls fail on
// demand. Backs the REST handler's CCU-error path tests.
type errSuppressor struct {
	err    error
	getErr error
}

func (s errSuppressor) SuppressServiceMessage(_ context.Context, _, _, _ string, _ bool) error {
	return s.err
}

func (s errSuppressor) GetSuppressedServiceMessages(_ context.Context, _, _ string) ([]string, error) {
	return nil, s.getErr
}

// TestUnsuppressServiceMessage_CCUError_Returns502 asserts that a failing
// CCU call surfaces as a 502 upstream-unavailable problem rather than a
// silently-ignored error.
func TestUnsuppressServiceMessage_CCUError_Returns502(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.ServiceMessages = hub.NewServiceMessages(okMessageAcknowledger{})
	h.ServiceMessages.SetSuppressor(errSuppressor{err: errors.New("ccu unreachable")})
	idx := &testHubIndex{h: h}
	body := `{"interface":"HmIP-RF","channel":"ABC:1","parameter":"LOWBAT"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()
	UnsuppressServiceMessage(idx).ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestUnsuppressServiceMessage_InvalidJSON_Returns400 asserts a malformed
// body is rejected before reaching the domain layer.
func TestUnsuppressServiceMessage_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.ServiceMessages = hub.NewServiceMessages(okMessageAcknowledger{})
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{not-json`))
	w := httptest.NewRecorder()
	UnsuppressServiceMessage(idx).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestListSuppressedServiceMessages_ReadErrorKeepsRecord verifies the REST
// listing surfaces a suppression even when the CCU-side reconcile read
// fails for that channel — the aggregate tolerates the error and keeps the
// record (see [hub.ServiceMessages.Suppressed]).
func TestListSuppressedServiceMessages_ReadErrorKeepsRecord(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.ServiceMessages = hub.NewServiceMessages(okMessageAcknowledger{})
	h.ServiceMessages.SetSuppressor(errSuppressor{getErr: errors.New("rega timeout")})
	h.ServiceMessages.Replace([]hub.ServiceMessage{{
		ID: "S2", Address: "DEF:2", Parameter: "UNREACH", DeviceName: "Sensor",
		InterfaceID: "HmIP-RF", Timestamp: time.Now(),
	}})
	if err := h.ServiceMessages.Disable(context.Background(), "S2"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()
	ListSuppressedServiceMessages(idx).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out []SuppressedServiceMessageDTO
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0].Channel != "DEF:2" {
		t.Fatalf("len=%d entries=%+v, want 1 entry for DEF:2 kept despite the read error", len(out), out)
	}
}
