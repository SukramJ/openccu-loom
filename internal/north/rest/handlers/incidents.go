// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"time"
)

// Incident is one diagnostic entry surfaced at `/incidents`.
type Incident struct {
	ID        string    `json:"id"`
	When      time.Time `json:"when"`
	Component string    `json:"component"`
	Severity  string    `json:"severity"`
	Summary   string    `json:"summary"`
	Detail    string    `json:"detail,omitempty"`
}

// IncidentsReader is the narrow facade `/incidents` depends on.
type IncidentsReader interface {
	Incidents() []Incident
}

// ListIncidents renders the current incident list.
func ListIncidents(reader IncidentsReader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if reader == nil {
			JSON(w, http.StatusOK, []Incident{})
			return
		}
		JSON(w, http.StatusOK, reader.Incidents())
	}
}
