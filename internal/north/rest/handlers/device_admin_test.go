// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// stubDeviceAdmin is an inline stub for DeviceAdmin.
type stubDeviceAdmin struct {
	unpairErr           error
	renameErr           error
	renameChannelErr    error
	acceptErr           error
	updateFWErr         error
	setRoomsErr         error
	setFunctionsErr     error
	lastAddress         string
	lastNewName         string
	lastIncludeChannels bool
	lastChannelNo       int
	lastRooms           []string
	lastFunctions       []string
	lastReset           bool
	lastForce           bool
	lastAcceptOpts      interfaces.AcceptInboxOptions
}

func (s *stubDeviceAdmin) UnpairDevice(_ context.Context, addr string, reset, force bool) error {
	s.lastAddress = addr
	s.lastReset = reset
	s.lastForce = force
	return s.unpairErr
}

func (s *stubDeviceAdmin) RenameDevice(_ context.Context, addr, name string, includeChannels bool) error {
	s.lastAddress = addr
	s.lastNewName = name
	s.lastIncludeChannels = includeChannels
	return s.renameErr
}

func (s *stubDeviceAdmin) RenameChannel(_ context.Context, deviceAddr string, channelNo int, name string) error {
	s.lastAddress = deviceAddr
	s.lastChannelNo = channelNo
	s.lastNewName = name
	return s.renameChannelErr
}

func (s *stubDeviceAdmin) AcceptInboxDevice(_ context.Context, addr string, opts interfaces.AcceptInboxOptions) error {
	s.lastAddress = addr
	s.lastAcceptOpts = opts
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

func TestDeleteDevice_DefaultFlagsFalse(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/devices/DEV001", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	DeleteDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if admin.lastReset || admin.lastForce {
		t.Fatalf("expected reset=false force=false when flags omitted, got reset=%v force=%v",
			admin.lastReset, admin.lastForce)
	}
}

func TestDeleteDevice_ParsesResetAndForceFlags(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	req := httptest.NewRequest(http.MethodDelete,
		"/api/v1/devices/DEV001?reset=true&force=true", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	DeleteDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if !admin.lastReset || !admin.lastForce {
		t.Fatalf("expected reset=true force=true, got reset=%v force=%v",
			admin.lastReset, admin.lastForce)
	}
}

func TestDeleteDevice_ParsesAsymmetricFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		query     string
		wantReset bool
		wantForce bool
	}{
		{name: "reset only", query: "?reset=true", wantReset: true, wantForce: false},
		{name: "force only", query: "?force=true", wantReset: false, wantForce: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			admin := &stubDeviceAdmin{}
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/devices/DEV001"+tt.query, http.NoBody)
			req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
			w := httptest.NewRecorder()
			DeleteDevice(admin).ServeHTTP(w, req)

			if w.Code != http.StatusAccepted {
				t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
			}
			if admin.lastReset != tt.wantReset || admin.lastForce != tt.wantForce {
				t.Fatalf("reset=%v force=%v, want reset=%v force=%v",
					admin.lastReset, admin.lastForce, tt.wantReset, tt.wantForce)
			}
		})
	}
}

// TestDeleteDevice_MalformedFlag_DefaultsFalse verifies that a query flag
// which does not parse as a bool (neither "true"/"false" nor "1"/"0") is
// treated as absent rather than rejected with a 400 — the delete-options
// contract degrades to the safe default instead of failing the request.
func TestDeleteDevice_MalformedFlag_DefaultsFalse(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/devices/DEV001?reset=yes&force=nope", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	DeleteDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if admin.lastReset || admin.lastForce {
		t.Fatalf("expected reset=false force=false for malformed values, got reset=%v force=%v",
			admin.lastReset, admin.lastForce)
	}
}

func TestDeleteDevice_Unsupported_Returns422(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{unpairErr: backends.ErrUnsupported}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/devices/DEV001", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	DeleteDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
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

// TestAcceptInboxDevice_EmptyBody_AcceptsWithoutConfig verifies the
// backward-compatible path: an empty request stream decodes to io.EOF,
// which the handler treats as "no first-time configuration" and forwards
// a zero-value options struct.
func TestAcceptInboxDevice_EmptyBody_AcceptsWithoutConfig(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AcceptInboxDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if admin.lastAcceptOpts.HasConfig() {
		t.Fatalf("expected empty options for empty body, got %+v", admin.lastAcceptOpts)
	}
}

// TestAcceptInboxDevice_BodyForwardsConfig verifies the handler parses
// every optional field and forwards it into the AcceptInboxOptions.
func TestAcceptInboxDevice_BodyForwardsConfig(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	body := strings.NewReader(
		`{"name":"Kitchen","include_channels":true,"rooms":["Living Room"],"functions":["Lights"]}`,
	)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AcceptInboxDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	got := admin.lastAcceptOpts
	if got.Name != "Kitchen" || !got.IncludeChannels {
		t.Fatalf("name/include_channels not forwarded: %+v", got)
	}
	if len(got.Rooms) != 1 || got.Rooms[0] != "Living Room" {
		t.Fatalf("rooms not forwarded: %+v", got.Rooms)
	}
	if len(got.Functions) != 1 || got.Functions[0] != "Lights" {
		t.Fatalf("functions not forwarded: %+v", got.Functions)
	}
}

// TestAcceptInboxDevice_PartialBody_OnlyName_RoomsAndFunctionsStayNil
// verifies that a body naming only one field leaves the other pointer
// fields nil end to end, so AcceptInboxOptions.Rooms/Functions stay nil
// ("untouched") rather than turning into an empty slice.
func TestAcceptInboxDevice_PartialBody_OnlyName_RoomsAndFunctionsStayNil(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	body := strings.NewReader(`{"name":"Kitchen"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AcceptInboxDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	got := admin.lastAcceptOpts
	if got.Name != "Kitchen" {
		t.Fatalf("name not forwarded: %+v", got)
	}
	if got.Rooms != nil || got.Functions != nil {
		t.Fatalf("expected rooms/functions to stay nil (untouched) for an omitted field, got %+v", got)
	}
}

// TestAcceptInboxDevice_RoomsEmptyArray_ForwardsNonNilEmptySlice verifies
// the handler preserves the "explicit empty array clears the assignment"
// signal: `"rooms":[]` must decode to a non-nil, zero-length slice, not to
// nil (which would mean "leave untouched").
func TestAcceptInboxDevice_RoomsEmptyArray_ForwardsNonNilEmptySlice(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	body := strings.NewReader(`{"rooms":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AcceptInboxDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	got := admin.lastAcceptOpts
	if got.Rooms == nil {
		t.Fatal("expected a non-nil empty rooms slice, got nil (would mean untouched)")
	}
	if len(got.Rooms) != 0 {
		t.Fatalf("expected zero rooms, got %+v", got.Rooms)
	}
	if !got.HasConfig() {
		t.Fatal("an explicit empty rooms array must still count as configuration")
	}
}

// TestAcceptInboxDevice_RoomsWrongType_Returns400 verifies a body that is
// syntactically valid JSON but has the wrong type for a field (a string
// instead of an array) is rejected as a decode error, not silently coerced.
func TestAcceptInboxDevice_RoomsWrongType_Returns400(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	body := strings.NewReader(`{"rooms":"not-an-array"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AcceptInboxDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if admin.lastAddress != "" {
		t.Fatal("accept must not be attempted on a body with a type-mismatched field")
	}
}

// TestAcceptInboxDevice_UnknownField_Returns400 verifies the decoder's
// DisallowUnknownFields rejects a body carrying an unrecognised key rather
// than silently ignoring it.
func TestAcceptInboxDevice_UnknownField_Returns400(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	body := strings.NewReader(`{"nickname":"Kitchen"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AcceptInboxDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestAcceptInboxDevice_InvalidJSON_Returns400 verifies a malformed body
// is rejected before the accept is attempted.
func TestAcceptInboxDevice_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("NOT JSON"))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AcceptInboxDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if admin.lastAddress != "" {
		t.Fatal("accept must not be attempted on a malformed body")
	}
}

// TestAcceptInboxDevice_ConfigIncomplete_Returns502WithClearTitle verifies
// that an ErrAcceptConfigIncomplete (accept succeeded, follow-up failed)
// is surfaced as a 502 whose title tells the operator the accept already
// happened.
func TestAcceptInboxDevice_ConfigIncomplete_Returns502WithClearTitle(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{
		acceptErr: fmt.Errorf("%w: rooms: ccu unreachable", interfaces.ErrAcceptConfigIncomplete),
	}
	body := strings.NewReader(`{"rooms":["Living Room"]}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AcceptInboxDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "accepted") {
		t.Fatalf("expected a title stating the device was accepted, got %s", w.Body.String())
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

func TestPatchDevice_RenameIncludeChannels_ForwardsFlag(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	body := strings.NewReader(`{"name":"New","include_channels":true}`)
	req := httptest.NewRequest(http.MethodPatch, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PatchDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if !admin.lastIncludeChannels {
		t.Fatal("expected include_channels flag to be forwarded as true")
	}
}

func TestPatchDevice_RenameDefaultsIncludeChannelsFalse(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	body := strings.NewReader(`{"name":"New"}`)
	req := httptest.NewRequest(http.MethodPatch, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PatchDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if admin.lastIncludeChannels {
		t.Fatal("expected include_channels to default to false when omitted")
	}
}

func TestPatchDevice_RenameUnsupported_Returns422(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{renameErr: backends.ErrUnsupported}
	body := strings.NewReader(`{"name":"New"}`)
	req := httptest.NewRequest(http.MethodPatch, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PatchDevice(admin).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPatchChannel_HappyPath(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	body := strings.NewReader(`{"name":"Kitchen Light"}`)
	req := httptest.NewRequest(http.MethodPatch, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "3"}))
	w := httptest.NewRecorder()
	PatchChannel(admin).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if admin.lastChannelNo != 3 || admin.lastNewName != "Kitchen Light" {
		t.Fatalf("channel rename not applied: no=%d name=%q", admin.lastChannelNo, admin.lastNewName)
	}
}

func TestPatchChannel_NilAdmin_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"name":"x"}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "3"}))
	w := httptest.NewRecorder()
	PatchChannel(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestPatchChannel_NoName_Returns422(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "3"}))
	w := httptest.NewRecorder()
	PatchChannel(admin).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}
}

func TestPatchChannel_InvalidChannelNo_Returns400(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{}
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"name":"x"}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "abc"}))
	w := httptest.NewRecorder()
	PatchChannel(admin).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPatchChannel_Unsupported_Returns422(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{renameChannelErr: backends.ErrUnsupported}
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"name":"x"}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "3"}))
	w := httptest.NewRecorder()
	PatchChannel(admin).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}
}

func TestPatchChannel_Error_Returns502(t *testing.T) {
	t.Parallel()
	admin := &stubDeviceAdmin{renameChannelErr: errors.New("ccu unreachable")}
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"name":"x"}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "3"}))
	w := httptest.NewRecorder()
	PatchChannel(admin).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

// --- RefreshFirmwareData ---

// stubFirmwareRefresher is an inline stub for the FirmwareRefresher port.
type stubFirmwareRefresher struct {
	err   error
	calls int
}

func (s *stubFirmwareRefresher) RefreshFirmwareData(_ context.Context) error {
	s.calls++
	return s.err
}

// TestRefreshFirmwareData_NilRefresher_Returns503 verifies that a nil
// FirmwareRefresher yields HTTP 503 with a problem+json body — the same
// "not wired yet" contract as the other DeviceAdmin handlers.
func TestRefreshFirmwareData_NilRefresher_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/devices/firmware/refresh", http.NoBody)
	w := httptest.NewRecorder()
	RefreshFirmwareData(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("expected Content-Type application/problem+json, got %q", ct)
	}
}

// TestRefreshFirmwareData_RefresherError_Returns502 verifies that a sweep
// error is surfaced as HTTP 502 (upstream/CCU unavailable), matching the
// other admin handlers' error-mapping convention.
func TestRefreshFirmwareData_RefresherError_Returns502(t *testing.T) {
	t.Parallel()
	refresher := &stubFirmwareRefresher{err: errors.New("ccu unreachable")}
	req := httptest.NewRequest(http.MethodPost, "/devices/firmware/refresh", http.NoBody)
	w := httptest.NewRecorder()
	RefreshFirmwareData(refresher).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("expected Content-Type application/problem+json, got %q", ct)
	}
	if refresher.calls != 1 {
		t.Errorf("expected 1 call to RefreshFirmwareData, got %d", refresher.calls)
	}
}

// TestRefreshFirmwareData_HappyPath_Returns204 verifies the success path
// responds with an empty 204 body — the sweep is synchronous and there is
// nothing to report back beyond the status code.
func TestRefreshFirmwareData_HappyPath_Returns204(t *testing.T) {
	t.Parallel()
	refresher := &stubFirmwareRefresher{}
	req := httptest.NewRequest(http.MethodPost, "/devices/firmware/refresh", http.NoBody)
	w := httptest.NewRecorder()
	RefreshFirmwareData(refresher).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body on 204, got %q", w.Body.String())
	}
	if refresher.calls != 1 {
		t.Errorf("expected 1 call to RefreshFirmwareData, got %d", refresher.calls)
	}
}
