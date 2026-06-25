// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/i18n"
)

func newUIWithI18n(t *testing.T) http.Handler {
	t.Helper()
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("catalogs: %v", err)
	}
	return NewRouter(Deps{Lang: "de", Catalogs: cats})
}

func TestUITranslatesNavToGerman(t *testing.T) {
	h := newUIWithI18n(t)
	req := httptest.NewRequest(http.MethodGet, "/about", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Systemzustand", "Info", "Anmelden"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q\nbody:\n%s", want, body)
		}
	}
}

func TestUISetupPage(t *testing.T) {
	h := newUIWithI18n(t)
	req := httptest.NewRequest(http.MethodGet, "/setup", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "openccu-loom") {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestUILoginPage(t *testing.T) {
	h := newUIWithI18n(t)
	req := httptest.NewRequest(http.MethodGet, "/login?error=1", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Anmeldedaten") {
		t.Fatalf("login body: %s", rr.Body.String())
	}
}

func TestUILayoutIncludesCSRFMeta(t *testing.T) {
	h := newUIWithI18n(t)
	req := httptest.NewRequest(http.MethodGet, "/about", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), `name="csrf-token"`) {
		t.Fatalf("csrf meta missing")
	}
}

func TestUILayoutLinksToSPA(t *testing.T) {
	h := newUIWithI18n(t)
	req := httptest.NewRequest(http.MethodGet, "/about", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), `href="app/"`) {
		t.Fatalf("SPA link missing in layout")
	}
}

// ---------------------------------------------------------------------------
// render — unknown template name returns 500
// ---------------------------------------------------------------------------

func TestRenderUnknownTemplateName500(t *testing.T) {
	t.Parallel()
	cats, _ := i18n.NewCatalogs()
	tpl := mustParseTemplates(cats, "en")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	// "no-such-template.html" does not exist in the templateSet.
	render(tpl, rr, req, "no-such-template.html", pageData{})
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for unknown template, got %d", rr.Code)
	}
}

// TestRenderWithIdentityInContext exercises the auth.IdentityFrom branch
// (line 184-186 of server.go) that sets data.Identity from the request context.
func TestRenderWithIdentityInContext(t *testing.T) {
	t.Parallel()
	cats, _ := i18n.NewCatalogs()
	tpl := mustParseTemplates(cats, "en")

	// Inject an identity into the context.
	ctx := auth.ContextWithIdentity(context.Background(), auth.Identity{Subject: "carol", Role: auth.RoleAdmin})
	req := httptest.NewRequest(http.MethodGet, "/about", http.NoBody).WithContext(ctx)
	rr := httptest.NewRecorder()
	render(tpl, rr, req, "about.html", pageData{Title: "About", Lang: "en"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with identity, got %d body=%s", rr.Code, rr.Body.String())
	}
}
