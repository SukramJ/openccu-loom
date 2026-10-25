// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package rest

import (
	"net/http"
	"sync"
	"time"

	"github.com/SukramJ/go-fabric-ui/fabricui"
)

// NodeAPI mounts go-fabric-ui's node routes under o.Prefix on a mux of
// their own and returns it, with one loom rule in front of the window
// route: the node's own boot window may be replaced.
//
// An uncommissioned node opens its own commissioning window at start
// (go-fabric's facade, adopted in ADR 0084 phase 3) — on the configured
// credentials, or on credentials nobody holds when none are configured.
// go-fabric-ui's POST /node/pairing/window answers 409 while any window is
// open, so without this rule the operator's first request after a boot is
// refused by the boot window, and with no configured passcode the bridge
// cannot be paired at all until that window times out. The daemon's own
// opener replaced the boot window and refused only a window the operator
// had opened and that was still running; this keeps that behaviour: a
// request that meets an open window which is the node's own and not one
// the operator opened less than its duration ago revokes it first, and
// the handler opens the operator's window on it. A commissioner's window
// (AdministratorCommissioning) is never touched — it is not the node's
// own — and the handler answers its 409.
func NodeAPI(o fabricui.Options) (http.Handler, error) {
	mux := http.NewServeMux()
	if err := fabricui.Mount(mux, o); err != nil {
		return nil, err
	}
	if o.Node == nil {
		return mux, nil
	}
	prefix := o.Prefix
	if prefix == "" {
		prefix = fabricui.DefaultPrefix
	}
	rep := &bootWindowReplacer{node: o.Node}
	windowPath := prefix + "/node/pairing/window"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != windowPath {
			mux.ServeHTTP(w, r)
			return
		}
		rep.beforeOpen(r)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		mux.ServeHTTP(rec, r)
		if rec.status == http.StatusOK {
			rep.opened()
		}
	}), nil
}

// bootWindowReplacer remembers until when the window the operator opened
// last runs, so a request inside that time meets the handler's 409 rather
// than restarting the window under a pairing in progress.
type bootWindowReplacer struct {
	node fabricui.NodeSource
	mu   sync.Mutex
	// until is when the operator's last window expires; zero before the
	// first one.
	until time.Time
}

func (b *bootWindowReplacer) beforeOpen(r *http.Request) {
	window := b.node.CommissioningWindow()
	if window == nil || !window.IsOpen() || !window.IsOwnWindow() {
		return
	}
	b.mu.Lock()
	running := time.Now().Before(b.until)
	b.mu.Unlock()
	if running {
		return
	}
	// The boot window, or an operator window past its duration that the
	// facade has not yet closed: the handler's open replaces it. The
	// revoke runs the node's window observer, so the pages and the hub
	// see window_closed before the window_opened that follows.
	_ = window.RevokeWindow(r.Context())
}

func (b *bootWindowReplacer) opened() {
	window := b.node.CommissioningWindow()
	if window == nil {
		return
	}
	b.mu.Lock()
	b.until = time.Now().Add(time.Duration(window.RequestedDurationSeconds()) * time.Second)
	b.mu.Unlock()
}

// statusRecorder captures the status the handler wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
