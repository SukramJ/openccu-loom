// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"regexp"
	"strings"
	"testing"
)

// TestCCUAddonLighttpdDropinProxiesTheDaemon pins the openccu-lite
// web-server drop-in (ADR 0078). occulited validates the fragment
// against an allowlist and installs a root-owned copy, so a directive
// outside the set its addons documentation admits would silently leave
// the box without the ingress path — the whole file would be rejected.
// Pinned here: the fragment uses only the four directives it needs, it
// proxies /addons/loom/ to the loopback on the port the rc.d script
// binds, it strips the prefix (the daemon mounts at the root), and it
// keeps the WebSocket upgrade enabled for the event stream.
func TestCCUAddonLighttpdDropinProxiesTheDaemon(t *testing.T) {
	t.Parallel()

	dropin := readCCUAddonFile(t, "addon/etc/lighttpd.conf")

	rc := readCCUAddonFile(t, "rc.d/openccu-loom")
	listen := regexp.MustCompile(`(?m)^export OPENCCU_LOOM_REST_LISTEN=:(\d+)$`).FindStringSubmatch(rc)
	if listen == nil {
		t.Fatal("rc.d/openccu-loom exports no OPENCCU_LOOM_REST_LISTEN")
	}

	for _, needle := range []string{
		`"host" => "127.0.0.1"`,
		`"port" => ` + listen[1],
		`"upgrade" => "enable"`,
		`"map-urlpath" => ( "/addons/loom/" => "/" )`,
		`"^/addons/loom/?$" => "/addons/loom/app/"`,
	} {
		if !strings.Contains(dropin, needle) {
			t.Errorf("addon/etc/lighttpd.conf misses %q", needle)
		}
	}

	// Only the four needed directives: anything else risks occulited's
	// validator rejecting the whole fragment on the box.
	allowed := map[string]bool{"url.redirect": true, "proxy.server": true, "proxy.header": true, "proxy.forwarded": true}
	directive := regexp.MustCompile(`^\s*([a-z][a-z0-9.-]*)\s*\+?=`)
	for _, line := range strings.Split(dropin, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "$") || line == "}" {
			continue
		}
		m := directive.FindStringSubmatch(line)
		if m == nil {
			continue // continuation of a multi-line value
		}
		if !allowed[m[1]] {
			t.Errorf("addon/etc/lighttpd.conf uses %q, which this fragment deliberately avoids", m[1])
		}
	}

	if strings.Contains(dropin, "/addons/openccu-loom") {
		t.Error("the proxy path collides with occulited's /addons/openccu-loom CGI alias (the settings card)")
	}
}
