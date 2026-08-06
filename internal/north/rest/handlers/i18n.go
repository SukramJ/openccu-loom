// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// EntityNamePrefixes are the catalogue namespaces that name entities
// rather than describe user interface chrome.
//
// `discovery.` names the hub singletons and the derived data points the
// MQTT discovery plane publishes; `security.entity.` names the Security
// & Safety surfaces. Both were reachable only through MQTT, so a
// REST/WebSocket consumer had to keep its own copy of the same words —
// and the CCU integration's copy of "Alarmmeldungen" drifted from this
// one the moment either side was edited alone.
//
// The list is deliberately a projection rather than the whole
// catalogue: `nav.`, `login.` and `setup.` belong to the Config UI and
// mean nothing to a consumer building entities.
var EntityNamePrefixes = []string{"discovery.", "security.entity."}

// EntityNameCatalogue resolves the daemon's entity-naming vocabulary.
// Implemented by [github.com/SukramJ/openccu-loom/internal/i18n.Catalogs].
type EntityNameCatalogue interface {
	// Prefixed returns every message under one of prefixes for locale,
	// falling back per key to the default locale.
	Prefixed(locale string, prefixes ...string) map[string]string
	// ResolveLocale reports which locale actually answered.
	ResolveLocale(locale string) string
}

// EntityNames serves the entity-name catalogue.
//
// The daemon is the single naming authority (ADR 0046): it already
// names every device, channel and custom data point on the wire, and it
// already names its own hub and security entities — but only on the
// MQTT plane, where the names were resolved at publish time and never
// left. This endpoint hands the same vocabulary to a REST/WebSocket
// consumer so a Home Assistant integration renders the daemon's words
// instead of maintaining a third copy of them.
//
// `locale` picks the language; it defaults to the daemon's configured
// one. The response echoes the locale that actually answered, so a
// consumer can tell a real translation from the fallback.
//
// Values are templates as authored: `Connectivity {iface}` carries a
// placeholder only the caller can fill, because the daemon does not
// know which interface the consumer is naming.
func EntityNames(catalogue EntityNameCatalogue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if catalogue == nil {
			writeJSON(w, http.StatusOK, hmapi.EntityNameCatalogue{Entries: map[string]string{}})
			return
		}
		locale := r.URL.Query().Get("locale")
		entries := catalogue.Prefixed(locale, EntityNamePrefixes...)
		if entries == nil {
			entries = map[string]string{}
		}
		writeJSON(w, http.StatusOK, hmapi.EntityNameCatalogue{
			Locale:  catalogue.ResolveLocale(locale),
			Entries: entries,
		})
	}
}
