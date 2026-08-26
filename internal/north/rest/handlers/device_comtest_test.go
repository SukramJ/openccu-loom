// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// stubCommunicationTestPort is a minimal fake for DeviceCommunicationTestPort.
type stubCommunicationTestPort struct {
	result      hmapi.CommunicationTestResult
	err         error
	lastAddress string
	// awaitCtx makes the stub behave like the CCU poll loop: it blocks
	// until the caller's context ends and reports that context error.
	awaitCtx bool
}

func (s *stubCommunicationTestPort) TestDeviceCommunication(ctx context.Context, addr string) (hmapi.CommunicationTestResult, error) {
	s.lastAddress = addr
	if s.awaitCtx {
		<-ctx.Done()
		return hmapi.CommunicationTestResult{}, ctx.Err()
	}
	if s.err != nil {
		return hmapi.CommunicationTestResult{}, s.err
	}
	return s.result, nil
}

// TestDeviceCommunicationTest_HappyPath_Returns200AndRecordsAudit verifies
// a successful test returns 200 with the CommunicationTestResult body and
// records exactly one audit entry tagged
// audit.ActionDeviceCommunicationTest against the device address.
func TestDeviceCommunicationTest_HappyPath_Returns200AndRecordsAudit(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	completed := started.Add(3 * time.Second)
	svc := &stubCommunicationTestPort{result: hmapi.CommunicationTestResult{
		Passed:      true,
		StartedAt:   started,
		CompletedAt: completed,
		DurationMs:  3000,
	}}
	rec := &captureRecorder{}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
	w := httptest.NewRecorder()
	TestDeviceCommunication(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body hmapi.CommunicationTestResult
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.Passed {
		t.Errorf("expected passed=true, got %+v", body)
	}
	if body.DurationMs != 3000 {
		t.Errorf("expected duration_ms=3000, got %d", body.DurationMs)
	}
	if svc.lastAddress != "0001ABCD" {
		t.Fatalf("expected lastAddress=0001ABCD, got %q", svc.lastAddress)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("expected exactly 1 audit entry, got %d: %+v", len(rec.entries), rec.entries)
	}
	e := rec.entries[0]
	if e.Action != audit.ActionDeviceCommunicationTest {
		t.Errorf("expected action=%q, got %q", audit.ActionDeviceCommunicationTest, e.Action)
	}
	if e.DeviceAddress != "0001ABCD" {
		t.Errorf("expected device_address=%q, got %q", "0001ABCD", e.DeviceAddress)
	}
}

// TestDeviceCommunicationTest_Unsupported_Returns422 verifies an interface
// without the communication-test capability (VirtualDevices, CUxD)
// surfaces as 422, not a generic 502.
func TestDeviceCommunicationTest_Unsupported_Returns422(t *testing.T) {
	t.Parallel()
	svc := &stubCommunicationTestPort{err: backends.ErrUnsupported}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
	w := httptest.NewRecorder()
	TestDeviceCommunication(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestDeviceCommunicationTest_ServiceError_Returns502 verifies a
// non-capability failure (e.g. the CCU is unreachable) surfaces as a 502
// upstream error.
func TestDeviceCommunicationTest_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubCommunicationTestPort{err: errors.New("CCU unreachable")}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
	w := httptest.NewRecorder()
	TestDeviceCommunication(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestDeviceCommunicationTest_PollBudgetElapsed_Returns200TimedOut
// verifies an unreachable device reports the documented timed-out result
// instead of a 502: the handler keeps a response margin of the request
// deadline, so its own poll budget ends first while the request is still
// alive.
func TestDeviceCommunicationTest_PollBudgetElapsed_Returns200TimedOut(t *testing.T) {
	t.Parallel()
	svc := &stubCommunicationTestPort{awaitCtx: true}
	ctx, cancel := context.WithTimeout(t.Context(), comTestResponseMargin+500*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody).WithContext(ctx)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
	w := httptest.NewRecorder()
	TestDeviceCommunication(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body hmapi.CommunicationTestResult
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.TimedOut || body.Passed {
		t.Fatalf("expected timed_out=true passed=false, got %+v", body)
	}
	if body.DurationMs <= 0 {
		t.Errorf("expected a positive duration_ms, got %d", body.DurationMs)
	}
}

// TestDeviceCommunicationTest_RequestCanceled_Returns502 verifies the
// timed-out result is only reported for the handler's own poll budget: a
// caller-side cancellation stays an upstream error.
func TestDeviceCommunicationTest_RequestCanceled_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubCommunicationTestPort{awaitCtx: true}
	ctx, cancel := context.WithCancel(t.Context())
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody).WithContext(ctx)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
	cancel()
	w := httptest.NewRecorder()
	TestDeviceCommunication(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestDeviceCommunicationTest_NilService_Returns503 verifies the handler
// degrades gracefully (503) rather than panicking when the daemon has not
// wired a DeviceCommunicationTestPort.
func TestDeviceCommunicationTest_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
	w := httptest.NewRecorder()
	TestDeviceCommunication(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestDeviceCommunicationTest_MissingAddr_Returns400 verifies an empty
// {addr} path parameter is rejected before the domain layer is ever
// called.
func TestDeviceCommunicationTest_MissingAddr_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubCommunicationTestPort{}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": ""}))
	w := httptest.NewRecorder()
	TestDeviceCommunication(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastAddress != "" {
		t.Errorf("domain layer must not be called on a missing address, got lastAddress=%q", svc.lastAddress)
	}
}
