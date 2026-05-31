// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenEditSession_HappyPath(t *testing.T) {
	t.Parallel()
	s := NewEditSessions()
	body := strings.NewReader(`{"key":"channel:DEV001:1:MASTER","subject":"user1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/edit", body)
	w := httptest.NewRecorder()
	OpenEditSession(s).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp EditSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("token must not be empty")
	}
	if resp.Key != "channel:DEV001:1:MASTER" {
		t.Fatalf("key mismatch: %q", resp.Key)
	}
}

func TestOpenEditSession_NilStore_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"key":"some-key"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	OpenEditSession(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestOpenEditSession_EmptyKey_Returns422(t *testing.T) {
	t.Parallel()
	s := NewEditSessions()
	body := strings.NewReader(`{"key":""}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	OpenEditSession(s).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}
}

func TestOpenEditSession_DuplicateKey_Returns423(t *testing.T) {
	t.Parallel()
	s := NewEditSessions()
	// First open succeeds.
	body1 := strings.NewReader(`{"key":"channel:DEV001:1:MASTER","subject":"user1"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/", body1)
	w1 := httptest.NewRecorder()
	OpenEditSession(s).ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first open failed: %d %s", w1.Code, w1.Body.String())
	}

	// Second open on same key must be locked.
	body2 := strings.NewReader(`{"key":"channel:DEV001:1:MASTER","subject":"user2"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/", body2)
	w2 := httptest.NewRecorder()
	OpenEditSession(s).ServeHTTP(w2, req2)

	if w2.Code != http.StatusLocked {
		t.Fatalf("expected 423, got %d body=%s", w2.Code, w2.Body.String())
	}
}

func TestHeartbeatEditSession_HappyPath(t *testing.T) {
	t.Parallel()
	s := NewEditSessions()
	key := "channel:DEV001:1:MASTER"
	lock, ok := s.Open(key, "user1")
	if !ok {
		t.Fatal("open failed")
	}
	payload := EditSessionResponse{Token: lock.Token, Key: key}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(b)))
	w := httptest.NewRecorder()
	HeartbeatEditSession(s).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHeartbeatEditSession_ExpiredToken_Returns410(t *testing.T) {
	t.Parallel()
	s := NewEditSessions()
	// Heartbeat on a key that was never opened.
	payload := EditSessionResponse{Token: "bogus-token", Key: "no-such-key"}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(b)))
	w := httptest.NewRecorder()
	HeartbeatEditSession(s).ServeHTTP(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d", w.Code)
	}
}

func TestCloseEditSession_HappyPath(t *testing.T) {
	t.Parallel()
	s := NewEditSessions()
	key := "channel:DEV001:2:MASTER"
	lock, _ := s.Open(key, "user1")
	payload := EditSessionResponse{Token: lock.Token, Key: key}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodDelete, "/", strings.NewReader(string(b)))
	w := httptest.NewRecorder()
	CloseEditSession(s).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestForceCloseEditSession_HappyPath(t *testing.T) {
	t.Parallel()
	s := NewEditSessions()
	key := "channel:DEV001:3:MASTER"
	s.Open(key, "user1") //nolint:errcheck // return value is not meaningful in this test setup
	body := strings.NewReader(`{"key":"channel:DEV001:3:MASTER"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	ForceCloseEditSession(s).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- HeartbeatEditSession: nil, bad body, expired ---

func TestHeartbeatEditSession_NilStore_Returns503(t *testing.T) {
	t.Parallel()
	body := `{"key":"k","token":"t"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions/heartbeat", strings.NewReader(body))
	w := httptest.NewRecorder()
	HeartbeatEditSession(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHeartbeatEditSession_BadBody_Returns400(t *testing.T) {
	t.Parallel()
	s := NewEditSessions()
	req := httptest.NewRequest(http.MethodPost, "/sessions/heartbeat", strings.NewReader("not-json"))
	w := httptest.NewRecorder()
	HeartbeatEditSession(s).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHeartbeatEditSession_ExpiredSession_Returns410(t *testing.T) {
	t.Parallel()
	s := NewEditSessions()
	body := `{"key":"nonexistent","token":"sometoken"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions/heartbeat", strings.NewReader(body))
	w := httptest.NewRecorder()
	HeartbeatEditSession(s).ServeHTTP(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- ForceCloseEditSession: nil, bad body ---

func TestForceCloseEditSession_NilStore_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/sessions/takeover", strings.NewReader(`{"key":"k"}`))
	w := httptest.NewRecorder()
	ForceCloseEditSession(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestForceCloseEditSession_BadBody_Returns400(t *testing.T) {
	t.Parallel()
	s := NewEditSessions()
	req := httptest.NewRequest(http.MethodPost, "/sessions/takeover", strings.NewReader("bad"))
	w := httptest.NewRecorder()
	ForceCloseEditSession(s).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestForceCloseEditSession_HappyPath_Returns204(t *testing.T) {
	t.Parallel()
	s := NewEditSessions()
	body := strings.NewReader(`{"key":"channel:DEV001:1:MASTER","subject":"user1"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/sessions/open", body)
	w1 := httptest.NewRecorder()
	OpenEditSession(s).ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("open session: %d %s", w1.Code, w1.Body.String())
	}

	// Now force-close it.
	closeBody := strings.NewReader(`{"key":"channel:DEV001:1:MASTER"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/sessions/takeover", closeBody)
	w2 := httptest.NewRecorder()
	ForceCloseEditSession(s).ServeHTTP(w2, req2)

	if w2.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w2.Code, w2.Body.String())
	}
}

// --- CloseEditSession: nil, bad body ---

func TestCloseEditSession_NilStore_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/sessions/edit", strings.NewReader(`{"key":"k","token":"t"}`))
	w := httptest.NewRecorder()
	CloseEditSession(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestCloseEditSession_BadBody_Returns400(t *testing.T) {
	t.Parallel()
	s := NewEditSessions()
	req := httptest.NewRequest(http.MethodDelete, "/sessions/edit", strings.NewReader("bad"))
	w := httptest.NewRecorder()
	CloseEditSession(s).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCloseEditSession_HappyPath_Returns204(t *testing.T) {
	t.Parallel()
	s := NewEditSessions()
	// Open a session first.
	openBody := strings.NewReader(`{"key":"channel:DEV999:2:MASTER","subject":"user99"}`)
	reqOpen := httptest.NewRequest(http.MethodPost, "/sessions/open", openBody)
	wOpen := httptest.NewRecorder()
	OpenEditSession(s).ServeHTTP(wOpen, reqOpen)
	if wOpen.Code != http.StatusOK {
		t.Fatalf("open session: %d %s", wOpen.Code, wOpen.Body.String())
	}
	var openResp EditSessionResponse
	_ = json.Unmarshal(wOpen.Body.Bytes(), &openResp)

	// Close using the token from the open response.
	closePayload, _ := json.Marshal(EditSessionResponse{Key: openResp.Key, Token: openResp.Token})
	reqClose := httptest.NewRequest(http.MethodDelete, "/sessions/edit", bytes.NewReader(closePayload))
	wClose := httptest.NewRecorder()
	CloseEditSession(s).ServeHTTP(wClose, reqClose)

	if wClose.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", wClose.Code, wClose.Body.String())
	}
}

// --- EditSessions prune: expired entry path ---

func TestEditSessions_Prune_ExpiresExpiredLock(t *testing.T) {
	t.Parallel()
	s := NewEditSessions()
	// Open a session.
	lock, ok := s.Open("key1", "user1")
	if !ok {
		t.Fatal("expected open to succeed")
	}
	// Manually expire it by setting Expires to the past.
	lock.Expires = lock.Expires.Add(-2 * EditSessionTTL)
	// Open the same key again — prune should clean up the expired one.
	_, ok2 := s.Open("key1", "user2")
	if !ok2 {
		t.Fatal("expected second open to succeed after expiry")
	}
}
