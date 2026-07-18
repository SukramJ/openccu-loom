// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// decodedPathRouting makes chi route on the percent-DECODED request
// path, so every chi.URLParam yields the decoded value.
//
// By default chi prefers r.URL.RawPath when it is set (i.e. whenever
// the request contained any percent-escape), which hands handlers the
// still-encoded segment. A conformant client percent-encodes path
// segments — the SPA wraps every path ID in encodeURIComponent — so
// channel addresses arrive as `0052E409A90362%3A4`, alarm output IDs
// as `a%7Cb%3A1%7Cc`, CDP wire names as `STATE%403`, and room/sysvar
// names as `K%C3%BCche`. Handlers that compare such params against
// stored identities miss and answer 404 for perfectly valid resources.
//
// Setting rctx.RoutePath before the mux matches forces the whole route
// tree (including nested subrouters) onto r.URL.Path, which net/http
// has already decoded. Trade-off: a path segment containing an encoded
// slash (%2F) is now treated as a separator — no identity in this API
// legitimately contains a slash, and such a request previously failed
// the handler lookup anyway.
//
// This must stay the first middleware on the router; per-handler
// url.PathUnescape calls must NOT be reintroduced on top of it (they
// would decode twice and corrupt values containing a literal '%').
func decodedPathRouting(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePath == "" {
			rctx.RoutePath = r.URL.Path
		}
		next.ServeHTTP(w, r)
	})
}
