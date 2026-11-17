// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// EditorComponentSource is the embedded device-editor component bundle.
type EditorComponentSource interface {
	Manifest() hmapi.EditorComponentManifest
}

// GetEditorComponent answers where the device editor's web component is,
// its content hash and the REST routes it calls (ADR 0089). A nil source
// is a build without the component bundle and answers 404.
func GetEditorComponent(src EditorComponentSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if src == nil {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Not found",
					"this build carries no editor component"))
			return
		}
		// The manifest changes with every release; an integration that
		// cached it would fetch a module the daemon no longer serves.
		w.Header().Set("Cache-Control", "no-store")
		JSON(w, http.StatusOK, src.Manifest())
	}
}
