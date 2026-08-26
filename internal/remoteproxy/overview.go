// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package remoteproxy

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

// serveStatus is the JSON feed behind the overview page's auto-refresh.
func (s *Server) serveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		Instances []InstanceStatus `json:"instances"`
	}{Instances: s.poller.snapshot()})
}

// serveOverview renders the instance selector with live status tiles.
// Links are relative so they resolve under whatever base the Ingress
// session uses.
func (s *Server) serveOverview(w http.ResponseWriter, r *http.Request) {
	loc := localeFor(r)
	// One JSON object carries every label the refresh script needs —
	// JSON is valid JS here, and a single injection point avoids the
	// double-encoding that piping strings through the template's JS
	// escaper would add.
	labels, err := json.Marshal(struct {
		Statuses map[string]string `json:"statuses"`
		Version  string            `json:"version"`
		Uptime   string            `json:"uptime"`
	}{loc.statusLabels, loc.VersionLabel, loc.UptimeLabel})
	if err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	data := overviewData{
		Locale: loc,
		Tiles:  s.poller.snapshot(),
		// Locale-constant JSON, not user input: safe to hand to the
		// inline refresh script verbatim.
		LabelsJSON: template.JS(labels), //nolint:gosec // G203: marshaled from compile-time locale constants only.
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := overviewTmpl.Execute(w, data); err != nil {
		s.log.Debug("overview render aborted", "error", err)
	}
}

// serveUnreachable is the browser-facing 502 for a dead upstream.
func serveUnreachable(w http.ResponseWriter, r *http.Request, instance string) {
	loc := localeFor(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusBadGateway)
	_ = errorTmpl.Execute(w, errorData{
		Locale:  loc,
		Message: fmt.Sprintf(loc.unreachable, instance),
	})
}

// locale carries the handful of proxy-rendered strings. The proxied SPA
// brings its own full i18n; only the selector/error shell lives here.
type locale struct {
	Lang         string
	Title        string
	Subtitle     string
	VersionLabel string
	UptimeLabel  string
	CheckedLabel string
	unreachable  string
	statusLabels map[string]string
}

var (
	localeEN = locale{
		Lang:         "en",
		Title:        "OpenCCU-Loom Remote",
		Subtitle:     "Select an instance",
		VersionLabel: "Version",
		UptimeLabel:  "Uptime",
		CheckedLabel: "Last checked",
		unreachable:  "The instance %q is currently unreachable.",
		statusLabels: map[string]string{
			"healthy":     "Healthy",
			"degraded":    "Degraded",
			"unhealthy":   "Unhealthy",
			"unreachable": "Unreachable",
			"unknown":     "Checking…",
		},
	}
	localeDE = locale{
		Lang:         "de",
		Title:        "OpenCCU-Loom Remote",
		Subtitle:     "Instanz auswählen",
		VersionLabel: "Version",
		UptimeLabel:  "Laufzeit",
		CheckedLabel: "Zuletzt geprüft",
		unreachable:  "Die Instanz %q ist derzeit nicht erreichbar.",
		statusLabels: map[string]string{
			"healthy":     "In Ordnung",
			"degraded":    "Beeinträchtigt",
			"unhealthy":   "Gestört",
			"unreachable": "Nicht erreichbar",
			"unknown":     "Prüfe…",
		},
	}
)

// StatusLabel resolves a status key for the initial server-side render;
// the refresh script uses the same mapping client-side.
func (l locale) StatusLabel(key string) string {
	if v, ok := l.statusLabels[key]; ok {
		return v
	}
	return key
}

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

type overviewData struct {
	Locale     locale
	Tiles      []InstanceStatus
	LabelsJSON template.JS
}

type errorData struct {
	Locale  locale
	Message string
}

// baseCSS is shared by every proxy-rendered page: tiny, dependency-free
// and theme-aware via color-scheme + light-dark().
const baseCSS = `
:root { color-scheme: light dark; }
* { box-sizing: border-box; }
body {
  font-family: system-ui, sans-serif;
  margin: 2rem auto; max-width: 44rem; padding: 0 1rem;
  background: light-dark(#fafafa, #111418);
  color: light-dark(#1c1c1e, #e4e6eb);
}
h1 { font-size: 1.3rem; margin: 0 0 .25rem; }
p.sub { margin: 0 0 1.5rem; color: light-dark(#5f6368, #9aa0a6); }
a { color: inherit; text-decoration: none; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(15rem, 1fr)); gap: .75rem; }
a.card {
  display: block; padding: 1rem; border-radius: .75rem;
  background: light-dark(#ffffff, #1b1f24);
  border: 1px solid light-dark(#e0e0e0, #2c313a);
  transition: border-color .15s ease;
}
a.card:hover { border-color: light-dark(#0b57d0, #8ab4f8); }
.name { font-weight: 600; margin-bottom: .35rem; overflow-wrap: anywhere; }
.row { display: flex; align-items: center; gap: .45rem; font-size: .9rem; }
.dot { width: .6rem; height: .6rem; border-radius: 50%; flex: none; background: light-dark(#9aa0a6, #5f6368); }
.dot.healthy { background: light-dark(#1e8e3e, #6dd58c); }
.dot.degraded { background: light-dark(#f9ab00, #fdd663); }
.dot.unhealthy, .dot.unreachable { background: light-dark(#d93025, #f28b82); }
.meta { margin-top: .4rem; font-size: .8rem; color: light-dark(#5f6368, #9aa0a6); }
p.error { color: light-dark(#b3261e, #f2b8b5); }
`

var overviewTmpl = template.Must(template.New("overview").Parse(`<!doctype html>
<html lang="{{.Locale.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Locale.Title}}</title>
<style>` + baseCSS + `</style>
</head>
<body>
<h1>{{.Locale.Title}}</h1>
<p class="sub">{{.Locale.Subtitle}}</p>
<div class="grid">
{{- $loc := .Locale }}
{{- range .Tiles }}
<a class="card" id="inst-{{.Name}}" href="i/{{.Name}}/">
  <div class="name">{{.Name}}</div>
  <div class="row"><span class="dot {{.Status}}"></span><span class="status">{{$loc.StatusLabel .Status}}</span></div>
  <div class="meta">{{if .Version}}{{$loc.VersionLabel}} {{.Version}}{{if .Uptime}} · {{$loc.UptimeLabel}} {{.Uptime}}{{end}}{{end}}&nbsp;</div>
</a>
{{- end }}
</div>
<script>
const labels = {{.LabelsJSON}};
async function refreshTiles() {
  try {
    const r = await fetch('./-/status', { cache: 'no-store' });
    if (!r.ok) return;
    const d = await r.json();
    for (const s of d.instances) {
      const el = document.getElementById('inst-' + s.name);
      if (!el) continue;
      el.querySelector('.dot').className = 'dot ' + s.status;
      el.querySelector('.status').textContent = labels.statuses[s.status] || s.status;
      let meta = '';
      if (s.version) {
        meta = labels.version + ' ' + s.version;
        if (s.uptime) meta += ' · ' + labels.uptime + ' ' + s.uptime;
      }
      el.querySelector('.meta').innerHTML = '';
      el.querySelector('.meta').textContent = meta || ' ';
    }
  } catch { /* transient — next tick retries */ }
}
setInterval(refreshTiles, 10000);
</script>
</body>
</html>
`))

var errorTmpl = template.Must(template.New("error").Parse(`<!doctype html>
<html lang="{{.Locale.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Locale.Title}}</title>
<style>` + baseCSS + `</style>
</head>
<body>
<h1>{{.Locale.Title}}</h1>
<p class="error">{{.Message}}</p>
</body>
</html>
`))
