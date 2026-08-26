// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// stubScheduleService is an inline stub for ScheduleService.
type stubScheduleService struct {
	listDevices    []hmapi.ScheduleDeviceSummary
	listErr        error
	getResult      *ClimateSchedule
	getErr         error
	putErr         error
	setProfileErr  error
	autoGetResult  *ClimateSchedule
	autoGetErr     error
	autoPutErr     error
	autoProfileErr error
	copyErr        error
	copyProfileErr error
	// captured call args for assertions.
	copyTarget       string
	copyProfileSrcCh string
	copyProfileSrcP  int
	copyProfileDstCh string
	copyProfileDstP  int
}

func (s *stubScheduleService) GetClimateSchedule(_ context.Context, _ string, _ int) (*ClimateSchedule, error) {
	return s.getResult, s.getErr
}

func (s *stubScheduleService) PutClimateSchedule(_ context.Context, _ string, _ int, _ *ClimateSchedule) error {
	return s.putErr
}

func (s *stubScheduleService) SetActiveProfile(_ context.Context, _ string, _ int, _ string) error {
	return s.setProfileErr
}

func (s *stubScheduleService) GetClimateScheduleAuto(_ context.Context, _ string) (*ClimateSchedule, error) {
	return s.autoGetResult, s.autoGetErr
}

func (s *stubScheduleService) PutClimateScheduleAuto(_ context.Context, _ string, _ *ClimateSchedule) error {
	return s.autoPutErr
}

func (s *stubScheduleService) SetActiveProfileAuto(_ context.Context, _, _ string) error {
	return s.autoProfileErr
}

func (s *stubScheduleService) FindScheduleChannel(_ context.Context, _ string) (int, error) {
	return 1, nil
}

func (s *stubScheduleService) ListScheduleDevices(_ context.Context) ([]hmapi.ScheduleDeviceSummary, error) {
	return s.listDevices, s.listErr
}

func (s *stubScheduleService) CopySchedule(_ context.Context, _, dst string) error {
	s.copyTarget = dst
	return s.copyErr
}

func (s *stubScheduleService) CopyClimateProfile(_ context.Context, srcCh string, srcP int, dstCh string, dstP int) error {
	s.copyProfileSrcCh = srcCh
	s.copyProfileSrcP = srcP
	s.copyProfileDstCh = dstCh
	s.copyProfileDstP = dstP
	return s.copyProfileErr
}

func TestGetSchedule_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{
		getResult: &ClimateSchedule{Kind: "climate"},
	}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	GetSchedule(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetSchedule_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	GetSchedule(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGetSchedule_InvalidChannelNo_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "notanumber"}))
	w := httptest.NewRecorder()
	GetSchedule(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPutSchedule_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{}
	body := strings.NewReader(`{"kind":"climate","channel":{"address":"DEV001:1","number":1,"device_address":"DEV001"}}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	PutSchedule(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutSchedule_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{}
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader("NOT JSON"))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	PutSchedule(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetScheduleAuto_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{
		autoGetResult: &ClimateSchedule{Kind: "simple"},
	}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	GetScheduleAuto(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostActiveProfile_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{}
	body := strings.NewReader(`{"profile":"P2"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	PostActiveProfile(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostActiveProfile_ServiceError(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{setProfileErr: errors.New("write failed")}
	body := strings.NewReader(`{"profile":"P2"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	PostActiveProfile(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestPostActiveProfileAuto_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{}
	body := strings.NewReader(`{"profile":"P3"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PostActiveProfileAuto(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- PutScheduleAuto ---

func TestPutScheduleAuto_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{}
	body := strings.NewReader(`{"kind":"simple","channel":{"address":"DEV001:2","number":2,"device_address":"DEV001"}}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PutScheduleAuto(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutScheduleAuto_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"kind":"simple"}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PutScheduleAuto(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestPutScheduleAuto_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{}
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader("NOT JSON"))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PutScheduleAuto(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPutScheduleAuto_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{autoPutErr: errors.New("CCU error")}
	body := strings.NewReader(`{"kind":"simple"}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PutScheduleAuto(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- GetScheduleAuto error paths ---

func TestGetScheduleAuto_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	GetScheduleAuto(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGetScheduleAuto_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{autoGetErr: errors.New("CCU error")}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	GetScheduleAuto(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetScheduleAuto_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{autoGetErr: hmerr.ErrDescriptionNotFound}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	GetScheduleAuto(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- PostActiveProfileAuto error paths ---

func TestPostActiveProfileAuto_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"profile":"P1"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PostActiveProfileAuto(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestPostActiveProfileAuto_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("NOT JSON"))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PostActiveProfileAuto(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPostActiveProfileAuto_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{autoProfileErr: errors.New("CCU error")}
	body := strings.NewReader(`{"profile":"P2"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PostActiveProfileAuto(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- PostActiveProfile error paths ---

func TestPostActiveProfile_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"profile":"P1"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	PostActiveProfile(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestPostActiveProfile_InvalidChannelNo_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{}
	body := strings.NewReader(`{"profile":"P1"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "notanumber"}))
	w := httptest.NewRecorder()
	PostActiveProfile(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPostActiveProfile_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("NOT JSON"))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	PostActiveProfile(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- PutSchedule error paths ---

func TestPutSchedule_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"kind":"climate"}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	PutSchedule(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestPutSchedule_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{putErr: errors.New("write failed")}
	body := strings.NewReader(`{"kind":"climate","channel":{"address":"DEV001:1","number":1,"device_address":"DEV001"}}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	PutSchedule(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- writeScheduleError specific cases ---

func TestWriteScheduleError_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	writeScheduleError(w, r, hmerr.ErrDescriptionNotFound)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestWriteScheduleError_NoScheduleParams_Returns404(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	writeScheduleError(w, r, fmt.Errorf("read source profile: %w", hmerr.ErrNoSchedule))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestWriteScheduleError_GenericError_Returns502(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	writeScheduleError(w, r, errors.New("some unexpected error"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// PostCopySchedule
// ---------------------------------------------------------------------------

// TestPostCopySchedule_HappyPath verifies that the handler parses the body,
// passes the target address to the service, and returns 202.
func TestPostCopySchedule_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{}
	body := strings.NewReader(`{"target_device_address":"DEV2"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV1"}))
	w := httptest.NewRecorder()
	PostCopySchedule(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.copyTarget != "DEV2" {
		t.Errorf("copyTarget=%q want DEV2", svc.copyTarget)
	}
}

// TestPostCopySchedule_NilService_Returns503 verifies that a nil service yields 503.
func TestPostCopySchedule_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"target_device_address":"DEV2"}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV1"}))
	w := httptest.NewRecorder()
	PostCopySchedule(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// TestPostCopySchedule_EmptyTarget_Returns422 verifies that an absent
// target_device_address field yields 422.
func TestPostCopySchedule_EmptyTarget_Returns422(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV1"}))
	w := httptest.NewRecorder()
	PostCopySchedule(svc).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestPostCopySchedule_SourceWithoutSchedule_Returns404 verifies the
// wrapped "no climate schedule" sentinel from the copy path is mapped to
// 404, not to a 502 upstream failure.
func TestPostCopySchedule_SourceWithoutSchedule_Returns404(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{
		copyErr: fmt.Errorf("schedules: copy read source: %w", hmerr.ErrNoSchedule),
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"target_device_address":"DEV2"}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV1"}))
	w := httptest.NewRecorder()
	PostCopySchedule(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestPostCopySchedule_NoOp_Returns422 verifies copying a device onto
// itself is reported as a caller mistake, not an upstream failure.
func TestPostCopySchedule_NoOp_Returns422(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{
		copyErr: hmerr.ErrScheduleCopyNoOp,
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"target_device_address":"DEV1"}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV1"}))
	w := httptest.NewRecorder()
	PostCopySchedule(svc).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestPostCopyProfile_DomainProfileRange_Returns422 verifies the domain's
// profile-range rejection maps to 422 even when it reaches the handler
// (the handler pre-validates the same range).
func TestPostCopyProfile_DomainProfileRange_Returns422(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{
		copyProfileErr: hmerr.ErrScheduleCopyProfileRange,
	}
	body := strings.NewReader(`{"source_profile":1,"target_channel_address":"DEV2:2","target_profile":2}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV1", "no": "1"}))
	w := httptest.NewRecorder()
	PostCopyProfile(svc).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// PostCopyProfile
// ---------------------------------------------------------------------------

// TestPostCopyProfile_HappyPath verifies that path params and body are
// assembled into the correct srcChannelAddress and forwarded to the service.
func TestPostCopyProfile_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{}
	body := strings.NewReader(`{"source_profile":1,"target_channel_address":"DEV2:2","target_profile":2}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV1", "no": "1"}))
	w := httptest.NewRecorder()
	PostCopyProfile(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.copyProfileSrcCh != "DEV1:1" {
		t.Errorf("copyProfileSrcCh=%q want DEV1:1", svc.copyProfileSrcCh)
	}
	if svc.copyProfileDstP != 2 {
		t.Errorf("copyProfileDstP=%d want 2", svc.copyProfileDstP)
	}
}

// TestPostCopyProfile_InvalidSourceProfile_Returns422 verifies that a
// source_profile of 0 is rejected before calling the service.
func TestPostCopyProfile_InvalidSourceProfile_Returns422(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{}
	body := strings.NewReader(`{"source_profile":0,"target_channel_address":"DEV2:2","target_profile":2}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV1", "no": "1"}))
	w := httptest.NewRecorder()
	PostCopyProfile(svc).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestPostCopyProfile_NilService_Returns503 verifies that a nil service
// yields 503 before parsing.
func TestPostCopyProfile_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"source_profile":1,"target_channel_address":"DEV2:2","target_profile":2}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV1", "no": "1"}))
	w := httptest.NewRecorder()
	PostCopyProfile(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}
