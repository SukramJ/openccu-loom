// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package middleware

import (
	"net/http"
	"strings"
)

// CORSConfig governs the CORS middleware.
type CORSConfig struct {
	Origins          []string // "*" matches any; comma-separated list forbidden
	AllowCredentials bool
	MaxAgeSeconds    int
}

// NormalizeOrigin canonicalises one allowed-origin entry — or an incoming
// Origin header — into the form both origin gates key on: lower-cased, no
// surrounding blanks, no trailing slash. A browser sends a bare
// `scheme://host[:port]`, but an operator copies the origin the way the
// address bar shows it (`https://ha.example.com/`), and both spellings must
// mean the same thing.
//
// The same configured list feeds this middleware and the WebSocket handshake
// gate, so both derive their lookup key through this function: when each
// derived its own, a trailing slash passed the WebSocket gate while every
// cross-origin REST call was denied with no header, no error and no log line.
func NormalizeOrigin(o string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(o), "/"))
}

// CORS returns a middleware that handles CORS preflight + adds the
// `Access-Control-Allow-*` headers when the request Origin is
// whitelisted.
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	allowAll := false
	whitelist := make(map[string]struct{}, len(cfg.Origins))
	for _, o := range cfg.Origins {
		if o == "*" {
			allowAll = true
			continue
		}
		if norm := NormalizeOrigin(o); norm != "" {
			whitelist[norm] = struct{}{}
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				allowed := allowAll
				if !allowed {
					_, allowed = whitelist[NormalizeOrigin(origin)]
				}
				if allowed {
					if allowAll && !cfg.AllowCredentials {
						w.Header().Set("Access-Control-Allow-Origin", "*")
					} else {
						w.Header().Set("Access-Control-Allow-Origin", origin)
						w.Header().Add("Vary", "Origin")
					}
					if cfg.AllowCredentials {
						w.Header().Set("Access-Control-Allow-Credentials", "true")
					}
				}
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				if h := r.Header.Get("Access-Control-Request-Headers"); h != "" {
					w.Header().Set("Access-Control-Allow-Headers", h)
				}
				if cfg.MaxAgeSeconds > 0 {
					w.Header().Set("Access-Control-Max-Age", itoa(cfg.MaxAgeSeconds))
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
