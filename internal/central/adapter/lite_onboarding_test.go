// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

func fakeHostPort(t *testing.T, f *litefake.Fake) (host string, port int) {
	t.Helper()
	u, err := url.Parse(f.URL())
	if err != nil {
		t.Fatal(err)
	}
	host, p, _ := net.SplitHostPort(u.Host)
	port, _ = strconv.Atoi(p)
	return host, port
}

// TestLiteProbeIdentifiesABoxBehindAnUnknownCertificate pins the first
// contact over HTTPS: a box whose certificate no CA signed is still
// identified, with the fingerprint it presented to pin, and a pairing
// started with that pin reaches the box while a different pin, or none,
// is refused as bad input.
func TestLiteProbeIdentifiesABoxBehindAnUnknownCertificate(t *testing.T) {
	t.Parallel()
	f := startTestFake(t, litefake.Options{TLS: true})
	host, port := fakeHostPort(t, f)
	fp := occulited.Fingerprint(f.Certificate())
	o := NewLiteOnboarding("loom-test", nil, "en", slog.New(slog.DiscardHandler))
	ctx := context.Background()

	res, err := o.Probe(ctx, hmapi.CentralProbeRequest{Host: host, Port: port, TLS: true})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if res.SystemType != "openccu-lite" || !res.Ready || res.Lite == nil || !res.Lite.PairingAvailable {
		t.Errorf("probe = %+v, want a ready openccu-lite box offering pairing", res)
	}
	if res.TLSFingerprint != fp {
		t.Errorf("fingerprint = %q, want the presented certificate's %q", res.TLSFingerprint, fp)
	}

	started, err := o.StartPairing(ctx, hmapi.CentralPairingRequest{Host: host, Port: port, TLS: true, TLSFingerprint: res.TLSFingerprint})
	if err != nil {
		t.Fatalf("StartPairing with the probed pin: %v", err)
	}
	if len(started.Code) != 6 {
		t.Errorf("pairing started = %+v, want a six-digit code", started)
	}

	other := "00" + fp[2:]
	if other == fp {
		other = "11" + fp[2:]
	}
	_, err = o.StartPairing(ctx, hmapi.CentralPairingRequest{Host: host, Port: port, TLS: true, TLSFingerprint: other})
	if !errors.Is(err, hmerr.ErrValidation) || !strings.Contains(err.Error(), "other than the pinned one") {
		t.Errorf("StartPairing with a wrong pin: %v, want a validation error naming the pin", err)
	}
	_, err = o.StartPairing(ctx, hmapi.CentralPairingRequest{Host: host, Port: port, TLS: true})
	if !errors.Is(err, hmerr.ErrValidation) || !strings.Contains(err.Error(), "pin its fingerprint") {
		t.Errorf("StartPairing over HTTPS without a pin: %v, want a validation error asking for the pin", err)
	}
}

// TestLiteProbeOverPlainHTTP pins the probe without TLS: the box is
// identified and no fingerprint is reported.
func TestLiteProbeOverPlainHTTP(t *testing.T) {
	t.Parallel()
	h, port := fakeHostPort(t, startTestFake(t, litefake.Options{}))
	o := NewLiteOnboarding("loom-test", nil, "en", slog.New(slog.DiscardHandler))

	res, err := o.Probe(context.Background(), hmapi.CentralProbeRequest{Host: h, Port: port})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if res.SystemType != "openccu-lite" || !res.Ready || res.TLSFingerprint != "" {
		t.Errorf("probe = %+v, want a ready openccu-lite box without a fingerprint", res)
	}
}

// TestLiteOnboardingRefusesAnAddressThatIsNotAHost pins the host rule on the
// onboarding: the address goes into the request URL, and an unauthenticated
// first-run wizard reaches this code. A host carrying credentials would
// otherwise still reach the named server — "loom@" + host passes
// JoinHostPort unchanged and becomes URL userinfo — so the negative control
// is the plain host, which the same fake answers.
func TestLiteOnboardingRefusesAnAddressThatIsNotAHost(t *testing.T) {
	t.Parallel()
	f := startTestFake(t, litefake.Options{})
	h, port := fakeHostPort(t, f)
	o := NewLiteOnboarding("loom-test", nil, "en", slog.New(slog.DiscardHandler))
	ctx := context.Background()

	for _, host := range []string{"loom@" + h, "http://" + h, h + "/api#", h + "?x="} {
		if _, err := o.Probe(ctx, hmapi.CentralProbeRequest{Host: host, Port: port}); !errors.Is(err, hmerr.ErrValidation) {
			t.Errorf("Probe(host %q) = %v, want a validation error", host, err)
		}
		if _, err := o.StartPairing(ctx, hmapi.CentralPairingRequest{Host: host, Port: port}); !errors.Is(err, hmerr.ErrValidation) {
			t.Errorf("StartPairing(host %q) = %v, want a validation error", host, err)
		}
	}
	for _, p := range []int{-1, 65536} {
		if _, err := o.Probe(ctx, hmapi.CentralProbeRequest{Host: h, Port: p}); !errors.Is(err, hmerr.ErrValidation) {
			t.Errorf("Probe(port %d) = %v, want a validation error", p, err)
		}
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("a refused address still reached the server: %d calls, first %+v", len(calls), calls[0])
	}

	if _, err := o.Probe(ctx, hmapi.CentralProbeRequest{Host: h, Port: port}); err != nil {
		t.Fatalf("Probe of the plain host: %v", err)
	}
	if len(f.Calls()) == 0 {
		t.Fatal("the plain host reached no server — the check above measured nothing")
	}
}

// TestLiteBaseURLTakesAnIPv6LiteralInItsURLForm covers the bracketed form
// the host rule accepts ("[::1]"): JoinHostPort brackets on its own.
func TestLiteBaseURLTakesAnIPv6LiteralInItsURLForm(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"::1", "[::1]"} {
		if got := liteBaseURL(host, 8181, false); got != "http://[::1]:8181" {
			t.Errorf("liteBaseURL(%q) = %q", host, got)
		}
	}
}
