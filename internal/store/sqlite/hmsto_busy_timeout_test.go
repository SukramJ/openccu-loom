// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"regexp"
	"strconv"
	"testing"
)

// hmStoParseBusyTimeoutMs pulls the busy_timeout value out of a pragma
// statement or DSN fragment. It parses the shipped string rather than
// re-deriving it from [busyTimeout], so a literal reintroduced anywhere in
// the pool's configuration is measured, not assumed.
func hmStoParseBusyTimeoutMs(t *testing.T, where, s string) int64 {
	t.Helper()
	m := regexp.MustCompile(`busy_timeout[ (=]+(\d+)`).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("%s carries no busy_timeout value: %q", where, s)
	}
	ms, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		t.Fatalf("%s busy_timeout %q: %v", where, m[1], err)
	}
	return ms
}

// TestBusyTimeout_PoolConfigurationStatesOneValue is the drift guard for the
// coupling health_probe.go documents but cannot enforce on its own: the DSN
// pragma every pooled connection carries, the applyPragmas priming statement
// and the probe's own deadline must all rest on [busyTimeout]. Raising the
// pragma without raising the probe's deadline makes the probe expire while
// SQLite is still legitimately waiting on a writer lock — the store escalates
// to UNHEALTHY and /health returns 503 on a database working as configured.
func TestBusyTimeout_PoolConfigurationStatesOneValue(t *testing.T) {
	t.Parallel()
	want := busyTimeout.Milliseconds()

	if got := hmStoParseBusyTimeoutMs(t, "connectionPragmas", connectionPragmas); got != want {
		t.Errorf("DSN busy_timeout = %d ms, want %d ms (busyTimeout)", got, want)
	}
	if got := hmStoParseBusyTimeoutMs(t, "FileDSN", FileDSN("/tmp/x.db")); got != want {
		t.Errorf("FileDSN busy_timeout = %d ms, want %d ms (busyTimeout)", got, want)
	}

	var found bool
	for _, p := range pragmaStatements("WAL") {
		if !regexp.MustCompile(`busy_timeout`).MatchString(p) {
			continue
		}
		found = true
		if got := hmStoParseBusyTimeoutMs(t, "applyPragmas", p); got != want {
			t.Errorf("applyPragmas busy_timeout = %d ms, want %d ms (busyTimeout)", got, want)
		}
	}
	if !found {
		t.Error("applyPragmas emits no busy_timeout pragma; the pool would run on SQLite's default (0)")
	}
}
