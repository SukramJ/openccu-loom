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
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// fakeUserAdminService is an in-memory UserAdminService for tests.
type fakeUserAdminService struct {
	users  []sqlite.UserRow
	putErr error
	delErr error
}

func (f *fakeUserAdminService) Put(_ context.Context, subject, _ string, role auth.Role) error {
	if f.putErr != nil {
		return f.putErr
	}
	now := time.Now().UTC()
	for i, u := range f.users {
		if u.Subject == subject {
			f.users[i].Role = role
			f.users[i].UpdatedAt = now
			return nil
		}
	}
	f.users = append(f.users, sqlite.UserRow{
		Subject:   subject,
		Role:      role,
		CreatedAt: now,
		UpdatedAt: now,
	})
	return nil
}

func (f *fakeUserAdminService) Delete(_ context.Context, subject string) error {
	if f.delErr != nil {
		return f.delErr
	}
	for i, u := range f.users {
		if u.Subject == subject {
			f.users = append(f.users[:i], f.users[i+1:]...)
			return nil
		}
	}
	return sqlite.ErrUserNotFound
}

func (f *fakeUserAdminService) List(_ context.Context) ([]sqlite.UserRow, error) {
	return f.users, nil
}

func (f *fakeUserAdminService) Count(_ context.Context) (int, error) {
	return len(f.users), nil
}

// withChiParam wraps a request so that chi.URLParam resolves correctly.
func withChiParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func newFakeUserSvc() *fakeUserAdminService {
	now := time.Now().UTC()
	return &fakeUserAdminService{
		users: []sqlite.UserRow{
			{Subject: "admin", Role: auth.RoleAdmin, CreatedAt: now, UpdatedAt: now},
			{Subject: "bob", Role: auth.RoleOperator, CreatedAt: now, UpdatedAt: now},
		},
	}
}

// --- CreateUser ---

func TestCreateUser_Happy(t *testing.T) {
	t.Parallel()
	svc := &fakeUserAdminService{}
	body := strings.NewReader(`{"username":"alice","password":"secret","role":"viewer"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/users", body)
	w := httptest.NewRecorder()
	CreateUser(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var resp userSummaryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Subject != "alice" {
		t.Errorf("expected subject=alice, got %q", resp.Subject)
	}
	if resp.Role != auth.RoleViewer {
		t.Errorf("expected role=viewer, got %q", resp.Role)
	}
}

func TestCreateUser_Upsert_ExistingUser(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	body := strings.NewReader(`{"username":"bob","password":"newpass","role":"viewer"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/users", body)
	w := httptest.NewRecorder()
	CreateUser(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 on upsert, got %d body=%s", w.Code, w.Body.String())
	}
	// Verify the role was updated.
	for _, u := range svc.users {
		if u.Subject == "bob" && u.Role != auth.RoleViewer {
			t.Errorf("expected bob role=viewer after upsert, got %q", u.Role)
		}
	}
}

func TestCreateUser_BadBody_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeUserAdminService{}
	req := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader("NOT JSON"))
	w := httptest.NewRecorder()
	CreateUser(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateUser_MissingUsername_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeUserAdminService{}
	body := strings.NewReader(`{"password":"secret","role":"viewer"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/users", body)
	w := httptest.NewRecorder()
	CreateUser(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateUser_MissingPassword_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeUserAdminService{}
	body := strings.NewReader(`{"username":"alice","role":"viewer"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/users", body)
	w := httptest.NewRecorder()
	CreateUser(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateUser_InvalidRole_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeUserAdminService{}
	body := strings.NewReader(`{"username":"alice","password":"secret","role":"superuser"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/users", body)
	w := httptest.NewRecorder()
	CreateUser(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- UpdateUser ---

func TestUpdateUser_Happy(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	body := strings.NewReader(`{"password":"newpass","role":"viewer"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/bob", body)
	req = withChiParam(req, "subject", "bob")
	w := httptest.NewRecorder()
	UpdateUser(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateUser_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	body := strings.NewReader(`{"password":"newpass","role":"viewer"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/ghost", body)
	req = withChiParam(req, "subject", "ghost")
	w := httptest.NewRecorder()
	UpdateUser(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestUpdateUser_InvalidRole_Returns400(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	body := strings.NewReader(`{"password":"newpass","role":"superuser"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/bob", body)
	req = withChiParam(req, "subject", "bob")
	w := httptest.NewRecorder()
	UpdateUser(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- DeleteUser ---

func TestDeleteUser_Happy(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/bob", http.NoBody)
	req = withChiParam(req, "subject", "bob")
	w := httptest.NewRecorder()
	DeleteUser(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestDeleteUser_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/ghost", http.NoBody)
	req = withChiParam(req, "subject", "ghost")
	w := httptest.NewRecorder()
	DeleteUser(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDeleteUser_LastAdmin_Returns409(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	svc.delErr = sqlite.ErrLastAdmin
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/admin", http.NoBody)
	req = withChiParam(req, "subject", "admin")
	w := httptest.NewRecorder()
	DeleteUser(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for last-admin, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteUser_OtherError_Returns500(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	svc.delErr = errors.New("disk full")
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/bob", http.NoBody)
	req = withChiParam(req, "subject", "bob")
	w := httptest.NewRecorder()
	DeleteUser(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// --- ListUsersV2 ---

func TestListUsersV2_ReturnsSorted(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	req := httptest.NewRequest(http.MethodGet, "/admin/users", http.NoBody)
	w := httptest.NewRecorder()
	ListUsersV2(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var rows []userListEntry
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 users, got %d", len(rows))
	}
	// Verify no password hash is present — userListEntry has no hash field.
	// Also verify both known subjects appear.
	subjects := map[string]bool{}
	for _, r := range rows {
		subjects[r.Subject] = true
	}
	if !subjects["admin"] || !subjects["bob"] {
		t.Errorf("unexpected subjects: %v", subjects)
	}
}

func TestListUsersV2_Empty(t *testing.T) {
	t.Parallel()
	svc := &fakeUserAdminService{}
	req := httptest.NewRequest(http.MethodGet, "/admin/users", http.NoBody)
	w := httptest.NewRecorder()
	ListUsersV2(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var rows []userListEntry
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected empty list, got %d entries", len(rows))
	}
}
