// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// Deps collects every dependency the bootstrap UI needs. The HTMX
// surface is intentionally narrow — login/setup before the SPA can
// authenticate, plus a server-rendered /health and /about for the
// case where the SPA bundle fails to load.
type Deps struct {
	Logger   *slog.Logger
	Lang     string
	Health   handlers.HealthReader
	Catalogs *i18n.Catalogs
	Auth     *AuthDeps
	OIDC     *OIDCDeps
	// Setup wires the multi-step SQLite-backed wizard. When nil (or when
	// Setup.Users is nil), the router falls back to the legacy single-step
	// handler so test fixtures that do not wire SQLite continue to work.
	Setup       *SetupWizardDeps
	AuthResolve func(http.Handler) http.Handler
	AuthRequire func(http.Handler) http.Handler
}

// NewRouter builds the bootstrap UI router. The HTMX surface only
// covers what the Svelte SPA structurally cannot:
//
//	/                       — redirect to /health
//	/login, POST /login     — form login (basic auth)
//	POST /logout            — clears the session
//	/setup, POST /setup     — first-run admin bootstrap
//	/login/oidc/start       — begin OIDC PKCE flow
//	/login/oidc/callback    — finish OIDC PKCE flow
//	/health                 — server-rendered status (SPA-down fallback)
//	/about                  — version + license
//	/ui/assets/*            — embedded CSS
//
// Everything device-, program-, sysvar-, paramset-, incident-,
// settings-, user- or token-related lives in the Svelte SPA at
// /app/* on the REST server.
func NewRouter(d Deps) *chi.Mux {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Lang == "" {
		d.Lang = "en"
	}
	tpl := mustParseTemplates(d.Catalogs, d.Lang)
	loginRL := newLoginRateLimiter(loginRateBurst)

	r := chi.NewRouter()
	if d.AuthResolve != nil {
		r.Use(d.AuthResolve)
	}

	assetSub, err := fs.Sub(assetFS, "assets")
	if err != nil {
		panic(err)
	}
	r.Handle("/ui/assets/*", http.StripPrefix("/ui/assets/", http.FileServer(http.FS(assetSub))))

	r.Group(func(pr chi.Router) {
		if d.AuthRequire != nil {
			pr.Use(d.AuthRequire)
		}
		pr.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/health", http.StatusSeeOther)
		})
		pr.Get("/health", handleHealth(d, tpl))
		pr.Get("/about", handleAbout(d, tpl))
		pr.Get("/login", handleLogin(d, tpl))
		pr.Post("/login", loginRL.guard(handleLoginPost(d, d.Auth)))
		pr.Post("/logout", handleLogoutPost(d, d.Auth))
		// Mount the multi-step wizard when SQLite deps are wired; fall back to
		// the legacy single-step handler otherwise (test fixtures, in-process simulator).
		if d.Setup != nil && d.Setup.Users != nil {
			wd := *d.Setup
			if wd.Sessions == nil {
				wd.Sessions = NewSetupSessionStore()
			}
			pr.Get("/setup", handleSetupWizard(d, tpl, wd))
			pr.Post("/setup/admin", handleSetupAdmin(wd))
			pr.Post("/setup/locale", handleSetupLocale(wd))
			pr.Post("/setup/ccu", handleSetupCCU(wd))
			pr.Post("/setup/mqtt", handleSetupMQTT(d, wd))
		} else {
			pr.Get("/setup", handleSetup(d, tpl))
			pr.Post("/setup", handleSetupPost(d, d.Auth))
		}
		pr.Get("/login/oidc/start", handleOIDCStart(d))
		pr.Get("/login/oidc/callback", handleOIDCCallback(d))
	})
	return r
}

func handleAbout(d Deps, tpl *templateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		render(tpl, w, r, "about.html", pageData{Title: "About", Lang: d.Lang})
	}
}

func handleLogin(d Deps, tpl *templateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		oidcEnabled := d.OIDC != nil && d.OIDC.Client != nil
		render(tpl, w, r, "login.html", pageData{
			Title: "Sign in", Lang: d.Lang,
			Data: struct {
				Error       bool
				OIDCEnabled bool
				SetupDone   bool
			}{
				Error:       r.URL.Query().Get("error") != "",
				OIDCEnabled: oidcEnabled,
				SetupDone:   r.URL.Query().Get("setup_done") == "1",
			},
		})
	}
}

func handleSetup(d Deps, tpl *templateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		render(tpl, w, r, "setup.html", pageData{Title: "Setup", Lang: d.Lang})
	}
}

// pageData is the common top-level envelope every page renders.
type pageData struct {
	Title     string
	Version   string
	Lang      string
	CSRFToken string
	Identity  auth.Identity
	Data      any
}

// templateSet indexes one compiled template per page. Each page
// carries its own `body` definition plus the shared layout; parsing
// them separately avoids the `{{define "body"}}` collision that
// arises when every page lives in a single template set.
type templateSet struct {
	pages map[string]*template.Template
}

func mustParseTemplates(catalogs *i18n.Catalogs, lang string) *templateSet {
	funcs := template.FuncMap{
		"deref": func(b *bool) bool {
			if b == nil {
				return false
			}
			return *b
		},
		// t translates key and optionally substitutes {name} placeholders from
		// trailing (name, value) argument pairs, e.g.
		//   {{t "setup.step.progress" "current" .Data.Step "total" .Data.Total}}
		// turns "Step {current} of {total}" into "Step 1 of 4". Calls with no
		// pairs behave as before.
		"t": func(key string, args ...any) string {
			s := key
			if catalogs != nil {
				s = catalogs.T(lang, key)
			}
			for i := 0; i+1 < len(args); i += 2 {
				name, ok := args[i].(string)
				if !ok {
					continue
				}
				s = strings.ReplaceAll(s, "{"+name+"}", fmt.Sprint(args[i+1]))
			}
			return s
		},
	}
	layoutBuf, err := fs.ReadFile(templateFS, "templates/layout.html")
	if err != nil {
		panic(err)
	}
	set := &templateSet{pages: make(map[string]*template.Template)}
	entries, err := fs.ReadDir(templateFS, "templates")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		if e.Name() == "layout.html" {
			continue
		}
		body, err := fs.ReadFile(templateFS, "templates/"+e.Name())
		if err != nil {
			panic(err)
		}
		t := template.New(e.Name()).Funcs(funcs)
		if _, err := t.Parse(string(layoutBuf)); err != nil {
			panic(err)
		}
		if _, err := t.Parse(string(body)); err != nil {
			panic(err)
		}
		set.pages[e.Name()] = t
	}
	return set
}

func render(set *templateSet, w http.ResponseWriter, r *http.Request, bodyFile string, data pageData) {
	data.Version = build.Version
	data.CSRFToken = auth.CSRFToken(r.Context())
	if id, ok := auth.IdentityFrom(r.Context()); ok {
		data.Identity = id
	}
	tpl, ok := set.pages[bodyFile]
	if !ok {
		http.Error(w, "unknown template "+bodyFile, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type healthData struct {
	Status     string
	Components []handlers.HealthComponent
}

func handleHealth(d Deps, tpl *templateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := healthData{Status: "unknown"}
		if d.Health != nil {
			data.Status = string(d.Health.Overall())
			for _, c := range d.Health.Snapshot() {
				data.Components = append(data.Components, handlers.HealthComponent{
					Name: c.Name, Status: string(c.Status), Note: c.LastSample.Note, RecordedAt: c.LastSample.Timestamp,
				})
			}
		}
		render(tpl, w, r, "health.html", pageData{Title: "Health", Lang: d.Lang, Data: data})
	}
}
