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

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stubDataPointWriter records SetValue calls and returns a configured error.
type stubDataPointWriter struct {
	calledAddress   string
	calledParameter hmenum.Parameter
	calledValue     any
	err             error
}

func (s *stubDataPointWriter) SetValue(_ context.Context, address string, parameter hmenum.Parameter, value any, _ hmenum.CommandPriority) error {
	s.calledAddress = address
	s.calledParameter = parameter
	s.calledValue = value
	return s.err
}

// stubNext is a minimal http.Handler that records whether it was called.
func stubNext() (handler http.Handler, called *bool) {
	wasCalled := false
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		wasCalled = true
		w.WriteHeader(http.StatusOK)
	})
	return h, &wasCalled
}

// ---------------------------------------------------------------------------
// InboundWebhookAuth middleware
// ---------------------------------------------------------------------------

func TestInboundWebhookAuth_NoCredentials_Returns401(t *testing.T) {
	t.Parallel()
	next, called := stubNext()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	w := httptest.NewRecorder()
	InboundWebhookAuth("secret")(next).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d body=%s", w.Code, w.Body.String())
	}
	if *called {
		t.Fatal("next handler must not be called on 401")
	}
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Error("WWW-Authenticate header must be set on 401")
	}
}

func TestInboundWebhookAuth_OperatorIdentity_PassesThrough(t *testing.T) {
	t.Parallel()
	next, called := stubNext()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(auth.ContextWithIdentity(req.Context(), auth.Identity{Subject: "op", Role: auth.RoleOperator}))
	w := httptest.NewRecorder()
	InboundWebhookAuth("secret")(next).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !*called {
		t.Fatal("next handler must be called for operator identity")
	}
}

func TestInboundWebhookAuth_ViewerIdentity_Returns401(t *testing.T) {
	t.Parallel()
	next, _ := stubNext()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(auth.ContextWithIdentity(req.Context(), auth.Identity{Subject: "viewer", Role: auth.RoleViewer}))
	w := httptest.NewRecorder()
	InboundWebhookAuth("")(next).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestInboundWebhookAuth_ValidBearerToken_PassesThrough(t *testing.T) {
	t.Parallel()
	next, called := stubNext()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req.Header.Set("Authorization", "Bearer secret-tok")
	w := httptest.NewRecorder()
	InboundWebhookAuth("secret-tok")(next).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !*called {
		t.Fatal("next handler must be called for valid bearer token")
	}
}

func TestInboundWebhookAuth_WrongBearerToken_Returns401(t *testing.T) {
	t.Parallel()
	next, _ := stubNext()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	InboundWebhookAuth("secret-tok")(next).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestInboundWebhookAuth_EmptyConfiguredToken_BearerPresent_Returns401(t *testing.T) {
	t.Parallel()
	next, _ := stubNext()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req.Header.Set("Authorization", "Bearer anything")
	w := httptest.NewRecorder()
	InboundWebhookAuth("")(next).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 when token path disabled, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// WebhookInboundValue
// ---------------------------------------------------------------------------

func TestWebhookInboundValue_HappyPath_Returns202(t *testing.T) {
	t.Parallel()
	writer := &stubDataPointWriter{}
	body := strings.NewReader(`{"address":"ABC:1","parameter":"STATE","value":true}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	WebhookInboundValue(writer).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", w.Code, w.Body.String())
	}
	if writer.calledAddress != "ABC:1" {
		t.Errorf("calledAddress=%q want ABC:1", writer.calledAddress)
	}
	if writer.calledParameter != hmenum.Parameter("STATE") {
		t.Errorf("calledParameter=%q want STATE", writer.calledParameter)
	}
	if writer.calledValue != true {
		t.Errorf("calledValue=%v want true", writer.calledValue)
	}
}

func TestWebhookInboundValue_MissingAddress_Returns400(t *testing.T) {
	t.Parallel()
	writer := &stubDataPointWriter{}
	body := strings.NewReader(`{"parameter":"STATE","value":true}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	WebhookInboundValue(writer).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestWebhookInboundValue_MissingParameter_Returns400(t *testing.T) {
	t.Parallel()
	writer := &stubDataPointWriter{}
	body := strings.NewReader(`{"address":"ABC:1","value":true}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	WebhookInboundValue(writer).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestWebhookInboundValue_NilWriter_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"address":"ABC:1","parameter":"STATE","value":true}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	WebhookInboundValue(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", w.Code)
	}
}

func TestWebhookInboundValue_WriterError_Returns502(t *testing.T) {
	t.Parallel()
	writer := &stubDataPointWriter{err: errors.New("upstream down")}
	body := strings.NewReader(`{"address":"ABC:1","parameter":"STATE","value":true}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	WebhookInboundValue(writer).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// WebhookInboundProgram
// ---------------------------------------------------------------------------

func TestWebhookInboundProgram_HappyPath_Returns202(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	prog := hub.NewProgram("test-ccu", "P1", "Morning Routine", "", false, &errProgramWriter{err: nil})
	h.PutProgram(prog)
	idx := &testHubIndex{h: h}

	body := strings.NewReader(`{"program":"P1"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	WebhookInboundProgram(idx).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestWebhookInboundProgram_MissingProgram_Returns400(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	idx := &testHubIndex{h: h}

	body := strings.NewReader(`{"program":""}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	WebhookInboundProgram(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestWebhookInboundProgram_TwoCentralsNoCentral_Returns400(t *testing.T) {
	t.Parallel()
	h1 := hub.NewHub("ccu-alpha")
	h2 := hub.NewHub("ccu-beta")
	idx := &multiHubIndex{hubs: []NamedHub{
		{Central: "ccu-alpha", Hub: h1},
		{Central: "ccu-beta", Hub: h2},
	}}

	body := strings.NewReader(`{"program":"P1"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	WebhookInboundProgram(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 when central ambiguous, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestWebhookInboundProgram_UnknownProgram_Returns404(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	idx := &testHubIndex{h: h}

	body := strings.NewReader(`{"program":"NONEXISTENT"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	WebhookInboundProgram(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestWebhookInboundProgram_NilIdx_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"program":"P1"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	WebhookInboundProgram(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", w.Code)
	}
}

func TestWebhookInboundProgram_ExecuteError_Returns502(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	prog := hub.NewProgram("test-ccu", "P1", "Morning Routine", "", false, &errProgramWriter{err: errors.New("exec fail")})
	h.PutProgram(prog)
	idx := &testHubIndex{h: h}

	body := strings.NewReader(`{"program":"P1"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	WebhookInboundProgram(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d body=%s", w.Code, w.Body.String())
	}
}
