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
// When allowedOrigins is non-empty each incoming request must carry an
// Origin header whose scheme+host matches one of the listed origins.
// Requests from browsers that would carry a cross-site Origin are
// rejected with 403 before the handshake completes, closing the
// WebSocket CSRF vector that exists when session cookies are active.
// Pass nil or an empty slice to skip the check (API-token-only
// deployments without browser sessions).
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
		if len(originSet) > 0 {
			origin := r.Header.Get("Origin")
			if origin == "" {
				http.Error(w, "websocket origin required", http.StatusForbidden)
				return
			}
			u, err := url.Parse(origin)
			if err != nil {
				http.Error(w, "websocket origin invalid", http.StatusForbidden)
				return
			}
			normalized := strings.ToLower(u.Scheme + "://" + u.Host)
			if _, ok := originSet[normalized]; !ok {
				http.Error(w, "websocket origin not allowed", http.StatusForbidden)
				return
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

func acceptKey(key string) string {
	h := sha1.New() //nolint:gosec // required by RFC 6455; see #20
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte(handshakeMagic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
