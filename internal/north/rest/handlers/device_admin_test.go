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

	"github.com/go-chi/chi/v5"
)

// stubDeviceAdmin is an inline stub for DeviceAdmin.
type stubDeviceAdmin struct {
	unpairErr       error
	renameErr       error
	acceptErr       error
	updateFWErr     error
	setRoomsErr     error
	setFunctionsErr error
	lastAddress     string
	lastNewName     string
	lastRooms       []string
	lastFunctions   []string
}

func (s *stubDeviceAdmin) UnpairDevice(_ context.Context, addr string) error {
	s.lastAddress = addr
	return s.unpairErr
}

func (s *stubDeviceAdmin) RenameDevice(_ context.Context, addr, name string) error {
	s.lastAddress = addr
	s.lastNewName = name
	return s.renameErr
}

func (s *stubDeviceAdmin) AcceptInboxDevice(_ context.Context, addr string) error {
	s.lastAddress = addr
	return s.acceptErr
}

func (s *stubDeviceAdmin) UpdateFirmware(_ context.Context, addr string) error {
	s.lastAddress = addr
	return s.updateFWErr
}

func (s *stubDeviceAdmin) SetRooms(_ context.Context, addr string, rooms []string) error {
	s.lastAddress = addr
	s.lastRooms = rooms
	return s.setRoomsErr
}

func (s *stubDeviceAdmin) SetFunctions(_ context.Context, addr string, functions []string) error {
	s.lastAddress = addr
	s.lastFunctions = functions
	return s.setFunctionsErr
}

func TestDeleteDevice_HappyPath(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/devices/DEV001", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	DeleteDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if admin.lastAddress != "DEV001" {
		t.Fatalf("expected lastAddress=DEV001, got %q", admin.lastAddress)
	}
}

func TestDeleteDevice_AdminNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	DeleteDevice(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestDeleteDevice_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{unpairErr: errors.New("CCU refused")}
	req := httptest.NewRequest(http.MethodDelete, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	DeleteDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestPatchDevice_RenameHappyPath(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	body := strings.NewReader(`{"name": "New Name"}`)
	req := httptest.NewRequest(http.MethodPatch, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PatchDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if admin.lastNewName != "New Name" {
		t.Fatalf("expected lastNewName='New Name', got %q", admin.lastNewName)
	}
}

func TestPatchDevice_AdminNil_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"name": "foo"}`)
	req := httptest.NewRequest(http.MethodPatch, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PatchDevice(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestPatchDevice_NoFields_Returns422(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPatch, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PatchDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}
}

func TestPatchDevice_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader("NOT JSON"))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PatchDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPatchDevice_SetRoomsHappyPath(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	body := strings.NewReader(`{"rooms": ["Living Room", "Bedroom"]}`)
	req := httptest.NewRequest(http.MethodPatch, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PatchDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if len(admin.lastRooms) != 2 {
		t.Fatalf("expected 2 rooms, got %v", admin.lastRooms)
	}
}

func TestUpdateDeviceFirmware_HappyPath(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	UpdateDeviceFirmware(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["status"] != "scheduled" {
		t.Fatalf("expected status=scheduled, got %q", body["status"])
	}
}

func TestAcceptInboxDevice_HappyPath(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AcceptInboxDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}
}

// --- UpdateDeviceFirmware and AcceptInboxDevice additional error paths ---

func TestUpdateDeviceFirmware_NilAdmin_Returns503(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	r.Post("/devices/{addr}/firmware", UpdateDeviceFirmware(nil))
	req := httptest.NewRequest(http.MethodPost, "/devices/DEV001/firmware", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateDeviceFirmware_AdminError_Returns502(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{updateFWErr: errors.New("ccu unreachable")}
	r := chi.NewRouter()
	r.Post("/devices/{addr}/firmware", UpdateDeviceFirmware(admin))
	req := httptest.NewRequest(http.MethodPost, "/devices/DEV001/firmware", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateDeviceFirmware_HappyPath_Returns202(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	r := chi.NewRouter()
	r.Post("/devices/{addr}/firmware", UpdateDeviceFirmware(admin))
	req := httptest.NewRequest(http.MethodPost, "/devices/DEV001/firmware", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAcceptInboxDevice_NilAdmin_Returns503(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	r.Post("/devices/{addr}/accept", AcceptInboxDevice(nil))
	req := httptest.NewRequest(http.MethodPost, "/devices/DEV001/accept", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAcceptInboxDevice_AdminError_Returns502(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{acceptErr: errors.New("pairing failed")}
	r := chi.NewRouter()
	r.Post("/devices/{addr}/accept", AcceptInboxDevice(admin))
	req := httptest.NewRequest(http.MethodPost, "/devices/DEV001/accept", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAcceptInboxDevice_HappyPath_Returns202(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	r := chi.NewRouter()
	r.Post("/devices/{addr}/accept", AcceptInboxDevice(admin))
	req := httptest.NewRequest(http.MethodPost, "/devices/DEV001/accept", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- PatchDevice additional error paths ---

func TestPatchDevice_RenameError_Returns502(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{renameErr: errors.New("rename fail")}
	req := httptest.NewRequest(http.MethodPatch, "/",
		strings.NewReader(`{"name":"NewName"}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001:0"}))
	w := httptest.NewRecorder()
	PatchDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPatchDevice_SetRoomsError_Returns502(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{setRoomsErr: errors.New("rooms fail")}
	req := httptest.NewRequest(http.MethodPatch, "/",
		strings.NewReader(`{"rooms":["Living Room"]}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001:0"}))
	w := httptest.NewRecorder()
	PatchDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPatchDevice_SetFunctionsHappyPath_Returns202(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	req := httptest.NewRequest(http.MethodPatch, "/",
		strings.NewReader(`{"functions":["Lights"]}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001:0"}))
	w := httptest.NewRecorder()
	PatchDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPatchDevice_SetFunctionsError_Returns502(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{setFunctionsErr: errors.New("functions fail")}
	req := httptest.NewRequest(http.MethodPatch, "/",
		strings.NewReader(`{"functions":["Lights"]}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001:0"}))
	w := httptest.NewRecorder()
	PatchDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}
