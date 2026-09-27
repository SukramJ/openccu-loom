// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

// liteSystemCentral is a lite central wired through WireCentrals together
// with the registry and the backup adapter the daemon hands it.
type liteSystemCentral struct {
	unit   *central.Unit
	reg    *central.Registry
	backup *adapter.BackupAdapter
}

func startLiteSystemCentral(t *testing.T, fake *litefake.Fake, token string) liteSystemCentral {
	t.Helper()
	u, err := url.Parse(fake.URL())
	if err != nil {
		t.Fatalf("parse fake URL: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	cfg := &config.Config{Centrals: []config.CentralConfig{{
		Name: "box", Host: host, JSONRPCPort: port,
		SystemType: hmenum.SystemTypeOpenCCULite, APIToken: token,
		Interfaces: []config.InterfaceSpec{{Name: "BidCos-RF"}, {Name: "HmIP-RF"}},
	}}}
	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: "box"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Registry.Register: %v", err)
	}
	storage, err := adapter.NewFilesystemBackupStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemBackupStorage: %v", err)
	}
	backup := adapter.NewBackupAdapter(reg).SetStorage(storage)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr, err := adapter.WireCentrals(ctx, cfg, reg,
		adapter.WireDeps{Writer: client.NewValueWriter(), Backup: backup}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("WireCentrals: %v", err)
	}
	t.Cleanup(mgr.Teardown)
	waitLiteReady(t, unit)
	return liteSystemCentral{unit: unit, reg: reg, backup: backup}
}

func calledPath(fake *litefake.Fake, path string) bool {
	calls := fake.Calls()
	for i := range calls {
		if calls[i].Path == path {
			return true
		}
	}
	return false
}

// TestLiteRebootNeedsPowerScope pins the management ports the composition
// root installs on a lite central: a token with the power scope reboots
// the box; one without it is refused with the scope named, and the request
// never reaches the box.
func TestLiteRebootNeedsPowerScope(t *testing.T) {
	t.Run("granted", func(t *testing.T) {
		fake := startFake(t, litefake.Options{})
		c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
		if err := adapter.NewCCUMaintenanceDomain(c.reg).RebootCCU(context.Background(), "box"); err != nil {
			t.Fatalf("RebootCCU: %v", err)
		}
		if !calledPath(fake, "/api/system/v1/reboot") {
			t.Error("the reboot never reached the box")
		}
	})
	t.Run("missing", func(t *testing.T) {
		fake := startFake(t, litefake.Options{Tokens: map[string][]string{
			litefake.DefaultToken: {"rpc:read", "meta:read", "system:read"},
		}})
		c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
		err := adapter.NewCCUMaintenanceDomain(c.reg).RebootCCU(context.Background(), "box")
		fe, ok := errors.AsType[*hmerr.FeatureUnavailableError](err)
		if !ok || fe.Scope != "power" || fe.Feature != hmenum.FeatureSystemReboot {
			t.Fatalf("err = %v, want FeatureUnavailableError for system.reboot naming scope power", err)
		}
		if !errors.Is(err, backends.ErrUnsupported) {
			t.Error("the refusal does not match backends.ErrUnsupported, which the REST handler maps today")
		}
		if calledPath(fake, "/api/system/v1/reboot") {
			t.Error("a refused reboot still reached the box")
		}
	})
}

// TestLiteBackupCreateDownloadsArchive pins the backup path of a lite
// central end to end: the adapter the daemon builds creates a backup,
// which is the box's own archive stored under the box's own file name.
func TestLiteBackupCreateDownloadsArchive(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
	id, err := c.backup.CreateBackupForCentral(context.Background(), "box")
	if err != nil {
		t.Fatalf("CreateBackupForCentral: %v", err)
	}
	entries, err := c.backup.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range entries {
		if e.ID == id {
			if e.Filename != litefake.BackupFileName || e.Bytes == 0 {
				t.Errorf("stored backup = %q, %d bytes; want %q with content", e.Filename, e.Bytes, litefake.BackupFileName)
			}
			return
		}
	}
	t.Fatalf("backup %s is not in the list %+v", id, entries)
}

// TestLiteRestoreChecksThenApplies pins the restore path: the box checks
// the archive and applies the checked upload.
func TestLiteRestoreChecksThenApplies(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
	id, err := c.backup.CreateBackupForCentral(context.Background(), "box")
	if err != nil {
		t.Fatalf("CreateBackupForCentral: %v", err)
	}
	if _, err := c.backup.Restore(context.Background(), id); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(fake.Restores()) != 1 {
		t.Errorf("applied restores = %v, want exactly one", fake.Restores())
	}
}

// TestLiteRestoreRefusesArchiveNeedingRecoveryKey pins that an archive
// the box can open only with its recovery key is refused as invalid input
// and never applied.
func TestLiteRestoreRefusesArchiveNeedingRecoveryKey(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
	id, err := c.backup.CreateBackupForCentral(context.Background(), "box")
	if err != nil {
		t.Fatalf("CreateBackupForCentral: %v", err)
	}
	fake.SetRestoreNeedsRecoveryKey(true)
	_, err = c.backup.Restore(context.Background(), id)
	if !errors.Is(err, hmerr.ErrValidation) {
		t.Fatalf("Restore err = %v, want a validation refusal", err)
	}
	if len(fake.Restores()) != 0 {
		t.Errorf("an archive needing the recovery key was applied: %v", fake.Restores())
	}
}

// TestLiteSystemUpdateMapsFeed pins the system-update state of a lite
// central: the running openccu-lite version and the release the box's
// feed offers reach the hub model at bring-up.
func TestLiteSystemUpdateMapsFeed(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	fake.SetUpdateAvailable(&litefake.AvailableUpdate{Version: "9.9.9", Newer: true})
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)

	oc, err := occulited.New(occulited.Config{BaseURL: fake.URL(), Token: litefake.DefaultToken})
	if err != nil {
		t.Fatalf("occulited.New: %v", err)
	}
	state, err := oc.SystemUpdate(context.Background())
	if err != nil {
		t.Fatalf("SystemUpdate: %v", err)
	}
	var info struct {
		current, available string
		newer, feed        bool
	}
	waitFor(t, 5*time.Second, "the update state in the hub model", func() bool {
		got, observed := c.unit.HubModel.Update.UpdateInfo()
		info.current, info.available, info.newer, info.feed = got.CurrentFirmware, got.AvailableFirmware, got.UpdateAvailable, got.CheckScriptAvailable
		return observed
	})
	if info.current != state.Running.Lite || info.available != "9.9.9" || !info.newer || !info.feed {
		t.Errorf("update info = %+v, want current %q, available 9.9.9, newer, feed", info, state.Running.Lite)
	}
}

// TestLiteBackupStatusFromTargets pins the on-box backup status the hub
// reports: running while a target runs, else the newest run's outcome;
// and a token that cannot read the targets is refused naming system:read.
func TestLiteBackupStatusFromTargets(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		targets []litefake.BackupTarget
		want    string
	}{
		{"no run yet", []litefake.BackupTarget{{ID: "a", State: litefake.BackupTargetState{State: "idle"}}}, "idle"},
		{"newest failed", []litefake.BackupTarget{
			{ID: "a", LastBackup: &litefake.LastBackup{At: "2026-09-01T02:00:00Z", OK: true}},
			{ID: "b", LastBackup: &litefake.LastBackup{At: "2026-09-02T02:00:00Z", OK: false, Error: "full"}},
		}, "failed"},
		{"newest ok", []litefake.BackupTarget{
			{ID: "a", LastBackup: &litefake.LastBackup{At: "2026-09-03T02:00:00Z", OK: true}},
			{ID: "b", LastBackup: &litefake.LastBackup{At: "2026-09-02T02:00:00Z", OK: false}},
		}, "ok"},
		{"running wins", []litefake.BackupTarget{
			{ID: "a", LastBackup: &litefake.LastBackup{At: "2026-09-03T02:00:00Z", OK: true}},
			{ID: "b", State: litefake.BackupTargetState{State: "running"}},
		}, "running"},
	} {
		fake.SetBackupTargets(tc.targets)
		got, err := c.unit.HubModel.BackupStatusRemote(ctx)
		if err != nil || got != tc.want {
			t.Errorf("%s: status = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}

	fake.SetTokens(map[string][]string{litefake.DefaultToken: {"rpc:read", "backup"}})
	_, err := c.unit.HubModel.BackupStatusRemote(ctx)
	if fe, ok := errors.AsType[*hmerr.FeatureUnavailableError](err); !ok || fe.Scope != "system:read" {
		t.Errorf("status without system:read: err = %v, want FeatureUnavailableError naming system:read", err)
	}
}
