// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rest

import (
	"net/http"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// TestOpenAPIDeclaresEveryStatusCodeTheHandlersEmit pins, for the
// operations whose handlers were found emitting a status their spec entry
// did not list, that assets/openapi.yaml now declares each such code. The
// request validator never checks responses, so a generated client treats
// an undeclared code as impossible and surfaces it as a generic failure.
// Each row names the handler branch that produces the code.
func TestOpenAPIDeclaresEveryStatusCodeTheHandlersEmit(t *testing.T) {
	t.Parallel()
	_, thisFile, _, _ := runtime.Caller(0)
	specPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "assets", "openapi.yaml")
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromFile(specPath)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}

	cases := []struct {
		method, path string
		code         int
		branch       string
	}{
		{http.MethodDelete, "/sysvars/{name}", http.StatusBadRequest, "requireMutationHub: central required (multiple CCUs)"},
		{http.MethodPut, "/sysvars/{name}", http.StatusBadRequest, "requireMutationHub: central required (multiple CCUs)"},
		{http.MethodPost, "/alarm/zones/{id}/acknowledge", http.StatusConflict, "engine.ErrNoIncident"},
		{http.MethodPost, "/alarm/outputs/{id}/test", http.StatusBadRequest, "DecodeJSONStatus on a malformed body"},
		{http.MethodPost, "/alarm/outputs/{id}/test", http.StatusBadGateway, "TestFire upstream failure"},
		{http.MethodPost, "/install-mode/search", http.StatusBadRequest, "DecodeJSONStatus on a malformed body"},
		{http.MethodGet, "/auth/oidc/callback", http.StatusSeeOther, "http.Redirect(..., StatusSeeOther) on success and on error"},
		{http.MethodPost, "/devices/{addr}/channels/{no}/config/import", http.StatusLocked, "enforceEditLock on a MASTER/LINK snapshot"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path+" "+strconv.Itoa(tc.code), func(t *testing.T) {
			t.Parallel()
			item := doc.Paths.Find(tc.path)
			if item == nil {
				t.Fatalf("path %s not in spec", tc.path)
			}
			op := item.GetOperation(tc.method)
			if op == nil {
				t.Fatalf("%s %s not in spec", tc.method, tc.path)
			}
			if op.Responses.Status(tc.code) == nil {
				t.Fatalf("%s %s: handler emits %d (%s) but the spec declares only %v",
					tc.method, tc.path, tc.code, tc.branch, declaredCodes(op))
			}
		})
	}
}

func declaredCodes(op *openapi3.Operation) []string {
	codes := make([]string, 0, op.Responses.Len())
	for code := range op.Responses.Map() {
		codes = append(codes, code)
	}
	return codes
}
