// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package middleware

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
)

// DeviceCentralLocator answers whether a central exists and holds a device.
// `(*central.Registry).HoldsDevice` satisfies it.
type DeviceCentralLocator interface {
	// HoldsDevice reports whether central is registered and whether it holds
	// the device behind address (a device or a channel address).
	HoldsDevice(central, address string) (known, holds bool)
}

// DeviceCentralQuery is the query parameter a device-addressed route reads its
// central from.
const DeviceCentralQuery = "central"

// DeviceCentral binds a device-addressed route to the central the request
// names in `?central=`. Device addresses are not unique across CCUs, so a
// caller that knows which CCU it means (an editor opened from one CCU's entry)
// must be answered from that CCU or not at all:
//
//   - no `?central=`: the request passes unscoped, and the address resolves on
//     the first central that holds it, as before;
//   - an unknown central, or one that does not hold the address: 404, before
//     the handler runs;
//   - otherwise the request is bound with [hmreqctx.WithCentralScope], which
//     every address lookup on the request path honours, and its log records
//     carry the central.
//
// It reads the `{addr}` URL parameter, so it must be attached per route (chi
// `With`) after the route matched, like [CentralScope]. A nil locator answers
// 503 for a request that names a central, rather than serving it unscoped.
func DeviceCentral(loc DeviceCentralLocator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			name := strings.TrimSpace(r.URL.Query().Get(DeviceCentralQuery))
			if name == "" {
				next.ServeHTTP(w, r)
				return
			}
			if loc == nil {
				problem.Write(w, http.StatusServiceUnavailable,
					problem.New(problem.TypeServiceUnready, r, "Central binding unavailable",
						"the daemon cannot resolve centrals for device routes"))
				return
			}
			addr := chi.URLParam(r, "addr")
			known, holds := loc.HoldsDevice(name, addr)
			switch {
			case !known:
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Unknown central", "no central named "+name))
				return
			case !holds:
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Device not on this central",
						"central "+name+" holds no device "+addr))
				return
			}
			r = r.WithContext(hmreqctx.WithCentralScope(r.Context(), name))
			next.ServeHTTP(w, SetCentralName(r, name))
		})
	}
}
