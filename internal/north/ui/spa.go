// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// spaFS holds the compiled Svelte single-page application. The source
// lives at `assets/ui/` and `vite build` writes its output here so the
// Go compiler can embed the static bundle.
//
// The `all:` prefix makes embed recurse into `assets/` subdirectories
// that Vite creates (for JS/CSS chunks, images, etc.). Files starting
// with `_` would otherwise be excluded.
//
//go:embed all:spa_dist
var spaFS embed.FS

// SPAHandler serves the compiled Svelte SPA under /app/. Client-side
// routes that don't correspond to a real file are rewritten to
// index.html so hash-less routing (deep links, bookmarks) still works.
//
// Use http.StripPrefix("/app", SPAHandler()) to mount.
func SPAHandler() http.Handler {
	sub, err := fs.Sub(spaFS, "spa_dist")
	if err != nil {
		// A bad embed directive is a build-time mistake; the panic
		// surfaces it on the first request rather than silently
		// serving 404s.
		panic(err)
	}
	server := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Any request that doesn't resolve to a file falls back to
		// the SPA's index.html — that is what makes client-side
		// routing work for deep links.
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			serveIndex(sub, w, r)
			return
		}
		if path == "index.html" {
			// Same no-store answer as every client-side route, so a fresh
			// deploy is picked up immediately however the entry point is
			// addressed.
			serveIndex(sub, w, r)
			return
		}
		f, err := sub.Open(path)
		if err != nil {
			serveIndex(sub, w, r)
			return
		}
		_ = f.Close()
		w.Header().Set("Cache-Control", cacheControlFor(path))
		server.ServeHTTP(w, r)
	})
}

// hashedAssetDir is the bundle directory the build writes content-hashed
// files into. Everything outside it is copied verbatim from the SPA's
// public/ directory and keeps its name across releases.
const hashedAssetDir = "assets/"

// cacheControlFor picks the caching policy for one bundle file.
//
// Only a content-hashed name may be served immutable: its URL changes
// whenever its content does, so a year-long cache can never go stale. The
// bundle also carries verbatim public files — the web manifest, the
// favicons, the wordmark — whose names are identical in every release. Marked
// immutable, a returning browser would keep the previous logo or manifest for
// up to a year and, because `immutable` suppresses revalidation, would not
// even ask; the operator has no client-side remedy short of clearing the
// cache. Those files get a short cache that revalidates instead.
func cacheControlFor(path string) string {
	if strings.HasPrefix(path, hashedAssetDir) {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=3600, must-revalidate"
}

func serveIndex(sub fs.FS, w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(spaNotBuiltHTML))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
	_ = r // linter-appeasing reference; r is not otherwise consumed
}

// spaNotBuiltHTML is shown when the embedded spa_dist/ has no index.html —
// the Svelte bundle has not been built. Only /health and /about remain on
// the REST listener in this state: login, setup and onboarding live in the
// SPA (ADR 0045), so they are unavailable precisely when this page shows.
const spaNotBuiltHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>openccu-loom — SPA not built</title>
    <style>
      body { font: 14px/1.5 system-ui, sans-serif; max-width: 42rem; margin: 4rem auto; padding: 0 1rem; color: #1e293b; }
      h1 { font-size: 1.4rem; margin-bottom: 0.5rem; }
      code { background: #f1f5f9; padding: 0.1rem 0.4rem; border-radius: 0.25rem; }
      pre { background: #f1f5f9; padding: 0.75rem 1rem; border-radius: 0.5rem; overflow-x: auto; }
      a { color: #2563eb; }
    </style>
  </head>
  <body>
    <h1>SPA bundle not built</h1>
    <p>The Svelte single-page application has not been compiled into <code>internal/north/ui/spa_dist/</code>.
    Build it with:</p>
    <pre>make ui-install   # once
make dist         # SPA + daemon</pre>
    <p>Or run the Vite dev server with hot-reload (proxies <code>/api</code> to this daemon):</p>
    <pre>make ui-dev       # then open http://localhost:5173/app/</pre>
    <p>Only the diagnostic pages remain on this listener while the bundle
    is missing: <a href="/health">/health</a> and <a href="/about">/about</a>.
    Login, setup and onboarding are part of the SPA and are unavailable
    until it is built.</p>
  </body>
</html>
`
