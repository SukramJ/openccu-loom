// SPDX-License-Identifier: Apache-2.0
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
	pairLabels, err := loc.pairLabelsJSON()
	if err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	data := overviewData{
		Locale: loc,
		Tiles:  s.poller.snapshot(),
		// Locale-constant JSON, not user input: safe to hand to the
		// inline refresh script verbatim.
		LabelsJSON:     template.JS(labels), //nolint:gosec // G203: marshaled from compile-time locale constants only.
		PairLabelsJSON: pairLabels,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := overviewTmpl.Execute(w, data); err != nil {
		s.log.Debug("overview render aborted", "error", err)
	}
}

// servePairPage is the pairing page of one instance at /-/pair/<name>:
// the way to pair in transparent single-instance mode, where no overview
// exists, and the target of the login explanation in both modes.
func (s *Server) servePairPage(w http.ResponseWriter, r *http.Request, p *instanceProxy) {
	loc := localeFor(r)
	pairLabels, err := loc.pairLabelsJSON()
	if err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := pairPageTmpl.Execute(w, pairPageData{
		Locale: loc, Name: p.inst.Name, View: p.pairView(), PairLabelsJSON: pairLabels,
	}); err != nil {
		s.log.Debug("pairing page render aborted", "error", err)
	}
}

// authWallData fills the login explanation for this instance.
func (p *instanceProxy) authWallData(loc locale, pairURL string) authWallPage {
	d := authWallPage{Locale: loc, Intro: fmt.Sprintf(loc.WallIntro, p.inst.Name), PairURL: pairURL}
	switch {
	case p.inst.Token != "":
		d.Message = loc.WallOptions
	case p.lastPairError() == "not-local":
		d.Message = loc.WallNotLocal
	case p.authSource() == authSourcePaired:
		d.Message = loc.WallPaired
		d.CanPair = true
	default:
		d.Message = loc.WallPair
		d.CanPair = true
	}
	return d
}

// pairLabelsJSON carries the pairing strings the browser script needs.
func (l locale) pairLabelsJSON() (template.JS, error) {
	raw, err := json.Marshal(struct {
		Pair   string            `json:"pair"`
		Repair string            `json:"repair"`
		States map[string]string `json:"states"`
		Errors map[string]string `json:"errors"`
	}{l.PairButton, l.RepairButton, l.pairStates, l.pairErrors})
	if err != nil {
		return "", err
	}
	return template.JS(raw), nil //nolint:gosec // G203: marshaled from compile-time locale constants only.
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

	// Box pairing (docs/adr/0091-remote-addon-box-pairing.md).
	PairButton   string
	RepairButton string
	UnpairButton string
	PairCodeHint string
	PairOpenBox  string
	PairTitle    string
	PairIntro    string
	BackLabel    string
	pairStates   map[string]string
	pairErrors   map[string]string

	// The login explanation in front of a box-protected instance.
	WallIntro    string
	WallPair     string
	WallPaired   string
	WallNotLocal string
	WallOptions  string
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
			"healthy":      "Healthy",
			"degraded":     "Degraded",
			"unhealthy":    "Unhealthy",
			"unreachable":  "Unreachable",
			"unauthorized": "Login required",
			"unknown":      "Checking…",
		},
		PairButton:   "Pair with box",
		RepairButton: "Pair again",
		UnpairButton: "Forget pairing",
		PairCodeHint: "Approve this code in the web interface of the openccu-lite box:",
		PairOpenBox:  "Open the box",
		PairTitle:    "Pair with the openccu-lite box",
		PairIntro: "Pairing asks the box for a token that opens the OpenCCU-Loom add-on with operator rights. " +
			"For admin rights, create a Full-access API token on the box and enter it in the add-on options instead; " +
			"a token in the options always wins.",
		BackLabel: "Back",
		pairStates: map[string]string{
			"idle":     "Not paired.",
			"pending":  "Waiting for the approval on the box…",
			"paired":   "Paired with the box (operator).",
			"rejected": "The box rejected the pairing.",
			"expired":  "The pairing expired before it was approved.",
			"options":  "The token in the add-on options is used.",
		},
		pairErrors: map[string]string{
			"not-local": "The box refuses pairing from this network: Home Assistant is not in one of the box's local networks. " +
				"Create a Full-access API token on the box and enter it in the add-on options.",
			"pairing-off":    "Pairing is switched off on the box.",
			"limit":          "The box takes no further pairing requests right now; try again in a minute.",
			"fingerprint":    "Pairing aborted: the certificate the box reports differs from the one seen (possible interception).",
			"unsupported":    "The target offers no openccu-lite pairing. Pairing works only for an instance reached through the box.",
			"unreachable":    "The box cannot be reached.",
			"no-addon-scope": "The box approved the pairing without the OpenCCU-Loom add-on.",
			"state-write":    "The paired token could not be stored.",
			"failed":         "Pairing failed.",
		},
		WallIntro:  "The instance %q is behind the login of the openccu-lite box.",
		WallPair:   "Pair the add-on with the box for operator access, or enter a Full-access API token of the box in the add-on options for admin access.",
		WallPaired: "The paired token does not open this page. Pair again, or enter a Full-access API token of the box in the add-on options.",
		WallNotLocal: "The box refuses pairing from this network: Home Assistant is not in one of the box's local networks. " +
			"Create a Full-access API token in the box's web interface and enter it in the add-on options.",
		WallOptions: "The box does not accept the token in the add-on options for this instance. Check the token there.",
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
			"healthy":      "In Ordnung",
			"degraded":     "Beeinträchtigt",
			"unhealthy":    "Gestört",
			"unreachable":  "Nicht erreichbar",
			"unauthorized": "Anmeldung nötig",
			"unknown":      "Prüfe…",
		},
		PairButton:   "Mit Box koppeln",
		RepairButton: "Erneut koppeln",
		UnpairButton: "Kopplung aufheben",
		PairCodeHint: "Diesen Code in der Weboberfläche der openccu-lite-Box bestätigen:",
		PairOpenBox:  "Box öffnen",
		PairTitle:    "Mit der openccu-lite-Box koppeln",
		PairIntro: "Die Kopplung erbittet von der Box ein Token, das das OpenCCU-Loom-Add-on mit Bedienerrechten öffnet. " +
			"Für Administratorrechte stattdessen auf der Box ein API-Token mit Vollzugriff erstellen und in den Add-on-Optionen eintragen; " +
			"ein Token in den Optionen hat immer Vorrang.",
		BackLabel: "Zurück",
		pairStates: map[string]string{
			"idle":     "Nicht gekoppelt.",
			"pending":  "Warte auf die Bestätigung auf der Box…",
			"paired":   "Mit der Box gekoppelt (Bediener).",
			"rejected": "Die Box hat die Kopplung abgelehnt.",
			"expired":  "Die Kopplung ist abgelaufen, bevor sie bestätigt wurde.",
			"options":  "Das Token aus den Add-on-Optionen wird verwendet.",
		},
		pairErrors: map[string]string{
			"not-local": "Die Box lehnt Kopplungen aus diesem Netz ab: Home Assistant liegt in keinem lokalen Netz der Box. " +
				"Auf der Box ein API-Token mit Vollzugriff erstellen und in den Add-on-Optionen eintragen.",
			"pairing-off":    "Die Kopplung ist auf der Box ausgeschaltet.",
			"limit":          "Die Box nimmt gerade keine weiteren Kopplungsanfragen an; in einer Minute erneut versuchen.",
			"fingerprint":    "Kopplung abgebrochen: Das Zertifikat, das die Box meldet, ist nicht das gesehene (mögliches Abfangen).",
			"unsupported":    "Das Ziel bietet keine openccu-lite-Kopplung. Gekoppelt werden kann nur eine Instanz, die über die Box erreicht wird.",
			"unreachable":    "Die Box ist nicht erreichbar.",
			"no-addon-scope": "Die Box hat die Kopplung ohne das OpenCCU-Loom-Add-on bestätigt.",
			"state-write":    "Das gekoppelte Token konnte nicht gespeichert werden.",
			"failed":         "Die Kopplung ist fehlgeschlagen.",
		},
		WallIntro:  "Die Instanz %q liegt hinter der Anmeldung der openccu-lite-Box.",
		WallPair:   "Das Add-on für Bedienerzugriff mit der Box koppeln, oder für Administratorzugriff ein API-Token der Box mit Vollzugriff in den Add-on-Optionen eintragen.",
		WallPaired: "Das gekoppelte Token öffnet diese Seite nicht. Erneut koppeln, oder ein API-Token der Box mit Vollzugriff in den Add-on-Optionen eintragen.",
		WallNotLocal: "Die Box lehnt Kopplungen aus diesem Netz ab: Home Assistant liegt in keinem lokalen Netz der Box. " +
			"In der Weboberfläche der Box ein API-Token mit Vollzugriff erstellen und in den Add-on-Optionen eintragen.",
		WallOptions: "Die Box akzeptiert das Token aus den Add-on-Optionen für diese Instanz nicht. Das Token dort prüfen.",
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
	for part := range strings.SplitSeq(accept, ",") {
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
	Locale         locale
	Tiles          []InstanceStatus
	LabelsJSON     template.JS
	PairLabelsJSON template.JS
}

type pairPageData struct {
	Locale         locale
	Name           string
	View           pairView
	PairLabelsJSON template.JS
}

type authWallPage struct {
	Locale  locale
	Intro   string
	Message string
	PairURL string
	CanPair bool
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
.dot.unauthorized { background: light-dark(#f9ab00, #fdd663); }
.tile { display: flex; flex-direction: column; gap: .4rem; }
.pair { font-size: .85rem; display: flex; flex-wrap: wrap; align-items: center; gap: .4rem; }
.pair[hidden], .pair [hidden] { display: none; }
button {
  font: inherit; cursor: pointer; padding: .3rem .7rem; border-radius: .5rem;
  border: 1px solid light-dark(#c4c7c5, #444746);
  background: light-dark(#ffffff, #1b1f24); color: inherit;
}
button:hover { border-color: light-dark(#0b57d0, #8ab4f8); }
.pair-code { flex-basis: 100%; }
.digits { font-size: 1.6rem; font-weight: 600; letter-spacing: .2em; font-variant-numeric: tabular-nums; }
.pair-code a, a.button { text-decoration: underline; }
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
<div class="tile" id="inst-{{.Name}}">
<a class="card" href="i/{{.Name}}/">
  <div class="name">{{.Name}}</div>
  <div class="row"><span class="dot {{.Status}}"></span><span class="status">{{$loc.StatusLabel .Status}}</span></div>
  <div class="meta">{{if .Version}}{{$loc.VersionLabel}} {{.Version}}{{if .Uptime}} · {{$loc.UptimeLabel}} {{.Uptime}}{{end}}{{end}}&nbsp;</div>
</a>
<div class="pair" data-endpoint="-/pair/{{.Name}}"{{if eq .Auth "options"}} hidden{{end}}>
` + pairControls + `
</div>
</div>
{{- end }}
</div>
<script>
` + pairScript + `
document.querySelectorAll('.pair').forEach(pairBind);
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

// pairControls is one instance's pairing box: start, forget, the state
// line and the code. Its initial visibility follows the tile's credential
// source; pairScript takes over from the first click.
const pairControls = `<button type="button" class="pair-start">{{if eq .Auth "paired"}}{{$loc.RepairButton}}{{else}}{{$loc.PairButton}}{{end}}</button>
<button type="button" class="pair-forget"{{if ne .Auth "paired"}} hidden{{end}}>{{$loc.UnpairButton}}</button>
<span class="pair-msg"></span>
<div class="pair-code" hidden>{{$loc.PairCodeHint}} <span class="digits"></span> <a class="pair-box" target="_blank" rel="noopener noreferrer">{{$loc.PairOpenBox}}</a></div>`

// pairScript drives a pairing box: POST starts, the state is polled while
// the box waits for its administrator, DELETE withdraws and forgets. The
// calls carry a JSON content type and the browser's Origin, which the
// proxy checks; the answers never carry the token.
const pairScript = `const pairLabels = {{.PairLabelsJSON}};
async function pairCall(endpoint, method) {
  const opts = { method, cache: 'no-store', headers: { 'Accept': 'application/json' } };
  if (method !== 'GET') { opts.headers['Content-Type'] = 'application/json'; opts.body = '{}'; }
  try {
    const r = await fetch(endpoint, opts);
    if (r.ok || r.status === 409) return await r.json();
    return { state: 'failed', error: 'failed', message: (await r.text()).trim() };
  } catch (e) {
    return { state: 'failed', error: 'failed', message: String(e) };
  }
}
function pairRender(box, v) {
  const code = box.querySelector('.pair-code');
  box.hidden = v.state === 'options';
  box.querySelector('.pair-start').hidden = v.state === 'pending';
  box.querySelector('.pair-start').textContent = v.paired ? pairLabels.repair : pairLabels.pair;
  box.querySelector('.pair-forget').hidden = !(v.paired || v.state === 'pending');
  code.hidden = v.state !== 'pending';
  if (v.state === 'pending') {
    code.querySelector('.digits').textContent = v.code || '';
    code.querySelector('.pair-box').href = v.box_url || '#';
  }
  let text = pairLabels.states[v.state] || '';
  if (v.state === 'failed') {
    text = pairLabels.errors[v.error] || (pairLabels.errors.failed + (v.message ? ' (' + v.message + ')' : ''));
  }
  box.querySelector('.pair-msg').textContent = text;
}
async function pairWatch(box, endpoint) {
  for (;;) {
    await new Promise((res) => setTimeout(res, 2000));
    const v = await pairCall(endpoint, 'GET');
    pairRender(box, v);
    if (v.state !== 'pending') return;
  }
}
function pairBind(box) {
  const endpoint = box.dataset.endpoint;
  box.querySelector('.pair-start').addEventListener('click', async () => {
    const v = await pairCall(endpoint, 'POST');
    pairRender(box, v);
    if (v.state === 'pending') pairWatch(box, endpoint);
  });
  box.querySelector('.pair-forget').addEventListener('click', async () => {
    pairRender(box, await pairCall(endpoint, 'DELETE'));
  });
}`

var pairPageTmpl = template.Must(template.New("pair").Parse(`<!doctype html>
<html lang="{{.Locale.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Locale.Title}}</title>
<style>` + baseCSS + `</style>
</head>
<body>
<h1>{{.Locale.PairTitle}}</h1>
<p class="sub">{{.Name}}</p>
<p>{{.Locale.PairIntro}}</p>
{{- $loc := .Locale }}
<div class="pair" data-endpoint="{{.Name}}"{{if eq .View.State "options"}} hidden{{end}}>
<button type="button" class="pair-start">{{if .View.Paired}}{{$loc.RepairButton}}{{else}}{{$loc.PairButton}}{{end}}</button>
<button type="button" class="pair-forget"{{if not .View.Paired}} hidden{{end}}>{{$loc.UnpairButton}}</button>
<span class="pair-msg"></span>
<div class="pair-code" hidden>{{$loc.PairCodeHint}} <span class="digits"></span> <a class="pair-box" target="_blank" rel="noopener noreferrer">{{$loc.PairOpenBox}}</a></div>
</div>
<p><a class="button" href="../../">{{.Locale.BackLabel}}</a></p>
<script>
` + pairScript + `
const box = document.querySelector('.pair');
pairBind(box);
pairCall(box.dataset.endpoint, 'GET').then((v) => {
  pairRender(box, v);
  if (v.state === 'pending') pairWatch(box, box.dataset.endpoint);
});
</script>
</body>
</html>
`))

var authWallTmpl = template.Must(template.New("authwall").Parse(`<!doctype html>
<html lang="{{.Locale.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Locale.Title}}</title>
<style>` + baseCSS + `</style>
</head>
<body>
<h1>{{.Locale.Title}}</h1>
<p>{{.Intro}}</p>
<p class="error">{{.Message}}</p>
{{- if .CanPair }}
<p><a class="button" href="{{.PairURL}}">{{.Locale.PairButton}}</a></p>
{{- end }}
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
