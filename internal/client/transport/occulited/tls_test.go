// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited_test

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
)

func tlsServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{}")
	}))
	// The refused handshakes are the point of the tests; keep them out
	// of the log.
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func getVia(c *occulited.Client, u string) error {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, u, http.NoBody)
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// TestTLSPinAcceptsOnlyThePinnedCertificate pins the fingerprint check:
// the pinned certificate passes without CA verification, any other one
// fails with the presented fingerprint, and without a pin an unknown CA
// fails while still revealing the fingerprint for a first contact.
func TestTLSPinAcceptsOnlyThePinnedCertificate(t *testing.T) {
	srv := tlsServer(t)
	fp := occulited.Fingerprint(srv.Certificate())

	good, err := occulited.New(occulited.Config{BaseURL: srv.URL, TLSFingerprint: strings.ToUpper(fp[:2]) + ":" + fp[2:]})
	if err != nil {
		t.Fatal(err)
	}
	if err := getVia(good, srv.URL); err != nil {
		t.Errorf("pinned certificate refused: %v", err)
	}

	wrong := strings.Repeat("00", 32)
	bad, err := occulited.New(occulited.Config{BaseURL: srv.URL, TLSFingerprint: wrong})
	if err != nil {
		t.Fatal(err)
	}
	err = getVia(bad, srv.URL)
	var fe *occulited.FingerprintError
	if !errors.Is(err, occulited.ErrFingerprintMismatch) || !errors.As(err, &fe) || fe.Got != fp || fe.Want != wrong {
		t.Errorf("wrong pin: %v", err)
	}
	if got, ok := occulited.FingerprintOf(err); !ok || got != fp {
		t.Errorf("FingerprintFromError(pin) %q %v", got, ok)
	}

	plain := newClient(t, srv.URL, "")
	err = getVia(plain, srv.URL)
	if err == nil {
		t.Fatal("unknown CA accepted without a pin")
	}
	if got, ok := occulited.FingerprintOf(err); !ok || got != fp {
		t.Errorf("FingerprintFromError(unverified) %q %v", got, ok)
	}

	insecure, err := occulited.New(occulited.Config{BaseURL: srv.URL, InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := getVia(insecure, srv.URL); err != nil {
		t.Errorf("InsecureSkipVerify refused: %v", err)
	}
}

func TestDetectCapturesTheFingerprint(t *testing.T) {
	srv := tlsServer(t)
	c, err := occulited.New(occulited.Config{BaseURL: srv.URL, InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d.TLSFingerprint != occulited.Fingerprint(srv.Certificate()) {
		t.Errorf("fingerprint %q", d.TLSFingerprint)
	}
}
