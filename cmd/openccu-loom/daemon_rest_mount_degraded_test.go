// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	northbridge "github.com/SukramJ/openccu-loom/internal/north/bridge"
)

// TestMountRESTServerWithoutConfigStore pins that the REST phase comes up when
// the app database could not be opened.
//
// openLoomDB logs `loom_db.open_failed` and returns nil for a missing or
// read-only data dir, a failed migration, or a migration-lock timeout, and
// boot deliberately continues: every other consumer of that path is
// nil-guarded. The REST mount was not — it called Effective on a nil
// ConfigAdminService interface, which dispatches no method, so the daemon
// panicked on the boot goroutine (no recover in the composition root) and
// exited instead of serving REST in the documented degraded mode.
func TestMountRESTServerWithoutConfigStore(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.North.MCP.Enabled = false
	mdnsOff := false
	cfg.North.Discovery.MDNS.Enabled = &mdnsOff

	d := restMountDeps{
		reg:    central.NewRegistry(),
		reload: newReloadDeps(),
		// configSvc / userSvc / tokenSvc / centSvc all stay nil: that is
		// exactly the shape wireREST returns when the database is absent.
	}

	teardown := mountRESTServer(
		context.Background(), cfg, slog.New(slog.DiscardHandler),
		northbridge.NewRegistry(slog.New(slog.DiscardHandler)), d,
	)
	if teardown == nil {
		t.Fatal("mountRESTServer returned a nil teardown")
	}
	teardown()
}
