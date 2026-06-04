// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package devicedetails

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
)

// ---------------------------------------------------------------------------
// minimal httptest helpers (mirror the pattern used in jsonrpc package tests)
// ---------------------------------------------------------------------------

type jsonrpcEnvelope struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
	ID     int            `json:"id"`
}

func newAdapterTestServer(t *testing.T, handlers map[string]func() any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env jsonrpcEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h, ok := handlers[env.Method]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"code": -32601, "message": "method not found"},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": h()})
	}))
}

// ---------------------------------------------------------------------------
// TestNewLoaderForJSONRPC — exercises NewLoaderForJSONRPC constructor
// ---------------------------------------------------------------------------

func TestNewLoaderForJSONRPC(t *testing.T) {
	t.Parallel()
	jc, err := jsonrpc.New(jsonrpc.Config{
		Endpoint: "http://127.0.0.1:9999/api", // unreachable — we never call it
	})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}

	cache := New()
	logger := slog.New(slog.DiscardHandler)
	loader := NewLoaderForJSONRPC(cache, jc, "test-central", logger)
	if loader == nil {
		t.Fatal("NewLoaderForJSONRPC returned nil")
	}
}

// ---------------------------------------------------------------------------
// jsonClientAdapter — GetDeviceDetails, GetAllRoomsRaw, GetAllFunctionsRaw
// These go through the jsonrpc.Client HTTP layer using an httptest server.
// ---------------------------------------------------------------------------

func TestJsonClientAdapterGetDeviceDetails(t *testing.T) {
	t.Parallel()
	want := []map[string]any{
		{"address": "VCU001", "name": "Dev1", "id": "1", "interface": "HmIP-RF", "channels": []any{}},
	}
	srv := newAdapterTestServer(t, map[string]func() any{
		"Device.listAllDetail": func() any { return want },
	})
	defer srv.Close()

	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	adapter := &jsonClientAdapter{jc: jc}
	got, err := adapter.GetDeviceDetails(context.Background())
	if err != nil {
		t.Fatalf("GetDeviceDetails: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d device(s), want 1", len(got))
	}
}

func TestJsonClientAdapterGetAllRoomsRaw(t *testing.T) {
	t.Parallel()
	want := []map[string]any{
		{"id": "10", "name": "Wohnzimmer", "channelIds": []string{"ch1"}},
	}
	srv := newAdapterTestServer(t, map[string]func() any{
		"Room.getAll": func() any { return want },
	})
	defer srv.Close()

	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	adapter := &jsonClientAdapter{jc: jc}
	got, err := adapter.GetAllRoomsRaw(context.Background())
	if err != nil {
		t.Fatalf("GetAllRoomsRaw: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Wohnzimmer" {
		t.Errorf("unexpected rooms: %+v", got)
	}
}

func TestJsonClientAdapterGetAllFunctionsRaw(t *testing.T) {
	t.Parallel()
	want := []map[string]any{
		{"id": "20", "name": "Heizung", "channelIds": []string{"ch2"}},
	}
	srv := newAdapterTestServer(t, map[string]func() any{
		"Subsection.getAll": func() any { return want },
	})
	defer srv.Close()

	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	adapter := &jsonClientAdapter{jc: jc}
	got, err := adapter.GetAllFunctionsRaw(context.Background())
	if err != nil {
		t.Fatalf("GetAllFunctionsRaw: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Heizung" {
		t.Errorf("unexpected functions: %+v", got)
	}
}

// TestJsonClientAdapterGetAllRoomsRaw_Error exercises the error path.
func TestJsonClientAdapterGetAllRoomsRaw_Error(t *testing.T) {
	t.Parallel()
	// Server returns a JSON-RPC error for Room.getAll.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": -32603, "message": "internal error"},
		})
	}))
	defer srv.Close()

	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	adapter := &jsonClientAdapter{jc: jc}
	_, err = adapter.GetAllRoomsRaw(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestJsonClientAdapterGetAllFunctionsRaw_Error exercises the error path.
func TestJsonClientAdapterGetAllFunctionsRaw_Error(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": -32603, "message": "internal error"},
		})
	}))
	defer srv.Close()

	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	adapter := &jsonClientAdapter{jc: jc}
	_, err = adapter.GetAllFunctionsRaw(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// sentinel to use errors.Is in calling code (keeps linter happy).
var _ = errors.New
