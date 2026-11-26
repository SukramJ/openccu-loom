// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"context"
	gosql "database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	matterstore "thingfabric.dev/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/datalock"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/secret"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// sealedFixture seals one value of each store under c: a central's
// password, the MQTT section's password and a Matter secret setting in a
// store opened sealed.
func sealedFixture(t *testing.T, db *gosql.DB, c *secret.Cipher) {
	t.Helper()
	ctx := context.Background()
	centrals := sqlite.NewCentralsStore(db)
	centrals.SetCipher(c)
	if err := centrals.Put(ctx, sqlite.CentralRow{Name: "ccu1", Host: "10.0.0.1", PasswordPlain: "ccu-pw", Enabled: true}); err != nil {
		t.Fatalf("put central: %v", err)
	}
	sections := sqlite.NewConfigSectionStore(db)
	sections.SetSecretTransform(func(section string, value []byte, seal bool) ([]byte, error) {
		return configstore.TransformSectionJSON(c, configstore.Section(section), value, seal)
	})
	if _, err := sections.Put(ctx, string(configstore.SectionMQTT), []byte(`{"password":"mqtt-pw"}`), "test"); err != nil {
		t.Fatalf("put section: %v", err)
	}
	st, _, err := openMatterStore(ctx, db, testSealer(t, c))
	if err != nil {
		t.Fatalf("seal the Matter store: %v", err)
	}
	if err := st.SetSetting(ctx, sealedSettingKey, "matter-secret"); err != nil {
		t.Fatal(err)
	}
}

// assertFixtureOpensUnder checks every fixture value reads back under c,
// and that old (when non-nil) opens none of them any more.
func assertFixtureOpensUnder(t *testing.T, db *gosql.DB, c, old *secret.Cipher) {
	t.Helper()
	ctx := context.Background()
	centrals := sqlite.NewCentralsStore(db)
	centrals.SetCipher(c)
	if row, err := centrals.Get(ctx, "ccu1"); err != nil || row.PasswordPlain != "ccu-pw" {
		t.Fatalf("central password under the new key = %q, %v", row.PasswordPlain, err)
	}
	sections := sqlite.NewConfigSectionStore(db)
	sections.SetSecretTransform(func(section string, value []byte, seal bool) ([]byte, error) {
		return configstore.TransformSectionJSON(c, configstore.Section(section), value, seal)
	})
	if row, err := sections.Get(ctx, string(configstore.SectionMQTT)); err != nil || !strings.Contains(string(row.ValueJSON), `"mqtt-pw"`) {
		t.Fatalf("MQTT section under the new key = %s, %v", row.ValueJSON, err)
	}
	st, _, err := openMatterStore(ctx, db, testSealer(t, c))
	if err != nil {
		t.Fatalf("Matter store under the new key: %v", err)
	}
	if v, ok, err := st.GetSetting(ctx, sealedSettingKey); err != nil || !ok || v != "matter-secret" {
		t.Fatalf("Matter secret under the new key = %q (%v), %v", v, ok, err)
	}
	if old == nil {
		return
	}
	oldCentrals := sqlite.NewCentralsStore(db)
	oldCentrals.SetCipher(old)
	if _, err := oldCentrals.Get(ctx, "ccu1"); err == nil {
		t.Fatal("the old key still opens the central password")
	}
	if _, _, err := openMatterStore(ctx, db, testSealer(t, old)); !errors.Is(err, errMatterSealKeyMismatch) {
		t.Fatalf("Matter store under the old key: err = %v, want errMatterSealKeyMismatch", err)
	}
}

// envOf is a getenv that knows one variable.
func envOf(name, value string) func(string) string {
	return func(k string) string {
		if k == name {
			return value
		}
		return ""
	}
}

// TestSecretRotateMovesEverySealedValueToTheNewKey: with a key file the
// rotation re-seals the configuration secrets and the Matter store under a
// fresh key, installs it as secret.key and leaves no pending file; the old
// key opens nothing afterwards.
func TestSecretRotateMovesEverySealedValueToTheNewKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldKey := bytes.Repeat([]byte{0x11}, 32)
	if err := secret.WriteKeyFile(filepath.Join(dir, secret.KeyFileName), oldKey); err != nil {
		t.Fatal(err)
	}
	old, _ := secret.NewCipher(oldKey)
	db := openTestLoomDB(t)
	sealedFixture(t, db, old)

	if err := rotateMasterKey(context.Background(), db, dir, envOf("", ""), "", io.Discard); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	newKey, err := secret.ReadKeyFile(filepath.Join(dir, secret.KeyFileName))
	if err != nil || bytes.Equal(newKey, oldKey) {
		t.Fatalf("secret.key after the rotation: %x, %v — want a new key", newKey, err)
	}
	if _, err := os.Stat(filepath.Join(dir, secret.PendingKeyFileName)); !os.IsNotExist(err) {
		t.Fatalf("the pending key file outlived the rotation: %v", err)
	}
	next, _ := secret.NewCipher(newKey)
	assertFixtureOpensUnder(t, db, next, old)
}

// TestSecretRotateFinishesAnInterruptedRun: a run that stopped after the
// configuration secrets moved — the Matter store still under the old key,
// secret.key still the old one, the next key in secret.key.next — is
// finished by running the command again.
func TestSecretRotateFinishesAnInterruptedRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldKey := bytes.Repeat([]byte{0x21}, 32)
	nextKey := bytes.Repeat([]byte{0x22}, 32)
	if err := secret.WriteKeyFile(filepath.Join(dir, secret.KeyFileName), oldKey); err != nil {
		t.Fatal(err)
	}
	old, _ := secret.NewCipher(oldKey)
	next, _ := secret.NewCipher(nextKey)
	db := openTestLoomDB(t)
	sealedFixture(t, db, old)
	if err := secret.WriteKeyFile(filepath.Join(dir, secret.PendingKeyFileName), nextKey); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resealConfigSecrets(context.Background(), db, old, next); err != nil {
		t.Fatalf("first half: %v", err)
	}

	if err := rotateMasterKey(context.Background(), db, dir, envOf("", ""), "", io.Discard); err != nil {
		t.Fatalf("resumed rotate: %v", err)
	}
	if got, _ := secret.ReadKeyFile(filepath.Join(dir, secret.KeyFileName)); !bytes.Equal(got, nextKey) {
		t.Fatal("the resumed rotation did not install the pending key")
	}
	assertFixtureOpensUnder(t, db, next, old)
}

// TestSecretRotateWithAnEnvironmentKey: with OPENCCU_LOOM_SECRET_KEY the
// rotation moves the stores to the key it was given and writes no key file —
// the operator sets the variable.
func TestSecretRotateWithAnEnvironmentKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldKey := bytes.Repeat([]byte{0x31}, 32)
	nextKey := bytes.Repeat([]byte{0x32}, 32)
	old, _ := secret.NewCipher(oldKey)
	next, _ := secret.NewCipher(nextKey)
	db := openTestLoomDB(t)
	sealedFixture(t, db, old)

	var out bytes.Buffer
	if err := rotateMasterKey(context.Background(), db, dir, envOf(secret.EnvKeyVar, secret.EncodeKey(oldKey)), secret.EncodeKey(nextKey), &out); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	for _, name := range []string{secret.KeyFileName, secret.PendingKeyFileName} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("an environment-key rotation wrote %s", name)
		}
	}
	if !strings.Contains(out.String(), secret.EnvKeyVar) {
		t.Fatalf("the report does not tell the operator to set %s:\n%s", secret.EnvKeyVar, out.String())
	}
	assertFixtureOpensUnder(t, db, next, old)
}

// TestOfflineCommandsRefuseARunningDaemon: while the data directory's lock
// is held — the daemon holds it while it runs — both offline commands
// refuse before they touch the database.
func TestOfflineCommandsRefuseARunningDaemon(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lock, err := datalock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	ran := false
	err = withOfflineDB(dir, func(context.Context, *gosql.DB) error { ran = true; return nil })
	if !errors.Is(err, datalock.ErrHeld) || ran {
		t.Fatalf("withOfflineDB under a held lock: err=%v ran=%v, want ErrHeld before the command", err, ran)
	}
	_ = lock.Release()
	if err := withOfflineDB(dir, func(context.Context, *gosql.DB) error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("withOfflineDB after the release: err=%v ran=%v", err, ran)
	}
}

// TestMatterResetRecoversALostKey: a store sealed under a key that is gone
// keeps the bridge off; `matter reset` erases it without the key, pins a
// fresh UniqueID, and the next start without a key brings the bridge up.
func TestMatterResetRecoversALostKey(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)
	ctx := context.Background()
	lost := startSealTestBridge(t, db, testCipher(t, 6), health.NewTracker())
	if lost == nil {
		t.Fatal("the sealing start returned nil")
	}
	lost.stop()
	if startSealTestBridge(t, db, &secret.Cipher{}, health.NewTracker()) != nil {
		t.Fatal("precondition: the bridge started on a sealed store without its key")
	}
	before, _, _ := matterstore.New(db).GetSetting(ctx, matterstore.SettingUniqueID)

	if err := resetMatterStore(ctx, db); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if sealed, _ := matterStoreSealed(ctx, db); sealed {
		t.Fatal("the reset left the seal canary")
	}
	after, ok, err := matterstore.New(db).GetSetting(ctx, matterstore.SettingUniqueID)
	if err != nil || !ok || after == "" || after == before {
		t.Fatalf("UniqueID pin after the reset = %q (ok %v, err %v), want a fresh one (before %q)", after, ok, err, before)
	}
	if startSealTestBridge(t, db, &secret.Cipher{}, health.NewTracker()) == nil {
		t.Fatal("the bridge did not start after the reset")
	}
}

// TestMatterResetNeedsConfirmation: without --yes the command changes
// nothing and says what it would erase.
func TestMatterResetNeedsConfirmation(t *testing.T) {
	t.Parallel()
	err := runMatterCLI([]string{"reset"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("matter reset without --yes: err = %v, want a refusal naming --yes", err)
	}
}

// TestDaemonRefusesADataDirectoryInUse: `run` takes the data directory's
// lock before anything else, so a daemon does not start on a directory an
// offline command (or another daemon) holds. Without the lock held the same
// start would boot the daemon, which this test does not let it reach.
func TestDaemonRefusesADataDirectoryInUse(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("data_dir: "+dir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCCU_LOOM_DATA_DIR", dir)
	lock, err := datalock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	err = run([]string{"run", "--config", cfgPath}, io.Discard, io.Discard)
	if !errors.Is(err, datalock.ErrHeld) {
		t.Fatalf("run on a held data directory: err = %v, want ErrHeld", err)
	}
}
