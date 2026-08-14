// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// fakeSelfPasswordService is an in-memory SelfPasswordService for tests.
type fakeSelfPasswordService struct {
	// users maps subject → {password, role}
	users map[string]struct {
		password string
		role     auth.Role
	}
	putErr error
}

func (f *fakeSelfPasswordService) AuthenticateBasic(_ context.Context, username, password string) (auth.Identity, error) {
	u, ok := f.users[username]
	if !ok {
		return auth.Identity{}, sqlite.ErrUserNotFound
	}
	if u.password != password {
		return auth.Identity{}, errors.New("wrong password")
	}
	return auth.Identity{Subject: username, Role: u.role}, nil
}

func (f *fakeSelfPasswordService) Put(_ context.Context, subject, password string, role auth.Role) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.users[subject] = struct {
		password string
		role     auth.Role
	}{password: password, role: role}
	return nil
}

// newFakeSelfPasswordSvc returns a service with one user "alice" (operator).
func newFakeSelfPasswordSvc() *fakeSelfPasswordService {
	return &fakeSelfPasswordService{
		users: map[string]struct {
			password string
			role     auth.Role
		}{
			"alice": {password: "correct", role: auth.RoleOperator},
		},
	}
}

// withIdentity injects an auth.Identity into the request context.
func withIdentity(r *http.Request, id auth.Identity) *http.Request {
	return r.WithContext(auth.ContextWithIdentity(r.Context(), id))
}

// --- ChangeOwnPassword ---

func TestChangeOwnPassword_Success_Returns204(t *testing.T) {
	t.Parallel()
	svc := newFakeSelfPasswordSvc()
	revoker := &fakeSessionRevoker{}
	body := strings.NewReader(`{"current_password":"correct","new_password":"brandnew"}`)
	req := httptest.NewRequest(http.MethodPatch, "/auth/me/password", body)
	req = withIdentity(req, auth.Identity{Subject: "alice", Role: auth.RoleOperator})
	w := httptest.NewRecorder()

	ChangeOwnPassword(svc, audit.NoopRecorder(), revoker).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	// Verify the password was actually updated and the role was preserved.
	u, ok := svc.users["alice"]
	if !ok {
		t.Fatal("alice not found in service after change")
	}
	if u.password != "brandnew" {
		t.Errorf("expected new password=brandnew, got %q", u.password)
	}
	if u.role != auth.RoleOperator {
		t.Errorf("expected role to remain operator, got %q", u.role)
	}
	// No session cookie was set on the request, so the preserved session
	// id (keepSID) must be empty — every session for alice would be
	// revoked in that case since there is nothing to keep.
	if len(revoker.revokeBySubjectExceptCalls) != 1 {
		t.Fatalf("expected RevokeBySubjectExcept called once, got %d", len(revoker.revokeBySubjectExceptCalls))
	}
	call := revoker.revokeBySubjectExceptCalls[0]
	if call.subject != "alice" || call.keepSID != "" {
		t.Errorf("expected RevokeBySubjectExcept(alice, \"\"), got (%q, %q)", call.subject, call.keepSID)
	}
}

func TestChangeOwnPassword_WrongCurrentPassword_Returns403(t *testing.T) {
	t.Parallel()
	svc := newFakeSelfPasswordSvc()
	revoker := &fakeSessionRevoker{}
	body := strings.NewReader(`{"current_password":"wrong","new_password":"brandnew"}`)
	req := httptest.NewRequest(http.MethodPatch, "/auth/me/password", body)
	req = withIdentity(req, auth.Identity{Subject: "alice", Role: auth.RoleOperator})
	w := httptest.NewRecorder()

	ChangeOwnPassword(svc, nil, revoker).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
	if len(revoker.revokeBySubjectExceptCalls) != 0 {
		t.Errorf("expected no revocation on wrong-password failure, got %v", revoker.revokeBySubjectExceptCalls)
	}
}

// TestChangeOwnPassword_PreservesCallersSessionCookie verifies that when the
// request carries the caller's own session cookie, that session id is
// threaded through to RevokeBySubjectExcept as keepSID so the caller is not
// logged out by their own password change.
func TestChangeOwnPassword_PreservesCallersSessionCookie(t *testing.T) {
	t.Parallel()
	svc := newFakeSelfPasswordSvc()
	revoker := &fakeSessionRevoker{}
	body := strings.NewReader(`{"current_password":"correct","new_password":"brandnew"}`)
	req := httptest.NewRequest(http.MethodPatch, "/auth/me/password", body)
	req = withIdentity(req, auth.Identity{Subject: "alice", Role: auth.RoleOperator})
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "own-session-id"})
	w := httptest.NewRecorder()

	ChangeOwnPassword(svc, nil, revoker).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if len(revoker.revokeBySubjectExceptCalls) != 1 {
		t.Fatalf("expected RevokeBySubjectExcept called once, got %d", len(revoker.revokeBySubjectExceptCalls))
	}
	call := revoker.revokeBySubjectExceptCalls[0]
	if call.subject != "alice" || call.keepSID != "own-session-id" {
		t.Errorf("expected RevokeBySubjectExcept(alice, own-session-id), got (%q, %q)", call.subject, call.keepSID)
	}
}

func TestChangeOwnPassword_NoLocalPassword_Returns409(t *testing.T) {
	t.Parallel()
	// "ghost" has no row in the store → AuthenticateBasic returns ErrUserNotFound.
	svc := newFakeSelfPasswordSvc()
	body := strings.NewReader(`{"current_password":"any","new_password":"brandnew"}`)
	req := httptest.NewRequest(http.MethodPatch, "/auth/me/password", body)
	req = withIdentity(req, auth.Identity{Subject: "ghost", Role: auth.RoleOperator})
	w := httptest.NewRecorder()

	ChangeOwnPassword(svc, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestChangeOwnPassword_FederatedIdentity_Returns409 pins that a caller
// signed in through the external identity provider cannot rewrite the
// password of the local account whose name happens to fold to the same
// string. The two are different principals; the provider owns the
// credentials of the one holding this session.
func TestChangeOwnPassword_FederatedIdentity_Returns409(t *testing.T) {
	t.Parallel()
	svc := newFakeSelfPasswordSvc()
	revoker := &fakeSessionRevoker{}
	body := strings.NewReader(`{"current_password":"correct","new_password":"brandnew"}`)
	req := httptest.NewRequest(http.MethodPatch, "/auth/me/password", body)
	req = withIdentity(req, auth.Identity{Subject: "alice", Role: auth.RoleOperator, Scheme: auth.SchemeOIDC})
	w := httptest.NewRecorder()

	ChangeOwnPassword(svc, audit.NoopRecorder(), revoker).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.users["alice"].password != "correct" {
		t.Error("the local account's password was rewritten by a federated caller")
	}
	if len(revoker.revokeBySubjectExceptCalls) != 0 {
		t.Error("the local account's sessions were revoked by a federated caller")
	}
}

func TestChangeOwnPassword_NoIdentityInContext_Returns401(t *testing.T) {
	t.Parallel()
	svc := newFakeSelfPasswordSvc()
	body := strings.NewReader(`{"current_password":"correct","new_password":"brandnew"}`)
	req := httptest.NewRequest(http.MethodPatch, "/auth/me/password", body)
	// No identity injected.
	w := httptest.NewRecorder()

	ChangeOwnPassword(svc, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestChangeOwnPassword_EmptyCurrentPassword_Returns400(t *testing.T) {
	t.Parallel()
	svc := newFakeSelfPasswordSvc()
	body := strings.NewReader(`{"current_password":"","new_password":"brandnew"}`)
	req := httptest.NewRequest(http.MethodPatch, "/auth/me/password", body)
	req = withIdentity(req, auth.Identity{Subject: "alice", Role: auth.RoleOperator})
	w := httptest.NewRecorder()

	ChangeOwnPassword(svc, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestChangeOwnPassword_EmptyNewPassword_Returns400(t *testing.T) {
	t.Parallel()
	svc := newFakeSelfPasswordSvc()
	body := strings.NewReader(`{"current_password":"correct","new_password":""}`)
	req := httptest.NewRequest(http.MethodPatch, "/auth/me/password", body)
	req = withIdentity(req, auth.Identity{Subject: "alice", Role: auth.RoleOperator})
	w := httptest.NewRecorder()

	ChangeOwnPassword(svc, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestChangeOwnPassword_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"current_password":"correct","new_password":"brandnew"}`)
	req := httptest.NewRequest(http.MethodPatch, "/auth/me/password", body)
	req = withIdentity(req, auth.Identity{Subject: "alice", Role: auth.RoleOperator})
	w := httptest.NewRecorder()

	ChangeOwnPassword(nil, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestChangeOwnPassword_PutError_Returns500(t *testing.T) {
	t.Parallel()
	svc := newFakeSelfPasswordSvc()
	svc.putErr = errors.New("disk full")
	body := strings.NewReader(`{"current_password":"correct","new_password":"brandnew"}`)
	req := httptest.NewRequest(http.MethodPatch, "/auth/me/password", body)
	req = withIdentity(req, auth.Identity{Subject: "alice", Role: auth.RoleOperator})
	w := httptest.NewRecorder()

	ChangeOwnPassword(svc, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}
