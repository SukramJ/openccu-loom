// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

func serveEntityNames(t *testing.T, catalogue handlers.EntityNameCatalogue, query string) hmapi.EntityNameCatalogue {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/i18n/entities"+query, http.NoBody)
	handlers.EntityNames(catalogue)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body hmapi.EntityNameCatalogue
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func newCatalogs(t *testing.T) *i18n.Catalogs {
	t.Helper()
	c, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("i18n.NewCatalogs: %v", err)
	}
	return c
}

// TestEntityNamesServesTheNamingProjection asserts the endpoint serves
// the entity-naming namespaces and nothing else.
//
// The projection is the point: returning the whole catalogue would ship
// the Config UI's navigation, login and setup strings to every consumer
// building entities, and would make "which keys are a naming contract"
// unanswerable.
func TestEntityNamesServesTheNamingProjection(t *testing.T) {
	t.Parallel()

	body := serveEntityNames(t, newCatalogs(t), "")

	if len(body.Entries) == 0 {
		t.Fatal("catalogue is empty")
	}
	for key := range body.Entries {
		if !strings.HasPrefix(key, "discovery.") && !strings.HasPrefix(key, "security.entity.") {
			t.Errorf("key %q is outside the entity-naming projection", key)
		}
	}
	// The two families a consumer builds entities from.
	for _, key := range []string{
		"discovery.alarm_messages",
		"discovery.service_messages",
		"discovery.inbox",
		"discovery.connectivity",
		"discovery.system_health",
		"security.entity.state",
		"security.entity.class.smoke",
		"security.entity.last_alarm",
	} {
		if body.Entries[key] == "" {
			t.Errorf("entry %q missing from the catalogue", key)
		}
	}
}

// TestEntityNamesHonoursTheRequestedLocale pins the language selection
// and the echo a consumer reads to tell a translation from a fallback.
func TestEntityNamesHonoursTheRequestedLocale(t *testing.T) {
	t.Parallel()
	catalogs := newCatalogs(t)

	de := serveEntityNames(t, catalogs, "?locale=de")
	if de.Locale != "de" {
		t.Fatalf("locale = %q, want de", de.Locale)
	}
	if got := de.Entries["discovery.alarm_messages"]; got != "Alarmmeldungen" {
		t.Errorf("german name = %q", got)
	}

	en := serveEntityNames(t, catalogs, "?locale=en")
	if got := en.Entries["discovery.alarm_messages"]; got != "Alarm messages" {
		t.Errorf("english name = %q", got)
	}
}

// TestEntityNamesFallsBackForAnUnknownLocale pins the fallback: an
// unknown tag still answers with usable names, and the echoed locale
// says which language the consumer actually received.
func TestEntityNamesFallsBackForAnUnknownLocale(t *testing.T) {
	t.Parallel()

	body := serveEntityNames(t, newCatalogs(t), "?locale=kl")

	if body.Locale == "kl" {
		t.Fatal("echoed the requested locale although no catalogue exists for it")
	}
	if body.Entries["discovery.inbox"] == "" {
		t.Error("fallback produced no names")
	}
}

// TestEntityNamesKeepsPlaceholders pins the templates. A name like
// `Connectivity {iface}` can only be completed by the caller — the
// daemon does not know which interface is being named — so resolving or
// stripping the placeholder here would destroy the information.
func TestEntityNamesKeepsPlaceholders(t *testing.T) {
	t.Parallel()

	body := serveEntityNames(t, newCatalogs(t), "?locale=en")

	if got := body.Entries["discovery.connectivity"]; !strings.Contains(got, "{iface}") {
		t.Errorf("connectivity template = %q, want a {iface} placeholder", got)
	}
}

// TestEntityNamesWithoutACatalogueServesEmpty pins the degraded path: a
// daemon wired without catalogues answers an empty catalogue rather than
// a 404, so a consumer falls back to its own tokens without treating the
// call as an error.
func TestEntityNamesWithoutACatalogueServesEmpty(t *testing.T) {
	t.Parallel()

	body := serveEntityNames(t, nil, "")

	if len(body.Entries) != 0 {
		t.Fatalf("entries = %v, want empty", body.Entries)
	}
}
