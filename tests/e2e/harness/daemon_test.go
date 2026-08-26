// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package harness

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestStartupDeadlineHonoursTheEnvOverride pins the documented knob for both
// halves of start-up. The wait for the daemon's bound REST address used to cap
// itself at a hardcoded minute, so on a loaded runner — the situation
// OPENCCU_LOOM_E2E_STARTUP_DEADLINE exists for — the harness still failed at
// 60 s with "no rest.listen line", before the override could apply.
func TestStartupDeadlineHonoursTheEnvOverride(t *testing.T) {
	t.Setenv("OPENCCU_LOOM_E2E_STARTUP_DEADLINE", "180s")

	if got := startupDeadline(0); got != 180*time.Second {
		t.Errorf("startupDeadline(0) = %s, want the env override 180s", got)
	}
	if got := startupDeadline(5 * time.Second); got != 5*time.Second {
		t.Errorf("startupDeadline(5s) = %s; an explicit option must win over the env", got)
	}
}

// TestStartupDeadlineDefaults covers the two cases that must fall back to the
// compiled-in budget: no override at all, and an unparseable one.
func TestStartupDeadlineDefaults(t *testing.T) {
	t.Setenv("OPENCCU_LOOM_E2E_STARTUP_DEADLINE", "")
	if got := startupDeadline(0); got != 60*time.Second {
		t.Errorf("startupDeadline(0) = %s, want the 60s default", got)
	}

	t.Setenv("OPENCCU_LOOM_E2E_STARTUP_DEADLINE", "not-a-duration")
	if got := startupDeadline(0); got != 60*time.Second {
		t.Errorf("startupDeadline(0) with a malformed override = %s, want the 60s default", got)
	}
}

// TestRESTListenForOIDCCarriesAConcretePort pins the one place ":0" cannot be
// used. The OIDC block templates redirect_url from this value, so a dynamic
// listener would advertise 127.0.0.1:0 — an address nothing can be redirected
// to — while the daemon serves the callback somewhere else entirely.
func TestRESTListenForOIDCCarriesAConcretePort(t *testing.T) {
	if got := restListenFor(t, AuthBasic); got != ":0" {
		t.Errorf("non-OIDC listen = %q, want the dynamic \":0\"", got)
	}

	listen := restListenFor(t, AuthOIDC)
	port, err := strconv.Atoi(strings.TrimPrefix(listen, ":"))
	if err != nil || port <= 0 {
		t.Fatalf("OIDC listen = %q, want \":<port>\" with a real port", listen)
	}

	yaml := buildConfigYAML(configInputs{
		DataDir:    t.TempDir(),
		RESTListen: listen,
		UIListen:   ":0",
		AuthMode:   AuthOIDC,
		OIDCIssuer: "http://127.0.0.1:9999",
		CCUHost:    "127.0.0.1",
	})
	want := fmt.Sprintf("redirect_url: \"http://127.0.0.1:%d/api/v1/auth/oidc/callback\"", port)
	if !strings.Contains(yaml, want) {
		t.Errorf("config does not redirect to the port the daemon binds; want %q in:\n%s", want, yaml)
	}
	if strings.Contains(yaml, "http://127.0.0.1:0/") {
		t.Error("config advertises 127.0.0.1:0 as the OIDC redirect target")
	}
}
