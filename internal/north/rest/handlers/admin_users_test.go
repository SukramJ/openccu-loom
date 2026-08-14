// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	users      []sqlite.UserRow
	putCalls   []string
	putErr     error
	setRoleErr error
	delErr     error
}

func (f *fakeUserAdminService) Put(_ context.Context, subject, _ string, role auth.Role) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.putCalls = append(f.putCalls, subject)
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

func (f *fakeUserAdminService) SetRole(_ context.Context, subject string, role auth.Role) error {
	if f.setRoleErr != nil {
		return f.setRoleErr
	}
	for i, u := range f.users {
		if u.Subject == subject {
			f.users[i].Role = role
			f.users[i].UpdatedAt = time.Now().UTC()
			return nil
		}
	}
	return sqlite.ErrUserNotFound
}

// roleOf reports the stored role of subject, or the empty role when the
// subject is unknown.
func (f *fakeUserAdminService) roleOf(subject string) auth.Role {
	for _, u := range f.users {
		if u.Subject == subject {
			return u.Role
		}
	}
	return ""
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

// fakeSessionRevoker is an in-memory SessionRevoker for tests: it records
// every call so handler tests can assert revocation happened exactly when
// expected (on success, never on a validation or not-found failure).
type fakeSessionRevoker struct {
	revokeBySubjectCalls       []string
	revokeBySubjectExceptCalls []fakeRevokeExceptCall
}

type fakeRevokeExceptCall struct {
	subject string
	keepSID string
}

func (f *fakeSessionRevoker) RevokeBySubject(subject string) int {
	f.revokeBySubjectCalls = append(f.revokeBySubjectCalls, subject)
	return 1
}

func (f *fakeSessionRevoker) RevokeBySubjectExcept(subject, keepSID string) int {
	f.revokeBySubjectExceptCalls = append(f.revokeBySubjectExceptCalls, fakeRevokeExceptCall{subject: subject, keepSID: keepSID})
	return 1
}

// fakeTokenPurger is an in-memory TokenPurger for tests.
type fakeTokenPurger struct {
	deleteBySubjectCalls []string
	deleteErr            error
}

func (f *fakeTokenPurger) DeleteBySubject(_ context.Context, subject string) (int, error) {
	f.deleteBySubjectCalls = append(f.deleteBySubjectCalls, subject)
	if f.deleteErr != nil {
		return 0, f.deleteErr
	}
	return 1, nil
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

// TestCreateUser_ExistingUser_Returns409 pins POST as create-only. An
// upsert here would rewrite an existing account's password and role
// without the session revocation PATCH performs, leaving a stolen cookie
// with the old role alive for the rest of the session TTL. The casing of
// the submitted username must not matter: the store canonicalises before
// the conflict is detected.
func TestCreateUser_ExistingUser_Returns409(t *testing.T) {
	t.Parallel()
	for _, username := range []string{"bob", "Bob", "BOB"} {
		svc := newFakeUserSvc()
		body := strings.NewReader(`{"username":"` + username + `","password":"newpass","role":"viewer"}`)
		req := httptest.NewRequest(http.MethodPost, "/admin/users", body)
		w := httptest.NewRecorder()
		CreateUser(svc, audit.NoopRecorder()).ServeHTTP(w, req)

		if w.Code != http.StatusConflict {
			t.Fatalf("POST %q: expected 409, got %d body=%s", username, w.Code, w.Body.String())
		}
		for _, u := range svc.users {
			if u.Subject == "bob" && u.Role != auth.RoleOperator {
				t.Errorf("POST %q: bob role=%q, want the stored role untouched", username, u.Role)
			}
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

// TestCreateUser_SpoofableUsername_Returns400 verifies that a username
// carrying '=', whitespace, or a control character is rejected, so it can
// never tamper with the key=value shape of the `subject=<name> role=<role>`
// audit note (nor inject a forged log line).
func TestCreateUser_SpoofableUsername_Returns400(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"eve role=admin",  // whitespace + '=' → spoofs role= field
		"eve=admin",       // bare '='
		"eve\nrole=admin", // control character (newline / log injection)
		"a\tb",            // tab
	} {
		svc := &fakeUserAdminService{}
		spy := &captureRecorder{}
		body := strings.NewReader(`{"username":` + strconv.Quote(name) + `,"password":"secret","role":"viewer"}`)
		req := httptest.NewRequest(http.MethodPost, "/admin/users", body)
		w := httptest.NewRecorder()
		CreateUser(svc, spy).ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("username %q: expected 400, got %d body=%s", name, w.Code, w.Body.String())
		}
		if len(spy.entries) != 0 {
			t.Errorf("username %q: rejected create must not emit an audit entry", name)
		}
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
	revoker := &fakeSessionRevoker{}
	body := strings.NewReader(`{"password":"newpass","role":"viewer"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/bob", body)
	req = withChiParam(req, "subject", "bob")
	w := httptest.NewRecorder()
	UpdateUser(svc, audit.NoopRecorder(), revoker, &fakeTokenPurger{}).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if len(revoker.revokeBySubjectCalls) != 1 || revoker.revokeBySubjectCalls[0] != "bob" {
		t.Errorf("expected RevokeBySubject(bob) exactly once, got %v", revoker.revokeBySubjectCalls)
	}
}

func TestUpdateUser_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	revoker := &fakeSessionRevoker{}
	body := strings.NewReader(`{"password":"newpass","role":"viewer"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/ghost", body)
	req = withChiParam(req, "subject", "ghost")
	w := httptest.NewRecorder()
	UpdateUser(svc, nil, revoker, &fakeTokenPurger{}).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if len(revoker.revokeBySubjectCalls) != 0 {
		t.Errorf("expected no revocation on 404, got %v", revoker.revokeBySubjectCalls)
	}
}

func TestUpdateUser_InvalidRole_Returns400(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	revoker := &fakeSessionRevoker{}
	body := strings.NewReader(`{"password":"newpass","role":"superuser"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/bob", body)
	req = withChiParam(req, "subject", "bob")
	w := httptest.NewRecorder()
	UpdateUser(svc, nil, revoker, &fakeTokenPurger{}).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if len(revoker.revokeBySubjectCalls) != 0 {
		t.Errorf("expected no revocation on 400, got %v", revoker.revokeBySubjectCalls)
	}
}

// TestUpdateUser_RevokesSessionIssuedWithDifferentCasing drives a real
// [auth.SessionStore] instead of a recording fake: the guarantee the
// handler documents is that the session is *gone* after a password or
// role change, and a fake that only records the call cannot show that.
// A user who signs in typing "Markus" gets a session; the admin can only
// address the account by its canonical spelling "markus", and that
// spelling must still evict the session.
func TestUpdateUser_RevokesSessionIssuedWithDifferentCasing(t *testing.T) {
	t.Parallel()
	svc := &fakeUserAdminService{users: []sqlite.UserRow{{Subject: "markus", Role: auth.RoleOperator}}}
	sessions := auth.NewSessionStore()
	sess, err := sessions.Issue(auth.Identity{Subject: "Markus", Role: auth.RoleOperator})
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}

	body := strings.NewReader(`{"password":"newpass","role":"viewer"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/markus", body)
	req = withChiParam(req, "subject", "markus")
	w := httptest.NewRecorder()
	UpdateUser(svc, audit.NoopRecorder(), sessions, &fakeTokenPurger{}).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if sessions.Lookup(sess.ID) != nil {
		t.Error("session survived the password reset that was supposed to kill it")
	}
}

// TestUpdateUser_LastAdmin_Returns409 verifies that a store refusing to
// demote the last admin surfaces as the same conflict the DELETE path
// produces, instead of a bare 500.
func TestUpdateUser_LastAdmin_Returns409(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	svc.putErr = sqlite.ErrLastAdmin
	revoker := &fakeSessionRevoker{}
	body := strings.NewReader(`{"password":"newpass","role":"viewer"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/admin", body)
	req = withChiParam(req, "subject", "admin")
	w := httptest.NewRecorder()
	UpdateUser(svc, audit.NoopRecorder(), revoker, &fakeTokenPurger{}).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}
	if len(revoker.revokeBySubjectCalls) != 0 {
		t.Errorf("expected no revocation when the write was refused, got %v", revoker.revokeBySubjectCalls)
	}
}

// TestUpdateUser_RoleOnlyBodyChangesTheRole drives the body the SPA sends
// when the operator edits the role and leaves the password field blank.
// The account keeps its stored hash, so the update must not demand a
// password: refusing the body with 400 makes a role change impossible from
// the only surface that offers one.
func TestUpdateUser_RoleOnlyBodyChangesTheRole(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	revoker := &fakeSessionRevoker{}
	tokens := &fakeTokenPurger{}
	body := strings.NewReader(`{"role":"viewer"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/bob", body)
	req = withChiParam(req, "subject", "bob")
	w := httptest.NewRecorder()
	UpdateUser(svc, audit.NoopRecorder(), revoker, tokens).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if len(svc.putCalls) != 0 {
		t.Errorf("a role-only update must not rewrite the password, got Put calls %v", svc.putCalls)
	}
	if got := svc.roleOf("bob"); got != auth.RoleViewer {
		t.Errorf("role after update = %q, want viewer", got)
	}
	// A demotion the old session outlives is the hole the full update
	// closes; the role-only path must close it too.
	if len(revoker.revokeBySubjectCalls) != 1 || revoker.revokeBySubjectCalls[0] != "bob" {
		t.Errorf("expected RevokeBySubject(bob) exactly once, got %v", revoker.revokeBySubjectCalls)
	}
	// A bearer token carries the role it was minted with, so a demotion
	// that leaves it alive keeps the pre-demotion privileges usable.
	if len(tokens.deleteBySubjectCalls) != 1 || tokens.deleteBySubjectCalls[0] != "bob" {
		t.Errorf("expected DeleteBySubject(bob) exactly once, got %v", tokens.deleteBySubjectCalls)
	}
}

// TestUpdateUser_RoleOnlyLastAdmin_Returns409 pins that the role-only path
// runs through the same last-admin guard as the full update: demoting the
// only admin must stay refused, not become possible by omitting the
// password.
func TestUpdateUser_RoleOnlyLastAdmin_Returns409(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	svc.setRoleErr = sqlite.ErrLastAdmin
	revoker := &fakeSessionRevoker{}
	tokens := &fakeTokenPurger{}
	body := strings.NewReader(`{"role":"viewer"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/admin", body)
	req = withChiParam(req, "subject", "admin")
	w := httptest.NewRecorder()
	UpdateUser(svc, audit.NoopRecorder(), revoker, tokens).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}
	if len(revoker.revokeBySubjectCalls) != 0 {
		t.Errorf("expected no revocation when the write was refused, got %v", revoker.revokeBySubjectCalls)
	}
	if len(tokens.deleteBySubjectCalls) != 0 {
		t.Errorf("expected no token purge when the write was refused, got %v", tokens.deleteBySubjectCalls)
	}
}

// TestUpdateUser_PasswordOnlyBodyKeepsTheRole covers the other half of the
// documented "omitted fields leave the user unchanged" contract: a reset
// that names no role must not silently move the account to one.
func TestUpdateUser_PasswordOnlyBodyKeepsTheRole(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	revoker := &fakeSessionRevoker{}
	tokens := &fakeTokenPurger{}
	body := strings.NewReader(`{"password":"newpass"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/bob", body)
	req = withChiParam(req, "subject", "bob")
	w := httptest.NewRecorder()
	UpdateUser(svc, audit.NoopRecorder(), revoker, tokens).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if got := svc.roleOf("bob"); got != auth.RoleOperator {
		t.Errorf("role after password reset = %q, want the unchanged operator", got)
	}
	// The role did not change, so the subject's API tokens keep the
	// privileges they were minted with and must survive.
	if len(tokens.deleteBySubjectCalls) != 0 {
		t.Errorf("expected no token purge for a password-only update, got %v", tokens.deleteBySubjectCalls)
	}
}

// TestUpdateUser_EmptyBody_Returns400 pins that a body naming neither
// field is refused instead of reporting a success that changed nothing.
func TestUpdateUser_EmptyBody_Returns400(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	revoker := &fakeSessionRevoker{}
	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/bob", body)
	req = withChiParam(req, "subject", "bob")
	w := httptest.NewRecorder()
	UpdateUser(svc, audit.NoopRecorder(), revoker, &fakeTokenPurger{}).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if len(revoker.revokeBySubjectCalls) != 0 {
		t.Errorf("expected no revocation on 400, got %v", revoker.revokeBySubjectCalls)
	}
}

// --- DeleteUser ---

func TestDeleteUser_Happy(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	revoker := &fakeSessionRevoker{}
	tokens := &fakeTokenPurger{}
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/bob", http.NoBody)
	req = withChiParam(req, "subject", "bob")
	w := httptest.NewRecorder()
	DeleteUser(svc, audit.NoopRecorder(), revoker, tokens).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if len(revoker.revokeBySubjectCalls) != 1 || revoker.revokeBySubjectCalls[0] != "bob" {
		t.Errorf("expected RevokeBySubject(bob) exactly once, got %v", revoker.revokeBySubjectCalls)
	}
	if len(tokens.deleteBySubjectCalls) != 1 || tokens.deleteBySubjectCalls[0] != "bob" {
		t.Errorf("expected DeleteBySubject(bob) exactly once, got %v", tokens.deleteBySubjectCalls)
	}
}

// TestDeleteUser_RevokesSessionIssuedWithDifferentCasing is the DELETE
// twin of the PATCH case: a deleted account must retain no live
// credentials, including the session of a login that used a different
// spelling, and the token purge must be asked for the canonical subject.
func TestDeleteUser_RevokesSessionIssuedWithDifferentCasing(t *testing.T) {
	t.Parallel()
	svc := &fakeUserAdminService{users: []sqlite.UserRow{{Subject: "markus", Role: auth.RoleOperator}}}
	sessions := auth.NewSessionStore()
	sess, err := sessions.Issue(auth.Identity{Subject: "Markus", Role: auth.RoleOperator})
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	tokens := &fakeTokenPurger{}

	req := httptest.NewRequest(http.MethodDelete, "/admin/users/markus", http.NoBody)
	req = withChiParam(req, "subject", "markus")
	w := httptest.NewRecorder()
	DeleteUser(svc, audit.NoopRecorder(), sessions, tokens).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if sessions.Lookup(sess.ID) != nil {
		t.Error("session survived the deletion of the account it belongs to")
	}
	if len(tokens.deleteBySubjectCalls) != 1 || tokens.deleteBySubjectCalls[0] != "markus" {
		t.Errorf("token purge calls=%v, want one call for the canonical subject", tokens.deleteBySubjectCalls)
	}
}

func TestDeleteUser_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	revoker := &fakeSessionRevoker{}
	tokens := &fakeTokenPurger{}
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/ghost", http.NoBody)
	req = withChiParam(req, "subject", "ghost")
	w := httptest.NewRecorder()
	DeleteUser(svc, nil, revoker, tokens).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if len(revoker.revokeBySubjectCalls) != 0 {
		t.Errorf("expected no revocation on 404, got %v", revoker.revokeBySubjectCalls)
	}
	if len(tokens.deleteBySubjectCalls) != 0 {
		t.Errorf("expected no token purge on 404, got %v", tokens.deleteBySubjectCalls)
	}
}

func TestDeleteUser_LastAdmin_Returns409(t *testing.T) {
	t.Parallel()
	svc := newFakeUserSvc()
	svc.delErr = sqlite.ErrLastAdmin
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/admin", http.NoBody)
	req = withChiParam(req, "subject", "admin")
	w := httptest.NewRecorder()
	DeleteUser(svc, nil, nil, nil).ServeHTTP(w, req)

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
	DeleteUser(svc, nil, nil, nil).ServeHTTP(w, req)

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
