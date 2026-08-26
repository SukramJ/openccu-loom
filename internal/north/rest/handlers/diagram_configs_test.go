// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

type stubDiagramService struct {
	list       []DiagramConfig
	one        DiagramConfig
	getErr     error
	createErr  error
	updateErr  error
	deleteErr  error
	created    DiagramConfig
	lastAdmin  bool
	deleteCall int
}

func (s *stubDiagramService) List(context.Context, string) ([]DiagramConfig, error) {
	return s.list, nil
}

func (s *stubDiagramService) Get(_ context.Context, _, _ string, isAdmin bool) (DiagramConfig, error) {
	s.lastAdmin = isAdmin
	return s.one, s.getErr
}

func (s *stubDiagramService) Create(_ context.Context, subject, name, visibility, cfg string) (DiagramConfig, error) {
	s.created = DiagramConfig{ID: "new", OwnerSubject: subject, Name: name, Visibility: visibility, ConfigJSON: cfg}
	return s.created, s.createErr
}

func (s *stubDiagramService) Update(_ context.Context, id, _ string, _ bool, name, visibility, cfg string) (DiagramConfig, error) {
	return DiagramConfig{ID: id, Name: name, Visibility: visibility, ConfigJSON: cfg}, s.updateErr
}

func (s *stubDiagramService) Delete(context.Context, string, string, bool) error {
	s.deleteCall++
	return s.deleteErr
}

func withUser(r *http.Request, subject string, role auth.Role) *http.Request {
	return r.WithContext(auth.ContextWithIdentity(r.Context(), auth.Identity{Subject: subject, Role: role}))
}

func TestListDiagrams_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubDiagramService{list: []DiagramConfig{{ID: "1", Name: "A", ConfigJSON: `{"series":[]}`}}}
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/v1/diagrams", http.NoBody), "alice", auth.RoleOperator)
	w := httptest.NewRecorder()
	ListDiagrams(svc).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body struct {
		Diagrams []diagramResponse `json:"diagrams"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Diagrams) != 1 || body.Diagrams[0].ID != "1" {
		t.Fatalf("body = %+v", body)
	}
}

func TestListDiagrams_Unauthenticated_Returns401(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagrams", http.NoBody)
	w := httptest.NewRecorder()
	ListDiagrams(&stubDiagramService{}).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestGetDiagram_ForwardsAdminFlag_And404(t *testing.T) {
	t.Parallel()
	svc := &stubDiagramService{getErr: ErrDiagramNotFound}
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/v1/diagrams/x", http.NoBody), "alice", auth.RoleAdmin)
	req = req.WithContext(chiContext(req, map[string]string{"id": "x"}))
	w := httptest.NewRecorder()
	GetDiagram(svc).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if !svc.lastAdmin {
		t.Error("admin flag not forwarded")
	}
}

func TestGetDiagram_Forbidden_Returns403(t *testing.T) {
	t.Parallel()
	svc := &stubDiagramService{getErr: ErrDiagramForbidden}
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/v1/diagrams/x", http.NoBody), "bob", auth.RoleOperator)
	req = req.WithContext(chiContext(req, map[string]string{"id": "x"}))
	w := httptest.NewRecorder()
	GetDiagram(svc).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestCreateDiagram_HappyPath_Returns201AndAudits(t *testing.T) {
	t.Parallel()
	svc := &stubDiagramService{}
	rec := &captureRecorder{}
	body := `{"name":"Living","visibility":"private","config":{"series":[{"central":"ccu1"}]}}`
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/v1/diagrams", strings.NewReader(body)), "alice", auth.RoleOperator)
	w := httptest.NewRecorder()
	CreateDiagram(svc, rec).ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.created.Name != "Living" || svc.created.OwnerSubject != "alice" {
		t.Errorf("create args wrong: %+v", svc.created)
	}
	if len(rec.entries) != 1 {
		t.Errorf("expected one audit entry, got %d", len(rec.entries))
	}
}

func TestCreateDiagram_Invalid_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubDiagramService{createErr: ErrDiagramInvalid}
	body := `{"name":""}`
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/v1/diagrams", strings.NewReader(body)), "alice", auth.RoleOperator)
	w := httptest.NewRecorder()
	CreateDiagram(svc, nil).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestDeleteDiagram_HappyPath_Returns204(t *testing.T) {
	t.Parallel()
	svc := &stubDiagramService{}
	req := withUser(httptest.NewRequest(http.MethodDelete, "/api/v1/diagrams/x", http.NoBody), "alice", auth.RoleOperator)
	req = req.WithContext(chiContext(req, map[string]string{"id": "x"}))
	w := httptest.NewRecorder()
	DeleteDiagram(svc, &captureRecorder{}).ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if svc.deleteCall != 1 {
		t.Error("delete not called")
	}
}

func TestDiagram_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagrams", http.NoBody)
	w := httptest.NewRecorder()
	ListDiagrams(nil).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}
