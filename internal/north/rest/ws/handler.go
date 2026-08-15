// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"bufio"
	"crypto/sha1" //nolint:gosec // required by RFC 6455 handshake; see #20
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// handshakeMagic is the RFC 6455 accept-token constant.
const handshakeMagic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Handler upgrades incoming HTTP requests to WebSocket connections
// and hands them to [*Hub]. It is suitable as an http.Handler mount
// point (see NewRouter wiring).
//
// When allowedOrigins is non-empty each incoming cross-origin request
// that carries an Origin header must match one of the listed origins
// (scheme+host); cross-site browser handshakes are rejected with 403
// before the upgrade completes, closing the WebSocket CSRF vector that
// exists when session cookies are active. Same-origin handshakes (the
// Origin's host equals the request Host, or the proxy-forwarded
// X-Forwarded-Host behind a reverse proxy that rewrites Host) are always
// allowed — they are not a CSRF vector — so the embedded SPA connects on any
// authority the daemon is reached on (localhost, IP, hostname, or an external
// proxy host), not just the localhost self-origin. See [sameOriginHost].
// Requests without an Origin header are allowed even
// when the list is non-empty: CSRF is a browser-only attack vector and
// browsers always attach an Origin to WebSocket handshakes, so an absent
// Origin identifies a non-browser client (native app, server-side
// API-token client) that no cross-site page could have forged. Pass nil
// or an empty slice to skip the check entirely.
func Handler(hub *Hub, logger *slog.Logger, allowedOrigins []string) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	originSet := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		originSet[strings.ToLower(strings.TrimRight(o, "/"))] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "websocket upgrade required", http.StatusBadRequest)
			return
		}
		if !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
			http.Error(w, "connection upgrade required", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Sec-WebSocket-Version") != "13" {
			http.Error(w, "websocket version 13 required", http.StatusBadRequest)
			return
		}
		key := r.Header.Get("Sec-WebSocket-Key")
		if key == "" {
			http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
			return
		}
		// Only validate the Origin for cookie-authenticated browser handshakes.
		// A request carrying an Authorization header (Bearer/Basic) is never a
		// CSRF vector — CSRF rides ambient cookie auth, and a browser cannot set
		// an Authorization header on a WebSocket handshake — so it is exempt,
		// mirroring the CSRF middleware (auth.CSRFMiddleware). This lets the SPA
		// connect through the remote-proxy add-on, which injects a Bearer token
		// and strips the session cookie: across that proxy chain the browser's
		// external Origin cannot be reconciled with the daemon's internal Host,
		// yet the handshake is not a CSRF risk. A missing Origin (non-browser
		// client) is likewise allowed.
		if origin := r.Header.Get("Origin"); r.Header.Get("Authorization") == "" &&
			len(originSet) > 0 && origin != "" {
			u, err := url.Parse(origin)
			if err != nil {
				http.Error(w, "websocket origin invalid", http.StatusForbidden)
				return
			}
			// Same-origin handshakes are never a CSRF vector (CSRF requires a
			// *different* origin), so allow them regardless of the allow-list.
			// This lets the embedded SPA connect no matter which authority the
			// operator reaches the daemon on — IP, hostname, container name —
			// not just the localhost self-origin the allow-list derives. The
			// allow-list still gates genuinely cross-origin browser clients.
			if !sameOriginHost(u.Host, r) {
				normalized := strings.ToLower(u.Scheme + "://" + u.Host)
				if _, ok := originSet[normalized]; !ok {
					http.Error(w, "websocket origin not allowed", http.StatusForbidden)
					return
				}
			}
		}

		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack not supported", http.StatusInternalServerError)
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			http.Error(w, "hijack failed", http.StatusInternalServerError)
			return
		}

		accept := acceptKey(key)
		handshake := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
		if _, err := buf.WriteString(handshake); err != nil {
			_ = conn.Close()
			return
		}
		if err := buf.Flush(); err != nil {
			_ = conn.Close()
			return
		}

		br := bufio.NewReader(conn)
		bw := bufio.NewWriter(conn)
		c := newClient(conn, br, bw, hub, logger)
		// The identity resolved for the upgrade request is captured once and
		// gates every command this connection later dispatches. It is only
		// as current as the connection: the raw credential is not retained,
		// so it cannot be re-resolved here. A revocation reaches the socket
		// from the other side instead — see [Hub.CloseBySubject] — and the
		// in-band reauth op replaces it without a reconnect.
		if id, ok := auth.IdentityFrom(r.Context()); ok {
			c.SetIdentity(id)
		}
		hub.register(c)
		defer hub.deregister(c)

		done := make(chan struct{})
		go func() {
			c.writePump()
			close(done)
		}()
		//nolint:contextcheck // the WebSocket connection outlives the HTTP upgrade request; readPump/reauth use the connection-scoped context, not r.Context()
		c.readPump()
		c.close()
		<-done
	})
}

// sameOriginHost reports whether originHost identifies the same authority the
// client actually reached. That is the request Host directly, or — behind a
// reverse proxy that rewrites Host to the internal upstream — the external host
// the proxy records in X-Forwarded-Host. Without the forwarded-host arm the
// SPA's live WebSocket 403s in a reconnect loop behind any proxy that does not
// preserve the original Host header (Origin carries the external authority,
// r.Host the internal one, so they never match).
//
// Honouring X-Forwarded-Host does not open a cross-site hole: a browser cannot
// set that header on a WebSocket handshake (the WS API exposes no header knob,
// and the browser fills Origin itself), so a forged cross-origin page still
// presents its own Origin — which matches neither the request Host nor the
// proxy's forwarded host — and is rejected.
func sameOriginHost(originHost string, r *http.Request) bool {
	if strings.EqualFold(originHost, r.Host) {
		return true
	}
	// X-Forwarded-Host may carry a comma-separated proxy chain; the first entry
	// is the original client-facing host.
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		if first := strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0]); first != "" &&
			strings.EqualFold(originHost, first) {
			return true
		}
	}
	return false
}

func acceptKey(key string) string {
	h := sha1.New() //nolint:gosec // required by RFC 6455; see #20
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte(handshakeMagic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
