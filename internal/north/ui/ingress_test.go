// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/i18n"
)

func TestIngressPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "no header",
			header: "",
			want:   "",
		},
		{
			name:   "valid ingress path",
			header: "/api/hassio_ingress/abc",
			want:   "/api/hassio_ingress/abc",
		},
		{
			name:   "trailing slash trimmed",
			header: "/api/hassio_ingress/abc/",
			want:   "/api/hassio_ingress/abc",
		},
		{
			name:   "double-slash open-redirect rejected",
			header: "//evil",
			want:   "",
		},
		{
			name:   "backslash open-redirect rejected",
			header: "/\\evil",
			want:   "",
		},
		{
			name:   "non-slash value rejected",
			header: "noslash",
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			if tc.header != "" {
				req.Header.Set("X-Ingress-Path", tc.header)
			}
			got := ingressPrefix(req)
			if got != tc.want {
				t.Errorf("ingressPrefix() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUIRedirectPrefixesLocation(t *testing.T) {
	t.Parallel()

	t.Run("with ingress prefix", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/login", http.NoBody)
		req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/abc")
		rr := httptest.NewRecorder()
		uiRedirect(rr, req, "/login")
		loc := rr.Header().Get("Location")
		if loc != "/api/hassio_ingress/abc/login" {
			t.Errorf("Location = %q, want %q", loc, "/api/hassio_ingress/abc/login")
		}
		if rr.Code != http.StatusSeeOther {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusSeeOther)
		}
	})

	t.Run("without ingress prefix", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/login", http.NoBody)
		rr := httptest.NewRecorder()
		uiRedirect(rr, req, "/login")
		loc := rr.Header().Get("Location")
		if loc != "/login" {
			t.Errorf("Location = %q, want %q", loc, "/login")
		}
	})
}

// TestRenderWithIngressPrefixEmitsBase verifies that a page rendered with
// X-Ingress-Path carries <base href="/api/hassio_ingress/abc/"> in the output,
// and that the login form action is relative ("login", no leading slash).
// Without the header the base must be <base href="/">.
func TestRenderWithIngressPrefixEmitsBase(t *testing.T) {
	t.Parallel()
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("i18n.NewCatalogs: %v", err)
	}
	tpl := mustParseTemplates(cats, "en")

	t.Run("with ingress prefix", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		data := pageData{
			Title:    "Sign in",
			Lang:     "en",
			BasePath: "/api/hassio_ingress/abc",
			Data: struct {
				Error       bool
				OIDCEnabled bool
				SetupDone   bool
			}{},
		}
		if err := tpl.pages["login.html"].ExecuteTemplate(&buf, "layout", data); err != nil {
			t.Fatalf("ExecuteTemplate: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, `<base href="/api/hassio_ingress/abc/">`) {
			t.Errorf("expected <base href=\"/api/hassio_ingress/abc/\"> in output; got:\n%s", out)
		}
		if !strings.Contains(out, `action="login"`) {
			t.Errorf("expected relative action=\"login\" in output; got:\n%s", out)
		}
		if strings.Contains(out, `action="/login"`) {
			t.Errorf("absolute action=\"/login\" must not appear; got:\n%s", out)
		}
	})

	t.Run("without ingress prefix", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		data := pageData{
			Title:    "Sign in",
			Lang:     "en",
			BasePath: "",
			Data: struct {
				Error       bool
				OIDCEnabled bool
				SetupDone   bool
			}{},
		}
		if err := tpl.pages["login.html"].ExecuteTemplate(&buf, "layout", data); err != nil {
			t.Fatalf("ExecuteTemplate: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, `<base href="/">`) {
			t.Errorf("expected <base href=\"/\"> in output; got:\n%s", out)
		}
	})
}
