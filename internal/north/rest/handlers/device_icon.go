// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// DeviceIconProxy fetches a device's type-icon image from the owning
// CCU. ok is false when the device, its central, or the upstream image
// is unavailable; the caller then answers 404 and the SPA falls back to
// a generic glyph.
type DeviceIconProxy interface {
	Icon(ctx context.Context, address string) (data []byte, contentType string, ok bool)
}

// GetDeviceIcon proxies the device-type icon PNG the CCU serves under
// /config/img/devices/250/<file>. The route is intentionally
// unauthenticated — it exposes only non-sensitive device model artwork
// and must resolve from an <img> tag regardless of the auth scheme in
// use; the equivalent device-icon proxy in the reference integration is
// unauthenticated for the same reason.
func GetDeviceIcon(proxy DeviceIconProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if proxy == nil {
			http.NotFound(w, r)
			return
		}
		addr := chi.URLParam(r, "addr")
		data, contentType, ok := proxy.Icon(r.Context(), addr)
		if !ok || len(data) == 0 {
			http.NotFound(w, r)
			return
		}
		if contentType == "" {
			contentType = "image/png"
		}
		w.Header().Set("Content-Type", contentType)
		// Icons are effectively static; let the browser cache aggressively.
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}
