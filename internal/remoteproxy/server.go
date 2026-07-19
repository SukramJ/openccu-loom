// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package remoteproxy

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// Server routes browser requests to the configured remote instances.
// With exactly one instance it is a fully transparent proxy at "/";
// with several, each instance mounts under "/i/<name>/" and "/" serves
// the overview page.
// loom:reachable:reason="returned by New and driven by cmd/openccu-loom-remote via Handler and Start; the type heuristic does not follow constructor returns of the proxy binary"
type Server struct {
	instances []*instanceProxy
	poller    *poller
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
	if !single {
		s.poller = newPoller(s.instances, clock.New(), log)
	}
	return s, nil
}

// Start launches the background status pollers feeding the overview
// tiles; it is a no-op in transparent single-instance mode. The workers
// stop when ctx is canceled.
func (s *Server) Start(ctx context.Context) {
	if s.poller != nil {
		s.poller.start(ctx)
	}
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
