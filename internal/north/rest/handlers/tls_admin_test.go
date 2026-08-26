// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// fakeTLSCertService captures the PEM blobs passed to SaveAndReload.
type fakeTLSCertService struct {
	savedCert []byte
	savedKey  []byte
	err       error
}

func (f *fakeTLSCertService) SaveAndReload(certPEM, keyPEM []byte) error {
	if f.err != nil {
		return f.err
	}
	f.savedCert = certPEM
	f.savedKey = keyPEM
	return nil
}

// buildMultipartRequest builds a multipart/form-data POST request with the
// given field values. Pass an empty string for a field to omit it entirely.
func buildMultipartRequest(t *testing.T, certValue, keyValue string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if certValue != "" {
		fw, err := w.CreateFormField("cert")
		if err != nil {
			t.Fatalf("create cert field: %v", err)
		}
		if _, err := fw.Write([]byte(certValue)); err != nil {
			t.Fatalf("write cert field: %v", err)
		}
	}
	if keyValue != "" {
		fw, err := w.CreateFormField("key")
		if err != nil {
			t.Fatalf("create key field: %v", err)
		}
		if _, err := fw.Write([]byte(keyValue)); err != nil {
			t.Fatalf("write key field: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/tls/certificate", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestUploadTLSCertificate_Success_Returns204AndForwardsPEMs(t *testing.T) {
	t.Parallel()
	svc := &fakeTLSCertService{}
	req := buildMultipartRequest(t, "CERTPEM", "KEYPEM")
	req = withIdentity(req, auth.Identity{Subject: "admin", Role: auth.RoleAdmin})
	w := httptest.NewRecorder()

	UploadTLSCertificate(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if string(svc.savedCert) != "CERTPEM" {
		t.Errorf("cert forwarded=%q, want %q", svc.savedCert, "CERTPEM")
	}
	if string(svc.savedKey) != "KEYPEM" {
		t.Errorf("key forwarded=%q, want %q", svc.savedKey, "KEYPEM")
	}
}

func TestUploadTLSCertificate_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := buildMultipartRequest(t, "CERTPEM", "KEYPEM")
	w := httptest.NewRecorder()

	UploadTLSCertificate(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUploadTLSCertificate_MissingKeyPart_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeTLSCertService{}
	// Only the cert part is present; key part is omitted.
	req := buildMultipartRequest(t, "CERTPEM", "")
	w := httptest.NewRecorder()

	UploadTLSCertificate(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUploadTLSCertificate_ServiceRejectsInvalidCert_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeTLSCertService{err: errors.New("tls: invalid key pair")}
	req := buildMultipartRequest(t, "CERTPEM", "KEYPEM")
	w := httptest.NewRecorder()

	UploadTLSCertificate(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUploadTLSCertificate_WithIdentityAndNilAudit_Returns204(t *testing.T) {
	t.Parallel()
	svc := &fakeTLSCertService{}
	req := buildMultipartRequest(t, "CERTPEM", "KEYPEM")
	req = withIdentity(req, auth.Identity{Subject: "operator", Role: auth.RoleOperator})
	w := httptest.NewRecorder()

	// audit.Recorder is nil — the handler must skip recording gracefully.
	UploadTLSCertificate(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}
