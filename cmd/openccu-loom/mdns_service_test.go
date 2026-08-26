// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"fmt"
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
	cfg.North.REST.Listen = ":8119"
	cfg.North.Discovery.MDNS.InstanceName = "loom-test"

	svc, ok := mdnsServiceFor(cfg, 3, []string{"11a0001234", "11b0009876"})
	if !ok {
		t.Fatal("mdnsServiceFor returned ok=false for a valid listen port")
	}
	if svc.Port != 8119 {
		t.Fatalf("Port = %d, want 8119", svc.Port)
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
	cfg.North.REST.Listen = ":8119"
	cfg.North.Discovery.MDNS.InstanceName = ""

	svc, ok := mdnsServiceFor(cfg, 0, nil)
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
	if _, ok := mdnsServiceFor(cfg, 1, nil); ok {
		t.Fatal("mdnsServiceFor should return ok=false when no port is configured")
	}
}

// TestMDNSTXTCCUsKey pins the ccus TXT contract: resolved serial
// suffixes appear sorted and comma-separated, unresolved (empty)
// entries are dropped, the key is absent with no resolved serials, and
// the value truncates at whole entries below the DNS 255-byte string
// limit.
func TestMDNSTXTCCUsKey(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.Listen = ":8119"

	txt := mdnsTXT(cfg, 2, []string{"11b0009876", "", "11a0001234"})
	if got, ok := txtValue(txt, "ccus"); !ok || got != "11a0001234,11b0009876" {
		t.Errorf("ccus = %q (present=%v), want sorted 11a0001234,11b0009876", got, ok)
	}

	txt = mdnsTXT(cfg, 0, nil)
	if _, ok := txtValue(txt, "ccus"); ok {
		t.Error("ccus key must be absent without resolved serials")
	}

	many := make([]string, 40) // 40*10 + separators > 255-byte value budget
	for i := range many {
		many[i] = fmt.Sprintf("%010d", i)
	}
	v := mdnsCCUsValue(many)
	if len("ccus=")+len(v) > 255 {
		t.Fatalf("ccus TXT string exceeds 255 bytes: %d", len("ccus=")+len(v))
	}
	if v == "" || strings.HasSuffix(v, ",") || strings.Contains(v, ",,") {
		t.Errorf("truncated value malformed: %q", v)
	}
	for _, sn := range strings.Split(v, ",") {
		if len(sn) != 10 {
			t.Errorf("partial serial %q survived truncation", sn)
		}
	}
}
