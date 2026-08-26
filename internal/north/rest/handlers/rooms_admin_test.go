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

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// fakeRoomFunctionAdmin is a configurable stub satisfying RoomFunctionAdmin.
type fakeRoomFunctionAdmin struct {
	createRoomID  int
	createRoomErr error
	renameRoomErr error
	deleteRoomErr error
	createFnID    int
	createFnErr   error
	renameFnErr   error
	deleteFnErr   error
}

func (f *fakeRoomFunctionAdmin) CreateRoom(_ context.Context, _, _ string) (int, error) {
	return f.createRoomID, f.createRoomErr
}

func (f *fakeRoomFunctionAdmin) RenameRoom(_ context.Context, _, _, _ string) error {
	return f.renameRoomErr
}

func (f *fakeRoomFunctionAdmin) DeleteRoom(_ context.Context, _, _ string) error {
	return f.deleteRoomErr
}

func (f *fakeRoomFunctionAdmin) CreateFunction(_ context.Context, _, _ string) (int, error) {
	return f.createFnID, f.createFnErr
}

func (f *fakeRoomFunctionAdmin) RenameFunction(_ context.Context, _, _, _ string) error {
	return f.renameFnErr
}

func (f *fakeRoomFunctionAdmin) DeleteFunction(_ context.Context, _, _ string) error {
	return f.deleteFnErr
}

// ============================================================
// CreateRoom
// ============================================================

func TestCreateRoom_ValidBody_Returns201WithIDAndName(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{createRoomID: 42}
	body := strings.NewReader(`{"name":"Wohnzimmer"}`)
	req := httptest.NewRequest(http.MethodPost, "/rooms", body)
	w := httptest.NewRecorder()

	CreateRoom(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if int(got["id"].(float64)) != 42 {
		t.Errorf("id = %v, want 42", got["id"])
	}
	if got["name"] != "Wohnzimmer" {
		t.Errorf("name = %v, want Wohnzimmer", got["name"])
	}
}

func TestCreateRoom_MissingName_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{}
	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/rooms", body)
	w := httptest.NewRecorder()

	CreateRoom(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateRoom_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"name":"Kueche"}`)
	req := httptest.NewRequest(http.MethodPost, "/rooms", body)
	w := httptest.NewRecorder()

	CreateRoom(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateRoom_RoomExists_Returns409(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{createRoomErr: hub.ErrRoomExists}
	body := strings.NewReader(`{"name":"Wohnzimmer"}`)
	req := httptest.NewRequest(http.MethodPost, "/rooms", body)
	w := httptest.NewRecorder()

	CreateRoom(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateRoom_NoRoomMutator_Returns503(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{createRoomErr: hub.ErrNoRoomMutator}
	body := strings.NewReader(`{"name":"Flur"}`)
	req := httptest.NewRequest(http.MethodPost, "/rooms", body)
	w := httptest.NewRecorder()

	CreateRoom(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateRoom_CentralAmbiguous_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{createRoomErr: hub.ErrCentralAmbiguous}
	body := strings.NewReader(`{"name":"Buero"}`)
	req := httptest.NewRequest(http.MethodPost, "/rooms", body)
	w := httptest.NewRecorder()

	CreateRoom(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateRoom_GenericServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{createRoomErr: errors.New("rega failure")}
	body := strings.NewReader(`{"name":"Keller"}`)
	req := httptest.NewRequest(http.MethodPost, "/rooms", body)
	w := httptest.NewRecorder()

	CreateRoom(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// ============================================================
// RenameRoom
// ============================================================

func TestRenameRoom_ValidBody_Returns204(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{}
	body := strings.NewReader(`{"new_name":"Esszimmer"}`)
	req := httptest.NewRequest(http.MethodPatch, "/rooms/Wohnzimmer", body)
	req = withChiParam(req, "name", "Wohnzimmer")
	w := httptest.NewRecorder()

	RenameRoom(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRenameRoom_MissingNewName_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{}
	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPatch, "/rooms/Wohnzimmer", body)
	req = withChiParam(req, "name", "Wohnzimmer")
	w := httptest.NewRecorder()

	RenameRoom(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRenameRoom_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"new_name":"Esszimmer"}`)
	req := httptest.NewRequest(http.MethodPatch, "/rooms/Wohnzimmer", body)
	req = withChiParam(req, "name", "Wohnzimmer")
	w := httptest.NewRecorder()

	RenameRoom(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRenameRoom_RoomNotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{renameRoomErr: hub.ErrRoomNotFound}
	body := strings.NewReader(`{"new_name":"Esszimmer"}`)
	req := httptest.NewRequest(http.MethodPatch, "/rooms/OldRoom", body)
	req = withChiParam(req, "name", "OldRoom")
	w := httptest.NewRecorder()

	RenameRoom(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// ============================================================
// DeleteRoom
// ============================================================

func TestDeleteRoom_Success_Returns204(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{}
	req := httptest.NewRequest(http.MethodDelete, "/rooms/Wohnzimmer?central=ccu1", http.NoBody)
	req = withChiParam(req, "name", "Wohnzimmer")
	w := httptest.NewRecorder()

	DeleteRoom(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteRoom_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/rooms/X", http.NoBody)
	req = withChiParam(req, "name", "X")
	w := httptest.NewRecorder()

	DeleteRoom(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteRoom_RoomNotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{deleteRoomErr: hub.ErrRoomNotFound}
	req := httptest.NewRequest(http.MethodDelete, "/rooms/Ghost", http.NoBody)
	req = withChiParam(req, "name", "Ghost")
	w := httptest.NewRecorder()

	DeleteRoom(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// ============================================================
// CreateFunction
// ============================================================

func TestCreateFunction_ValidBody_Returns201WithIDAndName(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{createFnID: 17}
	body := strings.NewReader(`{"name":"Licht"}`)
	req := httptest.NewRequest(http.MethodPost, "/functions", body)
	w := httptest.NewRecorder()

	CreateFunction(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if int(got["id"].(float64)) != 17 {
		t.Errorf("id = %v, want 17", got["id"])
	}
	if got["name"] != "Licht" {
		t.Errorf("name = %v, want Licht", got["name"])
	}
}

func TestCreateFunction_FunctionExists_Returns409(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{createFnErr: hub.ErrFunctionExists}
	body := strings.NewReader(`{"name":"Licht"}`)
	req := httptest.NewRequest(http.MethodPost, "/functions", body)
	w := httptest.NewRecorder()

	CreateFunction(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateFunction_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"name":"Heizung"}`)
	req := httptest.NewRequest(http.MethodPost, "/functions", body)
	w := httptest.NewRecorder()

	CreateFunction(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateFunction_MissingName_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{}
	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/functions", body)
	w := httptest.NewRecorder()

	CreateFunction(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// ============================================================
// DeleteFunction
// ============================================================

func TestDeleteFunction_Success_Returns204(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{}
	req := httptest.NewRequest(http.MethodDelete, "/functions/Licht?central=ccu1", http.NoBody)
	req = withChiParam(req, "name", "Licht")
	w := httptest.NewRecorder()

	DeleteFunction(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteFunction_FunctionNotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{deleteFnErr: hub.ErrFunctionNotFound}
	req := httptest.NewRequest(http.MethodDelete, "/functions/Ghost", http.NoBody)
	req = withChiParam(req, "name", "Ghost")
	w := httptest.NewRecorder()

	DeleteFunction(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// ============================================================
// RenameFunction
// ============================================================

func TestRenameFunction_ValidBody_Returns204(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{}
	body := strings.NewReader(`{"new_name":"Heizung"}`)
	req := httptest.NewRequest(http.MethodPatch, "/functions/Licht", body)
	req = withChiParam(req, "name", "Licht")
	w := httptest.NewRecorder()

	RenameFunction(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRenameFunction_FunctionNotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{renameFnErr: hub.ErrFunctionNotFound}
	body := strings.NewReader(`{"new_name":"Heizung"}`)
	req := httptest.NewRequest(http.MethodPatch, "/functions/Missing", body)
	req = withChiParam(req, "name", "Missing")
	w := httptest.NewRecorder()

	RenameFunction(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// ============================================================
// writeGroupError — remaining sentinel mappings
// ============================================================

func TestWriteGroupError_CentralNotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{deleteRoomErr: hub.ErrCentralNotFound}
	req := httptest.NewRequest(http.MethodDelete, "/rooms/X", http.NoBody)
	req = withChiParam(req, "name", "X")
	w := httptest.NewRecorder()

	DeleteRoom(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for ErrCentralNotFound, got %d", w.Code)
	}
}

func TestWriteGroupError_NoFunctionMutator_Returns503(t *testing.T) {
	t.Parallel()
	svc := &fakeRoomFunctionAdmin{createFnErr: hub.ErrNoFunctionMutator}
	body := strings.NewReader(`{"name":"Heizung"}`)
	req := httptest.NewRequest(http.MethodPost, "/functions", body)
	w := httptest.NewRecorder()

	CreateFunction(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for ErrNoFunctionMutator, got %d", w.Code)
	}
}
