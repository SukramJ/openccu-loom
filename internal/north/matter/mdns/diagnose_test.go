// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mdns

import (
	"net"
	"strings"
	"testing"
)

// codes collects the finding codes for a compact assertion.
func codes(findings []Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Code)
	}
	return out
}

func hasCode(findings []Finding, code string) bool {
	for _, f := range findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

// TestDiagnoseFindsTheSilentPairingFailures drives the conditions that
// each produced a successful-looking advertisement and a controller that
// never showed the bridge.
func TestDiagnoseFindsTheSilentPairingFailures(t *testing.T) {
	t.Parallel()

	healthy := Service{
		InstanceName: "9C71D38FBE48F2E5",
		ServiceType:  ServiceTypeCommissionable,
		Port:         5540,
		HostName:     "loom",
		Addresses:    []net.IP{net.ParseIP("fe80::1"), net.ParseIP("192.168.1.40")},
		Subtypes:     []string{"_L3840", "_S15", "_CM"},
	}

	cases := []struct {
		name     string
		services []Service
		wantCode string
		wantSev  Severity
	}{
		{
			name:     "advertising nothing at all",
			services: nil,
			wantCode: "no_services",
			wantSev:  SeverityError,
		},
		{
			name: "no subtype PTRs",
			services: []Service{func() Service {
				s := healthy
				s.Subtypes = nil
				return s
			}()},
			wantCode: "no_subtypes",
			wantSev:  SeverityError,
		},
		{
			name: "commissioning off 5540",
			services: []Service{func() Service {
				s := healthy
				s.Port = 5541
				return s
			}()},
			wantCode: "non_default_commissioning_port",
			wantSev:  SeverityWarning,
		},
		{
			name: "only a container-internal address",
			services: []Service{func() Service {
				s := healthy
				s.Addresses = []net.IP{net.ParseIP("172.18.0.4")}
				return s
			}()},
			wantCode: "container_internal_address",
			wantSev:  SeverityError, // nothing routable remains
		},
		{
			name: "IPv4 only",
			services: []Service{func() Service {
				s := healthy
				s.Addresses = []net.IP{net.ParseIP("192.168.1.40")}
				return s
			}()},
			wantCode: "no_ipv6_address",
			wantSev:  SeverityWarning,
		},
		{
			name: "no address at all",
			services: []Service{func() Service {
				s := healthy
				s.Addresses = nil
				return s
			}()},
			wantCode: "no_addresses",
			wantSev:  SeverityError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			findings := Diagnose(tc.services)
			var got *Finding
			for i := range findings {
				if findings[i].Code == tc.wantCode {
					got = &findings[i]
					break
				}
			}
			if got == nil {
				t.Fatalf("no %q finding; got %v", tc.wantCode, codes(findings))
			}
			if got.Severity != tc.wantSev {
				t.Errorf("%s severity = %q, want %q", tc.wantCode, got.Severity, tc.wantSev)
			}
			if strings.TrimSpace(got.Message) == "" {
				t.Errorf("%s carries no message — an operator needs to know what to do about it", tc.wantCode)
			}
		})
	}
}

// TestDiagnoseKeepsQuietOnAHealthyAdvertisement pins the other half: a
// correct advertisement must not produce noise, or the findings stop
// being read.
func TestDiagnoseKeepsQuietOnAHealthyAdvertisement(t *testing.T) {
	t.Parallel()

	findings := Diagnose([]Service{
		{
			InstanceName: "9C71D38FBE48F2E5",
			ServiceType:  ServiceTypeCommissionable,
			Port:         5540,
			Addresses:    []net.IP{net.ParseIP("fe80::1"), net.ParseIP("192.168.1.40")},
			Subtypes:     []string{"_L3840", "_S15", "_CM"},
		},
		{
			InstanceName: "9C71D38FBE48F2E5-0000000012345678",
			ServiceType:  ServiceTypeOperational,
			Port:         5540,
			Addresses:    []net.IP{net.ParseIP("fe80::1"), net.ParseIP("192.168.1.40")},
		},
	})
	if len(findings) != 0 {
		t.Errorf("a healthy advertisement produced %v — findings an operator learns to ignore are worse "+
			"than none", codes(findings))
	}
}

// TestDiagnoseTreatsALANAddressAsRoutable guards the check that would
// otherwise fire on every deployment: a LAN address is private but
// perfectly reachable, and flagging it would drown the container case
// this exists for.
func TestDiagnoseTreatsALANAddressAsRoutable(t *testing.T) {
	t.Parallel()

	for _, addr := range []string{"192.168.1.40", "10.0.0.5", "172.16.4.29"} {
		findings := Diagnose([]Service{{
			InstanceName: "x",
			ServiceType:  ServiceTypeOperational,
			Port:         5540,
			Addresses:    []net.IP{net.ParseIP("fe80::1"), net.ParseIP(addr)},
		}})
		if addr == "172.16.4.29" {
			// Documented as part of Docker's default pool, so it is
			// expected to be flagged — pinned so the boundary is a
			// decision rather than an accident.
			if !hasCode(findings, "container_internal_address") {
				t.Errorf("%s: want the documented container range to be flagged", addr)
			}
			continue
		}
		if hasCode(findings, "container_internal_address") {
			t.Errorf("%s was flagged as container-internal, but it is an ordinary LAN address", addr)
		}
	}
}
