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

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// Deps collects every dependency the diagnostic UI needs. The
// server-rendered surface is intentionally tiny — a no-JS /health and
// /about that stay reachable when the Svelte SPA bundle fails to load.
// Login, logout, OIDC, and first-run onboarding all live in the SPA now.
type Deps struct {
	Logger   *slog.Logger
	Lang     string
	Health   handlers.HealthReader
	Catalogs *i18n.Catalogs
}

// tr resolves an i18n catalogue key in the deps' locale, for page titles set
// outside the template's `t` func. Falls back to the key when no catalogues
// are wired.
func (d Deps) tr(key string) string {
	if d.Catalogs == nil {
		return key
	}
	return d.Catalogs.T(d.Lang, key)
}

// NewRouter builds the diagnostic UI router. The server-rendered surface
// only covers what stays useful when the SPA cannot load:
//
//	/              — redirect to /health
//	/health        — server-rendered status (SPA-down fallback)
//	/about         — version + license
//	/ui/assets/*   — embedded CSS + logo
//
// Everything interactive — login, onboarding, devices, settings, … — lives
// in the Svelte SPA at /app/ on the REST server.
func NewRouter(d Deps) *chi.Mux {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Lang == "" {
		d.Lang = "en"
	}
	tpl := mustParseTemplates(d.Catalogs, d.Lang)

	r := chi.NewRouter()

	assetSub, err := fs.Sub(assetFS, "assets")
	if err != nil {
		panic(err)
	}
	r.Handle("/ui/assets/*", http.StripPrefix("/ui/assets/", http.FileServer(http.FS(assetSub))))

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		uiRedirect(w, r, "/health")
	})
	r.Get("/health", handleHealth(d, tpl))
	r.Get("/about", handleAbout(d, tpl))
	return r
}

func handleAbout(d Deps, tpl *templateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		render(tpl, w, r, "about.html", pageData{Title: d.tr("nav.about"), Lang: d.Lang})
	}
}

// pageData is the common top-level envelope every page renders.
type pageData struct {
	Title   string
	Version string
	Lang    string
	// BasePath is the HA Ingress prefix (or "" for direct access). It backs
	// the layout's <base href> so every relative URL in the bootstrap pages
	// resolves through the Ingress proxy rather than the HA origin.
	BasePath string
	Data     any
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
		// t translates key and optionally substitutes {name} placeholders from
		// trailing (name, value) argument pairs. Calls with no pairs behave as
		// a plain lookup.
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
	data.BasePath = ingressPrefix(r)
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
		render(tpl, w, r, "health.html", pageData{Title: d.tr("nav.health"), Lang: d.Lang, Data: data})
	}
}
