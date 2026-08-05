// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration_live

package integration

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLive_RegaAdHocScript runs a HomeMatic Script from a file against
// the live CCU and prints its raw answer. It exists to answer questions
// about ReGa behaviour that only the CCU can settle — what an accessor
// returns, whether a name resolves — without guessing from the docs.
//
//	OPENCCU_LOOM_LIVE_SCRIPT=/path/to/probe.fn \
//	go test -tags=integration_live ./tests/integration/ \
//	 -run TestLive_RegaAdHocScript -v
//
// The script is executed verbatim. Keep probes read-only.
func TestLive_RegaAdHocScript(t *testing.T) {
	path := os.Getenv("OPENCCU_LOOM_LIVE_SCRIPT")
	if path == "" {
		t.Skip("set OPENCCU_LOOM_LIVE_SCRIPT to a script file to run a probe")
	}
	env := checkLiveCCU(t)
	client := liveRegaClient(t, env)

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	out, err := runRegaBody(ctx, client, string(body))
	if err != nil {
		t.Fatalf("run %s: %v", path, err)
	}
	t.Logf("%s → %d bytes\n%s", path, len(out), out)
}
