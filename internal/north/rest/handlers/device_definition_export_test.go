// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"context"
	"errors"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/model/device/definitionexport"
)

// stubDefinitionExportService is a configurable stub for DeviceDefinitionExportService.
type stubDefinitionExportService struct {
	model string
	zip   []byte
	err   error
}

func (s *stubDefinitionExportService) ExportDefinition(_ context.Context, _ string) (model string, zip []byte, err error) {
	return s.model, s.zip, s.err
}

func TestExportDeviceDefinition_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/devices/DEV001/export-definition", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	ExportDeviceDefinition(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestExportDeviceDefinition_MissingAddr_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubDefinitionExportService{model: "HmIP-PS", zip: []byte("ZIPDATA")}
	req := httptest.NewRequest(http.MethodGet, "/devices//export-definition", http.NoBody)
	// Explicitly set addr to empty string to trigger the missing-param guard.
	req = req.WithContext(chiContext(req, map[string]string{"addr": ""}))
	w := httptest.NewRecorder()
	ExportDeviceDefinition(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestExportDeviceDefinition_Success_Returns200WithHeaders(t *testing.T) {
	t.Parallel()
	zipPayload := []byte("FAKEZIPBYTES")
	svc := &stubDefinitionExportService{model: "HmIP-PS", zip: zipPayload}
	req := httptest.NewRequest(http.MethodGet, "/devices/DEV001/export-definition", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	ExportDeviceDefinition(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	wantDisp := `attachment; filename=HmIP-PS.zip`
	if cd := w.Header().Get("Content-Disposition"); cd != wantDisp {
		t.Errorf("Content-Disposition = %q, want %q", cd, wantDisp)
	}
	if !bytes.Equal(w.Body.Bytes(), zipPayload) {
		t.Errorf("body = %q, want %q", w.Body.Bytes(), zipPayload)
	}
}

// TestExportDeviceDefinition_ModelWithQuoteIsEscapedInContentDisposition
// verifies that a device model string containing a double quote
// cannot break out of the filename parameter and inject extra
// Content-Disposition directives.
func TestExportDeviceDefinition_ModelWithQuoteIsEscapedInContentDisposition(t *testing.T) {
	t.Parallel()
	zipPayload := []byte("ARCHIVEBYTES")
	trickyModel := `evil"; filename="pwned.sh`
	svc := &stubDefinitionExportService{model: trickyModel, zip: zipPayload}
	req := httptest.NewRequest(http.MethodGet, "/devices/DEV001/export-definition", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	ExportDeviceDefinition(svc).ServeHTTP(w, req)

	cd := w.Header().Get("Content-Disposition")
	_, params, err := mime.ParseMediaType(cd)
	if err != nil {
		t.Fatalf("Content-Disposition is not parseable: %q: %v", cd, err)
	}
	wantFilename := trickyModel + ".zip"
	if params["filename"] != wantFilename {
		t.Errorf("filename param = %q, want %q (header: %q)", params["filename"], wantFilename, cd)
	}
}

func TestExportDeviceDefinition_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := &stubDefinitionExportService{err: definitionexport.ErrDeviceNotFound}
	req := httptest.NewRequest(http.MethodGet, "/devices/UNKNOWN/export-definition", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "UNKNOWN"}))
	w := httptest.NewRecorder()
	ExportDeviceDefinition(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestExportDeviceDefinition_WrappedDeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	// Verify that errors.Is traversal works (wrapped sentinel).
	wrapped := errors.Join(errors.New("outer"), definitionexport.ErrDeviceNotFound)
	svc := &stubDefinitionExportService{err: wrapped}
	req := httptest.NewRequest(http.MethodGet, "/devices/UNKNOWN/export-definition", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "UNKNOWN"}))
	w := httptest.NewRecorder()
	ExportDeviceDefinition(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for wrapped ErrDeviceNotFound, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestExportDeviceDefinition_GenericError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubDefinitionExportService{err: errors.New("CCU unreachable")}
	req := httptest.NewRequest(http.MethodGet, "/devices/DEV001/export-definition", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	ExportDeviceDefinition(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestExportDeviceDefinition_ChiRouterIntegration mounts the handler on a real
// chi router and issues a GET with a path parameter to confirm chi.URLParam
// extraction works end-to-end.
func TestExportDeviceDefinition_ChiRouterIntegration(t *testing.T) {
	t.Parallel()

	zipPayload := []byte("ARCHIVEBYTES")
	svc := &stubDefinitionExportService{model: "HmIP-WTH-2", zip: zipPayload}

	r := chi.NewRouter()
	r.Get("/devices/{addr}/export-definition", ExportDeviceDefinition(svc))

	req := httptest.NewRequest(http.MethodGet, "/devices/ABCD1234/export-definition", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("chi router: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	wantDisp := `attachment; filename=HmIP-WTH-2.zip`
	if cd := w.Header().Get("Content-Disposition"); cd != wantDisp {
		t.Errorf("Content-Disposition = %q, want %q", cd, wantDisp)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "application/zip") {
		t.Errorf("Content-Type missing application/zip: %q", w.Header().Get("Content-Type"))
	}
}
