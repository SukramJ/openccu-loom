// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/deployment"
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

	svc, ok := mdnsServiceFor(cfg, mdnsSelf{kind: deployment.Standalone}, 3, []string{"11a0001234", "11b0009876"})
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
		"txtvers":     "1",
		"deploy":      "standalone",
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

	svc, ok := mdnsServiceFor(cfg, mdnsSelf{}, 0, nil)
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
	if _, ok := mdnsServiceFor(cfg, mdnsSelf{}, 1, nil); ok {
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

	txt := mdnsTXT(cfg, mdnsSelf{}, 2, []string{"11b0009876", "", "11a0001234"})
	if got, ok := txtValue(txt, "ccus"); !ok || got != "11a0001234,11b0009876" {
		t.Errorf("ccus = %q (present=%v), want sorted 11a0001234,11b0009876", got, ok)
	}

	txt = mdnsTXT(cfg, mdnsSelf{}, 0, nil)
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
	for sn := range strings.SplitSeq(v, ",") {
		if len(sn) != 10 {
			t.Errorf("partial serial %q survived truncation", sn)
		}
	}
}

// TestMDNSTXTSelfDescription pins the record's short form of the daemon's
// self-description: the deployment always, the ingress path only on a lite
// box, the login paths only when there are any, and a tls key that follows
// the listener instead of claiming plaintext.
func TestMDNSTXTSelfDescription(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.Listen = ":8119"

	lite := mdnsTXT(cfg, mdnsSelf{kind: deployment.LiteAddon, tls: true, loginPaths: []string{"pairing", "occulite_token"}}, 1, nil)
	for k, want := range map[string]string{
		"txtvers": "1", "deploy": "lite-addon", "ingress": "/addons/loom/", "auth": "pairing,occulite_token", "tls": "1",
	} {
		if got, ok := txtValue(lite, k); !ok || got != want {
			t.Errorf("lite-addon TXT %q = %q (present=%v), want %q", k, got, ok, want)
		}
	}
	if lite[0] != "txtvers=1" {
		t.Errorf("txtvers must be the record's first key (RFC 6763 section 6.7), got %q", lite[0])
	}

	plain := mdnsTXT(cfg, mdnsSelf{kind: deployment.CCUAddon}, 1, nil)
	if got, _ := txtValue(plain, "deploy"); got != "ccu-addon" {
		t.Errorf("deploy = %q, want ccu-addon", got)
	}
	if got, _ := txtValue(plain, "tls"); got != "0" {
		t.Errorf("tls = %q, want 0 without a TLS listener", got)
	}
	for _, k := range []string{"ingress", "auth"} {
		if v, ok := txtValue(plain, k); ok {
			t.Errorf("TXT %q = %q must be absent when there is nothing to say", k, v)
		}
	}
}
