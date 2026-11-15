// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package remoteproxy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// Server routes browser requests to the configured remote instances.
// With exactly one instance it is a fully transparent proxy at "/";
// with several, each instance mounts under "/i/<name>/" and "/" serves
// the overview page.
// loom:reachable:reason="returned by New and driven by cmd/openccu-loom-remote via Handler and Start; the type heuristic does not follow constructor returns of the proxy binary"
type Server struct {
	instances []*instanceProxy
	byName    map[string]*instanceProxy
	poller    *poller
	log       *slog.Logger
	// done ends the background pairing waits; Start sets it to the
	// daemon's lifetime before the handler serves (nil: never).
	done <-chan struct{}
}

// New builds the proxy server from validated options. Paired box tokens
// are loaded from opts.StateFile; entries whose instance or URL changed
// are dropped there.
func New(opts Options, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	store, err := openPairStore(opts.StateFile, opts.Instances, log)
	if err != nil {
		return nil, err
	}
	s := &Server{log: log, byName: map[string]*instanceProxy{}}
	single := len(opts.Instances) == 1
	for _, inst := range opts.Instances {
		prefix := ""
		if !single {
			prefix = "/i/" + inst.Name
		}
		p, err := newInstanceProxy(inst, prefix, store, log)
		if err != nil {
			return nil, err
		}
		s.instances = append(s.instances, p)
		s.byName[inst.Name] = p
	}
	s.poller = newPoller(s.instances, log)
	return s, nil
}

// Start launches the background status pollers: they feed the overview
// tiles and drop a paired token the box no longer accepts, which matters
// in transparent single-instance mode too. The workers and any pairing
// wait stop when ctx is canceled. Call it before serving.
func (s *Server) Start(ctx context.Context) {
	s.done = ctx.Done()
	s.poller.start(ctx)
}

// Handler returns the root http.Handler.
func (s *Server) Handler() http.Handler {
	if len(s.instances) == 1 {
		rp := s.instances[0].rp
		// No ServeMux here: it would clean and redirect paths the
		// transparent proxy must hand upstream verbatim. Only the reserved
		// pairing subtree is answered by the proxy itself.
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rest, ok := strings.CutPrefix(r.URL.Path, pairRoutePrefix); ok {
				s.servePair(w, r, rest)
				return
			}
			rp.ServeHTTP(w, r)
		})
	}
	mux := http.NewServeMux()
	mux.HandleFunc(pairRoutePrefix+"{name}", func(w http.ResponseWriter, r *http.Request) {
		s.servePair(w, r, r.PathValue("name"))
	})
	for _, p := range s.instances {
		prefix := p.prefix
		mux.Handle(prefix+"/", http.StripPrefix(prefix, p.rp))
		// Bare /i/<name> → /i/<name>/ so the SPA's relative asset URLs
		// resolve under the instance mount, not next to it. The query
		// travels along (deep links encode state there). ingressBase
		// sanitizes the header, so the target stays an absolute path on
		// the HA origin.
		mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
			target := ingressBase(r) + prefix + "/"
			if q := r.URL.RawQuery; q != "" {
				target += "?" + q
			}
			//nolint:gosec // G710: ingressBase rejects anything but a plain absolute path, so the target stays on-origin.
			http.Redirect(w, r, target, http.StatusPermanentRedirect)
		})
	}
	// All methods land here so a non-GET on "/" gets a 405 instead of
	// falling into the catch-all's redirect-to-self loop.
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		s.serveOverview(w, r)
	})
	mux.HandleFunc("GET /-/status", s.serveStatus)
	// Anything else is a stray path on the proxy itself (not under an
	// instance mount): send the browser back to the overview.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		//nolint:gosec // G710: ingressBase rejects anything but a plain absolute path, so the target stays on-origin.
		http.Redirect(w, r, ingressBase(r)+"/", http.StatusFound)
	})
	return mux
}

// pairRoutePrefix is the proxy's own pairing subtree, reserved in both
// modes and answered before anything is proxied.
const pairRoutePrefix = "/-/pair/"

// maxPairBody bounds the (ignored) body of a pairing mutation.
const maxPairBody = 4 << 10

// servePair answers /-/pair/<name>: GET the state (JSON) or the pairing
// page (HTML), POST starts a pairing, DELETE withdraws it and forgets the
// paired token. The token itself never leaves the proxy.
func (s *Server) servePair(w http.ResponseWriter, r *http.Request, name string) {
	p, ok := s.byName[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if wantsJSON(r) {
			writePairJSON(w, http.StatusOK, p.pairView())
			return
		}
		s.servePairPage(w, r, p)
	case http.MethodPost, http.MethodDelete:
		if status, reason := checkPairMutation(r); status != 0 {
			http.Error(w, reason, status)
			return
		}
		_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, maxPairBody))
		if r.Method == http.MethodDelete {
			writePairJSON(w, http.StatusOK, p.cancelPairing(r.Context()))
			return
		}
		if p.inst.Token != "" {
			// The options token wins anyway; a pairing would be dead weight.
			writePairJSON(w, http.StatusConflict, p.pairView())
			return
		}
		writePairJSON(w, http.StatusOK, p.startPairing(r.Context(), s.done))
	default:
		w.Header().Set("Allow", "GET, HEAD, POST, DELETE")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

// checkPairMutation guards the state-changing pairing calls against
// cross-site requests: the body must be declared JSON (which a plain HTML
// form cannot send without a CORS preflight), and the browser's Origin
// must be the host the browser addressed — X-Forwarded-Host as the HA
// ingress sets it, else Host. It answers 0 when the request may pass.
func checkPairMutation(r *http.Request) (status int, reason string) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return http.StatusUnsupportedMediaType, "pairing calls take application/json"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return http.StatusForbidden, "pairing calls need an Origin header"
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return http.StatusForbidden, "pairing calls need a valid Origin"
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	if !strings.EqualFold(u.Host, host) {
		return http.StatusForbidden, "cross-origin pairing calls are refused"
	}
	return 0, ""
}

// wantsJSON reports whether a GET asks for the state rather than the page.
func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}

func writePairJSON(w http.ResponseWriter, status int, v pairView) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
