// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// countingOnboarding records which onboarding calls reached it.
type countingOnboarding struct{ probes, starts int }

func (c *countingOnboarding) Probe(context.Context, hmapi.CentralProbeRequest) (hmapi.CentralProbeResult, error) {
	c.probes++
	return hmapi.CentralProbeResult{SystemType: "openccu-lite", Ready: true}, nil
}

func (c *countingOnboarding) StartPairing(context.Context, hmapi.CentralPairingRequest) (hmapi.CentralPairingStarted, error) {
	c.starts++
	return hmapi.CentralPairingStarted{PairingID: "p1", Code: "123456"}, nil
}

func (*countingOnboarding) PairingStatus(context.Context, string, time.Duration) (hmapi.CentralPairingStatus, error) {
	return hmapi.CentralPairingStatus{State: "pending"}, nil
}

func (*countingOnboarding) CancelPairing(context.Context, string) error { return nil }

func (*countingOnboarding) PairingToken(string) (token, fingerprint string, err error) {
	return "", "", nil
}

func (*countingOnboarding) ForgetPairing(string) {}

// TestSetupOnboardingOnlyBeforeFirstRun pins the setup variants of probe
// and pairing, which are reachable without a session: they reach the
// onboarding only while first-run setup is required and allowed, and are
// refused — without contacting any address — once setup completed or the
// operator disabled it.
func TestSetupOnboardingOnlyBeforeFirstRun(t *testing.T) {
	cases := []struct {
		name     string
		required bool
		allowed  bool
		want     int
	}{
		{"setup pending", true, true, http.StatusOK},
		{"setup completed", false, true, http.StatusConflict},
		{"first-run disabled", true, false, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &SetupService{
				Users:           sqlite.NewUserStore(nil),
				Required:        func(context.Context) bool { return tc.required },
				FirstRunAllowed: func() bool { return tc.allowed },
			}
			o := &countingOnboarding{}

			w := httptest.NewRecorder()
			SetupProbeCentral(o, svc).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/setup/probe",
				strings.NewReader(`{"host":"192.0.2.1"}`)))
			if w.Code != tc.want {
				t.Errorf("probe: status %d, want %d (%s)", w.Code, tc.want, w.Body)
			}

			wantStart := tc.want
			if wantStart == http.StatusOK {
				wantStart = http.StatusCreated
			}
			w = httptest.NewRecorder()
			SetupStartCentralPairing(o, svc).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/setup/pairing",
				strings.NewReader(`{"host":"192.0.2.1"}`)))
			if w.Code != wantStart {
				t.Errorf("pairing: status %d, want %d (%s)", w.Code, wantStart, w.Body)
			}

			reached := 0
			if tc.want == http.StatusOK {
				reached = 1
			}
			if o.probes != reached || o.starts != reached {
				t.Errorf("onboarding reached %d probes, %d pairings; want %d each", o.probes, o.starts, reached)
			}
		})
	}
}
