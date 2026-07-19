// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package remoteproxy

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"
)

// Server routes browser requests to the configured remote instances.
// With exactly one instance it is a fully transparent proxy at "/";
// with several, each instance mounts under "/i/<name>/" and "/" serves
// the overview page.
type Server struct {
	instances []*instanceProxy
	log       *slog.Logger
}

// New builds the proxy server from validated options.
func New(opts Options, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{log: log}
	single := len(opts.Instances) == 1
	for _, inst := range opts.Instances {
		prefix := ""
		if !single {
			prefix = "/i/" + inst.Name
		}
		p, err := newInstanceProxy(inst, prefix, log)
		if err != nil {
			return nil, err
		}
		s.instances = append(s.instances, p)
	}
	return s, nil
}

// Handler returns the root http.Handler.
func (s *Server) Handler() http.Handler {
	if len(s.instances) == 1 {
		return s.instances[0].rp
	}
	mux := http.NewServeMux()
	for _, p := range s.instances {
		prefix := p.prefix
		mux.Handle(prefix+"/", http.StripPrefix(prefix, p.rp))
		// Bare /i/<name> → /i/<name>/ so the SPA's relative asset URLs
		// resolve under the instance mount, not next to it. ingressBase
		// sanitizes the header, so the target stays an absolute path on
		// the HA origin.
		mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
			//nolint:gosec // G710: ingressBase rejects anything but a plain absolute path, so the target stays on-origin.
			http.Redirect(w, r, ingressBase(r)+prefix+"/", http.StatusPermanentRedirect)
		})
	}
	mux.HandleFunc("GET /{$}", s.serveOverview)
	// Anything else is a stray path on the proxy itself (not under an
	// instance mount): send the browser back to the overview.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		//nolint:gosec // G710: ingressBase rejects anything but a plain absolute path, so the target stays on-origin.
		http.Redirect(w, r, ingressBase(r)+"/", http.StatusFound)
	})
	return mux
}

// serveOverview renders the instance selector. Links are relative so
// they resolve under whatever base the Ingress session uses.
func (s *Server) serveOverview(w http.ResponseWriter, r *http.Request) {
	loc := localeFor(r)
	var b strings.Builder
	b.WriteString("<ul class=\"instances\">")
	for _, p := range s.instances {
		name := html.EscapeString(p.inst.Name)
		fmt.Fprintf(&b, "<li><a href=\"i/%s/\">%s</a></li>", name, name)
	}
	b.WriteString("</ul>")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, pageShell, loc.lang, html.EscapeString(loc.title), b.String())
}

// serveUnreachable is the browser-facing 502 for a dead upstream.
func serveUnreachable(w http.ResponseWriter, r *http.Request, instance string) {
	loc := localeFor(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusBadGateway)
	body := fmt.Sprintf("<p class=\"error\">%s</p>",
		html.EscapeString(fmt.Sprintf(loc.unreachable, instance)))
	_, _ = fmt.Fprintf(w, pageShell, loc.lang, html.EscapeString(loc.title), body)
}

// locale carries the handful of proxy-rendered strings. The proxied SPA
// brings its own full i18n; only the selector/error shell lives here.
type locale struct {
	lang        string
	title       string
	unreachable string
}

var (
	localeEN = locale{
		lang:        "en",
		title:       "OpenCCU-Loom Remote",
		unreachable: "The instance %q is currently unreachable.",
	}
	localeDE = locale{
		lang:        "de",
		title:       "OpenCCU-Loom Remote",
		unreachable: "Die Instanz %q ist derzeit nicht erreichbar.",
	}
)

// localeFor picks German when it precedes English in Accept-Language;
// everything else falls back to English.
func localeFor(r *http.Request) locale {
	accept := strings.ToLower(r.Header.Get("Accept-Language"))
	for _, part := range strings.Split(accept, ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		switch {
		case tag == "de" || strings.HasPrefix(tag, "de-"):
			return localeDE
		case tag == "en" || strings.HasPrefix(tag, "en-"):
			return localeEN
		}
	}
	return localeEN
}

// pageShell is the minimal shell for proxy-rendered pages. Styling is
// intentionally tiny and theme-aware via color-scheme + light-dark().
const pageShell = `<!doctype html>
<html lang="%s">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
:root { color-scheme: light dark; }
body {
  font-family: system-ui, sans-serif;
  margin: 2rem auto; max-width: 40rem; padding: 0 1rem;
  background: light-dark(#fafafa, #111418);
  color: light-dark(#1c1c1e, #e4e6eb);
}
a { color: light-dark(#0b57d0, #8ab4f8); text-decoration: none; }
a:hover { text-decoration: underline; }
ul.instances { list-style: none; padding: 0; }
ul.instances li { margin: .5rem 0; font-size: 1.1rem; }
p.error { color: light-dark(#b3261e, #f2b8b5); }
</style>
</head>
<body>
%s
</body>
</html>
`
