// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// fakeCentralAdminService is an in-memory CentralAdminService for tests.
type fakeCentralAdminService struct {
	centrals map[string]sqlite.CentralRow
	putErr   error
	delErr   error
}

func newFakeCentralSvc() *fakeCentralAdminService {
	now := time.Now().UTC()
	return &fakeCentralAdminService{
		centrals: map[string]sqlite.CentralRow{
			"home": {
				Name:      "home",
				Host:      "192.168.1.10",
				Enabled:   true,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}
}

func (f *fakeCentralAdminService) Put(_ context.Context, row sqlite.CentralRow) error {
	if f.putErr != nil {
		return f.putErr
	}
	if f.centrals == nil {
		f.centrals = map[string]sqlite.CentralRow{}
	}
	now := time.Now().UTC()
	if existing, ok := f.centrals[row.Name]; ok {
		row.CreatedAt = existing.CreatedAt
	} else {
		row.CreatedAt = now
	}
	row.UpdatedAt = now
	f.centrals[row.Name] = row
	return nil
}

func (f *fakeCentralAdminService) Get(_ context.Context, name string) (sqlite.CentralRow, error) {
	row, ok := f.centrals[name]
	if !ok {
		return sqlite.CentralRow{}, sqlite.ErrCentralNotFound
	}
	return row, nil
}

func (f *fakeCentralAdminService) Delete(_ context.Context, name string) error {
	if f.delErr != nil {
		return f.delErr
	}
	if _, ok := f.centrals[name]; !ok {
		return sqlite.ErrCentralNotFound
	}
	delete(f.centrals, name)
	return nil
}

func (f *fakeCentralAdminService) List(_ context.Context) ([]sqlite.CentralRow, error) {
	out := make([]sqlite.CentralRow, 0, len(f.centrals))
	for k := range f.centrals {
		row := f.centrals[k]
		out = append(out, row)
	}
	return out, nil
}

// --- ListCentrals ---

func TestListCentrals_Happy(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	req := httptest.NewRequest(http.MethodGet, "/admin/centrals", http.NoBody)
	w := httptest.NewRecorder()
	ListCentrals(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var rows []sqlite.CentralRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 central, got %d", len(rows))
	}
}

func TestListCentrals_Empty(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{centrals: map[string]sqlite.CentralRow{}}
	req := httptest.NewRequest(http.MethodGet, "/admin/centrals", http.NoBody)
	w := httptest.NewRecorder()
	ListCentrals(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var rows []sqlite.CentralRow
	_ = json.Unmarshal(w.Body.Bytes(), &rows)
	if len(rows) != 0 {
		t.Errorf("expected empty list, got %d", len(rows))
	}
}

// --- GetCentral ---

func TestGetCentral_Happy(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	req := httptest.NewRequest(http.MethodGet, "/admin/centrals/home", http.NoBody)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	GetCentral(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var row sqlite.CentralRow
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if row.Name != "home" {
		t.Errorf("expected name=home, got %q", row.Name)
	}
}

func TestGetCentral_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	req := httptest.NewRequest(http.MethodGet, "/admin/centrals/unknown", http.NoBody)
	req = withChiParam(req, "name", "unknown")
	w := httptest.NewRecorder()
	GetCentral(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- CreateCentral ---

func TestCreateCentral_Happy(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{}
	body := strings.NewReader(`{"Name":"office","Host":"10.0.0.5","Enabled":true}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/centrals", body)
	w := httptest.NewRecorder()
	CreateCentral(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var row sqlite.CentralRow
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if row.Name != "office" {
		t.Errorf("expected name=office, got %q", row.Name)
	}
}

func TestCreateCentral_BadBody_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{}
	req := httptest.NewRequest(http.MethodPost, "/admin/centrals", strings.NewReader("NOT JSON"))
	w := httptest.NewRecorder()
	CreateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateCentral_MissingName_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{}
	body := strings.NewReader(`{"Host":"10.0.0.5"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/centrals", body)
	w := httptest.NewRecorder()
	CreateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when name missing, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateCentral_MissingHost_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{}
	body := strings.NewReader(`{"Name":"office"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/centrals", body)
	w := httptest.NewRecorder()
	CreateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when host missing, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- UpdateCentral ---

func TestUpdateCentral_Happy(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	body := strings.NewReader(`{"Host":"192.168.1.99","Enabled":false}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/centrals/home", body)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	UpdateCentral(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	// Verify the update was applied.
	if updated := svc.centrals["home"]; updated.Host != "192.168.1.99" {
		t.Errorf("expected host=192.168.1.99 after update, got %q", updated.Host)
	}
}

func TestUpdateCentral_MissingHost_Returns400(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	body := strings.NewReader(`{"Enabled":true}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/centrals/home", body)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	UpdateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- DeleteCentral ---

func TestDeleteCentral_Happy(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	req := httptest.NewRequest(http.MethodDelete, "/admin/centrals/home", http.NoBody)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	DeleteCentral(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if _, ok := svc.centrals["home"]; ok {
		t.Error("expected home to be deleted")
	}
}

func TestDeleteCentral_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	req := httptest.NewRequest(http.MethodDelete, "/admin/centrals/ghost", http.NoBody)
	req = withChiParam(req, "name", "ghost")
	w := httptest.NewRecorder()
	DeleteCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
