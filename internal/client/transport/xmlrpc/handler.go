// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package xmlrpc

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// DefaultRequestLimit bounds the size of an incoming XML-RPC request.
const DefaultRequestLimit = 10 * 1024 * 1024

// Handler is the HTTP handler that serves the daemon's own XML-RPC
// callback endpoint. It parses <methodCall>, dispatches through a Mux,
// and serialises the response.
//
// The handler embeds its Mux for convenience — callers register methods
// directly on the Handler.
type Handler struct {
	Mux    *Mux
	Logger *slog.Logger

	// RequestLimit bounds the incoming body. Zero = [DefaultRequestLimit].
	RequestLimit int64
}

// NewHandler constructs a Handler with a fresh [Mux].
func NewHandler() *Handler {
	return &Handler{Mux: NewMux()}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logger := h.Logger
	if logger == nil {
		logger = slog.Default()
	}

	limit := h.RequestLimit
	if limit <= 0 {
		limit = DefaultRequestLimit
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		logger.Debug(
			"xmlrpc handler: read request failed",
			slog.String("remote", r.RemoteAddr),
			slog.String("err", err.Error()),
		)
		http.Error(w, "request too large or unreadable", http.StatusBadRequest)
		return
	}

	call, err := DecodeCall(bytes.NewReader(raw))
	if err != nil {
		logger.Debug(
			"xmlrpc handler: decode request failed",
			slog.String("remote", r.RemoteAddr),
			slog.String("err", err.Error()),
		)
		http.Error(w, "decode request failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	result, err := h.Mux.Dispatch(r.Context(), call.Method, call.Params)
	resp := &MethodResponse{}
	if err != nil {
		resp.Fault = asFault(err)
		logger.Debug(
			"xmlrpc handler: method returned fault",
			slog.String("method", call.Method),
			slog.Int("code", resp.Fault.Code),
			slog.String("message", resp.Fault.Message),
		)
	} else {
		if result == nil {
			result = NilValue{}
		}
		resp.Params = []Value{result}
	}

	var body bytes.Buffer
	if err := EncodeResponse(&body, resp); err != nil {
		logger.Error(
			"xmlrpc handler: encode response failed",
			slog.String("method", call.Method),
			slog.String("err", err.Error()),
		)
		http.Error(w, "encode response failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/xml; charset=ISO-8859-1")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body.Bytes()); err != nil {
		logger.Debug(
			"xmlrpc handler: write response failed",
			slog.String("remote", r.RemoteAddr),
			slog.String("err", err.Error()),
		)
	}
}

// asFault collapses any error into an XMLRPCFault. Errors that are
// already faults pass through untouched; anything else maps to code -1.
func asFault(err error) *hmerr.XMLRPCFault {
	if fault, ok := errors.AsType[*hmerr.XMLRPCFault](err); ok {
		return fault
	}
	return &hmerr.XMLRPCFault{Code: -1, Message: err.Error()}
}
