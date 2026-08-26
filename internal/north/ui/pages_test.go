// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	for _, want := range []string{"Systemzustand", "Info"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q\nbody:\n%s", want, body)
		}
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
