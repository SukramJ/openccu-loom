// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// txtValue returns the value of the first "key=value" TXT entry, or "".
func txtValue(txt []string, key string) (string, bool) {
	for _, e := range txt {
		if after, ok := strings.CutPrefix(e, key+"="); ok {
			return after, true
		}
	}
	return "", false
}

func TestMDNSServiceForBuildsDiscoveryTXT(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.Listen = ":8080"
	cfg.North.Discovery.MDNS.InstanceName = "loom-test"

	svc, ok := mdnsServiceFor(cfg, 3)
	if !ok {
		t.Fatal("mdnsServiceFor returned ok=false for a valid listen port")
	}
	if svc.Port != 8080 {
		t.Fatalf("Port = %d, want 8080", svc.Port)
	}
	if svc.InstanceName != "loom-test" {
		t.Fatalf("InstanceName = %q, want loom-test", svc.InstanceName)
	}

	want := map[string]string{
		"path":        "/api/v1",
		"api_version": handlers.APIVersion,
		"tls":         "0",
		"instance":    "loom-test",
		"centrals":    "3",
	}
	for k, v := range want {
		got, present := txtValue(svc.TXT, k)
		if !present {
			t.Errorf("TXT missing key %q (got %v)", k, svc.TXT)
			continue
		}
		if got != v {
			t.Errorf("TXT %q = %q, want %q", k, got, v)
		}
	}
}

// With InstanceName unset, the instance TXT falls back to the resolved
// (hostname-derived) label rather than an empty value.
func TestMDNSServiceForInstanceFallsBackToResolved(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.Listen = ":8080"
	cfg.North.Discovery.MDNS.InstanceName = ""

	svc, ok := mdnsServiceFor(cfg, 0)
	if !ok {
		t.Fatal("mdnsServiceFor returned ok=false")
	}
	inst, present := txtValue(svc.TXT, "instance")
	if !present || inst == "" {
		t.Fatalf("instance TXT should be the resolved non-empty label, got %q present=%v", inst, present)
	}
	if inst != cfg.North.Discovery.MDNS.ResolveInstanceName() {
		t.Errorf("instance TXT = %q, want resolved %q", inst, cfg.North.Discovery.MDNS.ResolveInstanceName())
	}
	if c, _ := txtValue(svc.TXT, "centrals"); c != "0" {
		t.Errorf("centrals TXT = %q, want 0", c)
	}
}

func TestMDNSServiceForNoPortSkips(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.Listen = "" // no host:port
	if _, ok := mdnsServiceFor(cfg, 1); ok {
		t.Fatal("mdnsServiceFor should return ok=false when no port is configured")
	}
}
