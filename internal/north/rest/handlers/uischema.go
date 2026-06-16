// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// UISchemaService is an alias for the canonical interface in pkg/interfaces.
type UISchemaService = interfaces.UISchemaService

// UISchemaRequest is an alias for the canonical DTO in pkg/hmapi.
type UISchemaRequest = hmapi.UISchemaRequest

// ErrUISchemaNotFound is an alias for the sentinel in pkg/hmapi.
var ErrUISchemaNotFound = hmapi.ErrUISchemaNotFound

// --- DTO aliases ---------------------------------------------------

// UISchema is an alias for the canonical DTO in pkg/hmapi.
type UISchema = hmapi.UISchema

// UISchemaSubsetGroup is an alias for the canonical DTO in pkg/hmapi.
type UISchemaSubsetGroup = hmapi.UISchemaSubsetGroup

// UISchemaSubsetOpt is an alias for the canonical DTO in pkg/hmapi.
type UISchemaSubsetOpt = hmapi.UISchemaSubsetOpt

// UISchemaChannel is an alias for the canonical DTO in pkg/hmapi.
type UISchemaChannel = hmapi.UISchemaChannel

// UISchemaGroup is an alias for the canonical DTO in pkg/hmapi.
type UISchemaGroup = hmapi.UISchemaGroup

// UISchemaParameter is an alias for the canonical DTO in pkg/hmapi.
type UISchemaParameter = hmapi.UISchemaParameter

// UISchemaPreset is an alias for the canonical DTO in pkg/hmapi.
type UISchemaPreset = hmapi.UISchemaPreset

// UISchemaTimePreset is an alias for the canonical DTO in pkg/hmapi.
type UISchemaTimePreset = hmapi.UISchemaTimePreset

// UISchemaValueListEntry is an alias for the canonical DTO in pkg/hmapi.
type UISchemaValueListEntry = hmapi.UISchemaValueListEntry

// UISchemaParameterOps is an alias for the canonical DTO in pkg/hmapi.
type UISchemaParameterOps = hmapi.UISchemaParameterOps

// UISchemaParameterFlags is an alias for the canonical DTO in pkg/hmapi.
type UISchemaParameterFlags = hmapi.UISchemaParameterFlags

// UISchemaVisibility is an alias for the canonical DTO in pkg/hmapi.
type UISchemaVisibility = hmapi.UISchemaVisibility

// UISchemaCrossValidation is an alias for the canonical DTO in pkg/hmapi.
type UISchemaCrossValidation = hmapi.UISchemaCrossValidation

// UISchemaProfile is an alias for the canonical DTO in pkg/hmapi.
type UISchemaProfile = hmapi.UISchemaProfile

// --- HTTP glue ----------------------------------------------------

// UISchemaHandler serves GET /devices/{addr}/channels/{no}/ui-schema.
func UISchemaHandler(svc UISchemaService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "UI schema service unavailable", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		noStr := chi.URLParam(r, "no")
		no, err := strconv.Atoi(noStr)
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid channel number", noStr))
			return
		}
		locale := r.URL.Query().Get("locale")
		if locale == "" {
			locale = "en"
		}
		paramset := r.URL.Query().Get("paramset")
		if paramset == "" {
			paramset = "VALUES"
		}
		peer := r.URL.Query().Get("peer")
		expert := r.URL.Query().Get("expert") == "true" ||
			r.URL.Query().Get("expert") == "1"
		schema, err := svc.UISchema(r.Context(), UISchemaRequest{
			Address:  addr,
			Channel:  no,
			Paramset: paramset,
			Peer:     peer,
			Locale:   locale,
			Expert:   expert,
		})
		if err != nil {
			if errors.Is(err, ErrUISchemaNotFound) {
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Channel not found", addr+"/"+noStr))
				return
			}
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "UI schema failed", err.Error()))
			return
		}
		JSON(w, http.StatusOK, schema)
	}
}
