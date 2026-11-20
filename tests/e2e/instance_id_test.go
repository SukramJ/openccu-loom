// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"regexp"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestInstanceIDReachesInfo pins that the installation identity the daemon
// keeps in its database arrives at `GET /api/v1/info`.
//
// Black-box against the built binary: the handler test proves only that
// Info renders the string it is handed; whether the composition root reads
// the identity from the database and hands it over is what this checks. Two
// daemons get two identities — the property a client tells two daemons
// serving the same CCU apart by (ADR 0058).
func TestInstanceIDReachesInfo(t *testing.T) {
	t.Parallel()
	shape := regexp.MustCompile(`^[0-9a-f]{32}$`)

	a := infoField(t, harness.Start(t, harness.Options{}), "instance_id")
	if !shape.MatchString(a) {
		t.Fatalf("instance_id = %q, want 32 lower-case hex digits", a)
	}
	b := infoField(t, harness.Start(t, harness.Options{}), "instance_id")
	if a == b {
		t.Fatalf("two daemons with their own databases share the instance_id %q", a)
	}
}
