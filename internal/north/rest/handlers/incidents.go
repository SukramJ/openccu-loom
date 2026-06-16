// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Incident is an alias for the canonical DTO in pkg/hmapi.
type Incident = hmapi.Incident

// IncidentsReader is an alias for the canonical interface in pkg/interfaces.
type IncidentsReader = interfaces.IncidentsReader

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
