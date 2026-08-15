// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/channelflags"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// The daemon supports booting with an unusable app database: openLoomDB warns
// and returns nil, every consumer is expected to degrade. The two stores below
// are the ones the REST layer keeps mounted in that state, relying on their own
// `store == nil` guard to answer 503. Handing them a nil *pointer* boxes into a
// NON-nil interface, the guard goes dead, and both endpoints report success
// while persisting nothing — the worst of the three possible outcomes, because
// the SPA tells the operator the setting was saved.

func TestChannelFlagsWriterFromKeepsTheHandlerNilGuardAlive(t *testing.T) {
	t.Parallel()
	// What daemon.go holds when openLoomDB returned no handle.
	var absent *sqlitestore.ChannelFlagsStore

	writer := channelFlagsWriterFrom(absent)
	if writer != nil {
		t.Fatal("the composition root boxed a nil store into a non-nil interface")
	}

	// The overlay is built unconditionally, so it cannot stand in for the
	// missing durable store: only the store half of the guard can fire.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/devices/0001ABCD/channels/4/flags",
		strings.NewReader(`{"hidden":true}`))
	handlers.PutChannelFlags(nil, writer, channelflags.New(), nil)(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("PUT channel flags without a durable store answered %d, want 503 — "+
			"the operator's hide/lock choice is reported as saved and lost on restart", rec.Code)
	}
}

func TestVisibilityUnIgnoreStoreFromKeepsTheHandlerNilGuardAlive(t *testing.T) {
	t.Parallel()
	var absent *sqlitestore.VisibilityUnIgnoreStore

	store := visibilityUnIgnoreStoreFrom(absent)
	if store != nil {
		t.Fatal("the composition root boxed a nil store into a non-nil interface")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/visibility/unignore", http.NoBody)
	handlers.ListVisibilityUnIgnore(stubCentralLister{"ccu-test"}, store)(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET visibility/unignore without a store answered %d, want 503 — "+
			"an unavailable store must not read as 'no rules configured'", rec.Code)
	}
}

// stubCentralLister is the non-degraded half of the visibility wiring: the
// central registry is always present, so only the store can trip the guard.
type stubCentralLister []string

func (s stubCentralLister) Names() []string { return s }
