// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

type stubEditorComponent struct{ m hmapi.EditorComponentManifest }

func (s stubEditorComponent) Manifest() hmapi.EditorComponentManifest { return s.m }

func TestGetEditorComponentAnswersTheManifest(t *testing.T) {
	want := hmapi.EditorComponentManifest{
		ModuleURL: "/app/component/editor-ab.js",
		Hash:      "ab",
		Routes:    []hmapi.EditorRoute{{Method: "GET", Prefix: "/devices"}},
	}
	rec := httptest.NewRecorder()
	GetEditorComponent(stubEditorComponent{want})(rec, httptest.NewRequest(http.MethodGet, "/api/v1/ui/editor-component", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q", got)
	}
	var got hmapi.EditorComponentManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ModuleURL != want.ModuleURL || got.Hash != want.Hash || len(got.Routes) != 1 || got.Routes[0] != want.Routes[0] {
		t.Fatalf("manifest = %+v", got)
	}
}

func TestGetEditorComponentWithoutBundleIsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	GetEditorComponent(nil)(rec, httptest.NewRequest(http.MethodGet, "/api/v1/ui/editor-component", http.NoBody))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}
