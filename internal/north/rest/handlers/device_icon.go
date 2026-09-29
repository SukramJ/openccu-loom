// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// DeviceIconProxy returns a device's type-icon image — from the embedded
// data snapshot, or else from the owning CCU. ok is false when the
// device or its image is unavailable; the caller then answers 404 and
// the SPA falls back to a generic glyph.
type DeviceIconProxy interface {
	Icon(ctx context.Context, address string) (data []byte, contentType string, ok bool)
}

// GetDeviceIcon serves the device-type icon PNG — the artwork the CCU
// WebUI shows from /config/img/devices/250/<file>. The route is mounted
// behind authentication: the image is not sensitive, but a 200 versus a
// 404 would tell an anonymous caller whether an address exists. The SPA
// renders it from an <img> tag, which carries the same-origin session
// cookie; a bearer-only client fetches the image itself.
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
