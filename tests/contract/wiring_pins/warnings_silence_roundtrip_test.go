// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/warnings"
)

type pinnedHealth struct{}

func (pinnedHealth) Snapshot() []health.Component {
	return []health.Component{{Name: "mqtt", Status: health.StatusUnhealthy}}
}

// TestWarningsSilenceRoundTripThroughTheRouter pins the whole
// collaboration the way production wires it: the warnings aggregator
// over a health source, the sqlite silence store, and the REST routes
// behind the real auth middleware. The effect asserted is the feature's
// contract — a silence set by one authenticated user annotates that
// user's list, survives a re-read from the store, and stays invisible
// to another user.
func TestWarningsSilenceRoundTripThroughTheRouter(t *testing.T) {
	db, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	svc := warnings.New(pinnedHealth{}, nil, nil, sqlite.NewWarningSilenceStore(db))

	tokens := auth.NewMemoryTokenStore(map[string]auth.Identity{
		"tok-markus": {Subject: "markus", Scheme: auth.SchemeBearer, Role: auth.RoleAdmin},
		"tok-gast":   {Subject: "gast", Scheme: auth.SchemeBearer, Role: auth.RoleViewer},
	})
	mw := auth.NewMiddleware(nil, tokens)
	router := rest.NewRouter(rest.Deps{
		Warnings:    svc,
		AuthResolve: mw.Resolve,
		AuthRequire: mw.Require,
	})

	do := func(method, path, token string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	list := func(token string) []warnings.Warning {
		t.Helper()
		rec := do(http.MethodGet, "/api/v1/warnings", token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /warnings as %s: %d %s", token, rec.Code, rec.Body.String())
		}
		var resp struct {
			Items []warnings.Warning `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Items
	}

	items := list("tok-markus")
	if len(items) != 1 || items[0].ID != "health:mqtt" || items[0].Silenced {
		t.Fatalf("initial list: %+v", items)
	}

	if rec := do(http.MethodPut, "/api/v1/warnings/health:mqtt/silence", "tok-markus", []byte(`{"days":7}`)); rec.Code != http.StatusNoContent {
		t.Fatalf("silence: %d %s", rec.Code, rec.Body.String())
	}
	if items = list("tok-markus"); !items[0].Silenced || items[0].SilencedUntil == nil {
		t.Fatalf("after silence: %+v", items)
	}
	if items = list("tok-gast"); items[0].Silenced {
		t.Fatalf("silence leaked to another user: %+v", items)
	}

	// Silencing an id that is not active answers 404, bad period 400.
	if rec := do(http.MethodPut, "/api/v1/warnings/health:gone/silence", "tok-markus", []byte(`{"days":7}`)); rec.Code != http.StatusNotFound {
		t.Fatalf("inactive id: %d", rec.Code)
	}
	if rec := do(http.MethodPut, "/api/v1/warnings/health:mqtt/silence", "tok-markus", []byte(`{"days":3}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad period: %d", rec.Code)
	}

	if rec := do(http.MethodDelete, "/api/v1/warnings/health:mqtt/silence", "tok-markus", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unsilence: %d", rec.Code)
	}
	if items = list("tok-markus"); items[0].Silenced {
		t.Fatalf("after unsilence: %+v", items)
	}
}
