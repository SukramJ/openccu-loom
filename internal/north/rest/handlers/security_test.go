// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stubSecurityDomain records the arguments SetSourceOverride was called
// with so a test can assert the handler forwarded the ref components
// unmodified.
type stubSecurityDomain struct {
	central     string
	interfaceID string
	channelAddr string
	parameter   string
	calls       int
}

func (s *stubSecurityDomain) Snapshot() security.Snapshot { return security.Snapshot{} }

func (s *stubSecurityDomain) Faults() []security.Fault { return nil }

func (s *stubSecurityDomain) AcknowledgeFault(context.Context, string, string) (bool, error) {
	return false, nil
}

func (s *stubSecurityDomain) Sources(context.Context) []security.SourceView { return nil }

func (s *stubSecurityDomain) SetSourceOverride(
	_ context.Context, central, interfaceID, channelAddress, parameter string,
	_ hmenum.SecurityClass, _ bool, _ string,
) error {
	s.calls++
	s.central = central
	s.interfaceID = interfaceID
	s.channelAddr = channelAddress
	s.parameter = parameter
	return nil
}

// TestPutSecuritySourceOverride_RefWithLiteralPercent pins that the
// handler consumes the already-decoded chi param verbatim. The router's
// decodedPathRouting middleware decodes the path exactly once, so a
// central name carrying a literal '%' reaches the handler as-is; a
// second percent-decode here either fails the request outright or
// silently rewrites the tuple before it reaches the domain.
func TestPutSecuritySourceOverride_RefWithLiteralPercent(t *testing.T) {
	t.Parallel()
	d := &stubSecurityDomain{}
	// What decodedPathRouting hands the handler after its single decode
	// of "Haus%20100%25%7C...": the literal, fully decoded ref.
	ref := "Haus 100%|Haus 100%-HmIP-RF|0052E409A90362:1|STATE"

	req := httptest.NewRequest(http.MethodPut, "/api/v1/security/sources/x",
		bytes.NewBufferString(`{"class":"technical"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "ref", ref)

	w := httptest.NewRecorder()
	PutSecuritySourceOverride(d, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	if d.calls != 1 {
		t.Fatalf("SetSourceOverride calls = %d, want 1", d.calls)
	}
	if d.central != "Haus 100%" || d.interfaceID != "Haus 100%-HmIP-RF" ||
		d.channelAddr != "0052E409A90362:1" || d.parameter != "STATE" {
		t.Fatalf("override tuple = %q/%q/%q/%q, want the four ref components unmodified",
			d.central, d.interfaceID, d.channelAddr, d.parameter)
	}
}

// TestPutSecuritySourceOverride_RefWithEscapeLikeComponent pins the
// silent-corruption half: a component that still looks like a
// percent-escape after the router's decode must not be decoded again.
func TestPutSecuritySourceOverride_RefWithEscapeLikeComponent(t *testing.T) {
	t.Parallel()
	d := &stubSecurityDomain{}
	// The client sent "%2541" for a central literally named "%41"; the
	// router's single decode already produced "%41".
	ref := "%41|iface|0052E409A90362:1|STATE"

	req := httptest.NewRequest(http.MethodPut, "/api/v1/security/sources/x",
		bytes.NewBufferString(`{"class":"technical"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "ref", ref)

	w := httptest.NewRecorder()
	PutSecuritySourceOverride(d, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	if d.central != "%41" {
		t.Fatalf("central = %q, want %q (no second decode)", d.central, "%41")
	}
}
