// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"context"
	gosql "database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	matterstore "thingfabric.dev/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/secret"
)

// sealedSettingKey is a matter_settings key go-fabric holds as a secret
// (go-fabric store/seal.go, sealedSettings): what a sealed store seals and
// a clear one keeps in clear.
const sealedSettingKey = "commissioning.rotating_device_id.unique_id"

// testCipher is a master-key cipher over a key made of b.
func testCipher(t *testing.T, b byte) *secret.Cipher {
	t.Helper()
	c, err := secret.NewCipher(bytes.Repeat([]byte{b}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// testSealer is the Matter store's sealer under c.
func testSealer(t *testing.T, c *secret.Cipher) matterstore.Sealer {
	t.Helper()
	s, err := matterSealer(c)
	if err != nil || s == nil {
		t.Fatalf("matter sealer: %v, %v", s, err)
	}
	return s
}

// settingSealedFlag reads the seal mark go-fabric keeps beside a setting.
func settingSealedFlag(t *testing.T, db *gosql.DB, key string) (value string, sealed bool) {
	t.Helper()
	if err := db.QueryRow(`SELECT value, sealed FROM matter_settings WHERE key = ?`, key).Scan(&value, &sealed); err != nil {
		t.Fatalf("read setting %s: %v", key, err)
	}
	return value, sealed
}

// sealTestConfig is a Matter configuration startMatterBridge runs on.
func sealTestConfig(t *testing.T, central string) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: central, Host: "127.0.0.1"}}
	return cfg
}

// startSealTestBridge runs startMatterBridge on db under cipher and stops
// the bridge at the end of the test.
func startSealTestBridge(t *testing.T, db *gosql.DB, cipher *secret.Cipher, tracker *health.Tracker) *matterBridgeBundle {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bundle := startMatterBridge(ctx, sealTestConfig(t, "ccu-seal"), buildTestRegistry(t, "ccu-seal"), db, cipher, tracker, nil, slog.New(slog.DiscardHandler), nil)
	if bundle != nil {
		t.Cleanup(bundle.stop)
	}
	return bundle
}

// TestMatterStoreSealedFollowsTheCanary pins the probe on go-fabric's
// reserved canary row by behaviour: a fresh database reads unsealed, one
// NewSealed opened reads sealed, and one Erase dropped the canary of reads
// unsealed again. A rename of the row upstream turns the second check red.
func TestMatterStoreSealedFollowsTheCanary(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)
	ctx := context.Background()
	if sealed, err := matterStoreSealed(ctx, db); err != nil || sealed {
		t.Fatalf("fresh database: sealed=%v err=%v, want unsealed", sealed, err)
	}
	st, err := matterstore.NewSealed(ctx, db, testSealer(t, testCipher(t, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if sealed, err := matterStoreSealed(ctx, db); err != nil || !sealed {
		t.Fatalf("after NewSealed: sealed=%v err=%v, want sealed", sealed, err)
	}
	if err := st.Erase(ctx); err != nil && !errors.Is(err, matterstore.ErrScrubIncomplete) {
		t.Fatal(err)
	}
	if sealed, err := matterStoreSealed(ctx, db); err != nil || sealed {
		t.Fatalf("after Erase: sealed=%v err=%v, want unsealed", sealed, err)
	}
}

// TestStartMatterBridgeSealsTheStoreWhenAMasterKeyResolves drives the
// activation through startMatterBridge, one case per value: with a master
// key the start seals the secret a clear database already holds, without
// one the database stays as it was.
func TestStartMatterBridgeSealsTheStoreWhenAMasterKeyResolves(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		cipher     *secret.Cipher
		wantSealed bool
	}{
		{name: "master key", cipher: testCipher(t, 7), wantSealed: true},
		{name: "no master key", cipher: &secret.Cipher{}, wantSealed: false},
		{name: "no cipher", cipher: nil, wantSealed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestLoomDB(t)
			ctx := context.Background()
			if err := matterstore.New(db).SetSetting(ctx, sealedSettingKey, "clear-secret"); err != nil {
				t.Fatal(err)
			}
			if bundle := startSealTestBridge(t, db, tc.cipher, health.NewTracker()); bundle == nil {
				t.Fatal("startMatterBridge returned nil")
			}
			value, sealed := settingSealedFlag(t, db, sealedSettingKey)
			if sealed != tc.wantSealed || (value == "clear-secret") == tc.wantSealed {
				t.Fatalf("secret setting sealed=%v value-in-clear=%v, want sealed=%v", sealed, value == "clear-secret", tc.wantSealed)
			}
			if got, _ := matterStoreSealed(ctx, db); got != tc.wantSealed {
				t.Fatalf("seal canary present = %v, want %v", got, tc.wantSealed)
			}
		})
	}
}

// TestStartMatterBridgeStaysOffOnASealedStoreItCannotOpen: a database sealed
// under one master key starts the bridge again under that key; under no key
// and under another one the bridge stays off and the health component
// names why. The first case is the control: the refusals are not the
// sealing failing.
func TestStartMatterBridgeStaysOffOnASealedStoreItCannotOpen(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		cipher   *secret.Cipher
		wantUp   bool
		wantNote string
	}{
		{name: "the sealing key", cipher: testCipher(t, 3), wantUp: true},
		{name: "no master key", cipher: &secret.Cipher{}, wantNote: "no master key is available"},
		{name: "another master key", cipher: testCipher(t, 4), wantNote: "does not open"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestLoomDB(t)
			first := startSealTestBridge(t, db, testCipher(t, 3), health.NewTracker())
			if first == nil {
				t.Fatal("the sealing start returned nil")
			}
			first.stop()

			tracker := health.NewTracker()
			got := startSealTestBridge(t, db, tc.cipher, tracker)
			if (got != nil) != tc.wantUp {
				t.Fatalf("bridge up = %v, want %v", got != nil, tc.wantUp)
			}
			if tc.wantUp {
				return
			}
			c, ok := tracker.Get(matterHealthComponentName)
			if !ok || c.LastSample.Healthy || !strings.Contains(c.LastSample.Note, tc.wantNote) {
				t.Fatalf("health %s = %+v (present %v), want unhealthy naming %q", matterHealthComponentName, c.LastSample, ok, tc.wantNote)
			}
		})
	}
}

// TestASealedStoreKeepsTheBridgeIdentityAcrossARestart: the sealing changes
// nothing a controller sees — a restart on the sealed store serves the
// UniqueID the first start pinned. Runs in a fresh process: the pinned
// UniqueID is not served once a dev-rotation start elsewhere in the
// package has switched the process to rotation.
func TestASealedStoreKeepsTheBridgeIdentityAcrossARestart(t *testing.T) {
	if !inFreshProcess(t) {
		return
	}
	db := openTestLoomDB(t)
	key := testCipher(t, 9)
	first := startSealTestBridge(t, db, key, health.NewTracker())
	if first == nil {
		t.Fatal("first start returned nil")
	}
	id := servedUniqueID(t, first)
	first.stop()
	second := startSealTestBridge(t, db, key, health.NewTracker())
	if second == nil {
		t.Fatal("restart on the sealed store returned nil")
	}
	if got := servedUniqueID(t, second); got != id {
		t.Fatalf("UniqueID after a restart on the sealed store = %q, want %q", got, id)
	}
}

// TestAnErasedBridgeComesBackSealed: the erase drops go-fabric's seal
// canary with every other row, and the node it brings up gets its store
// opened sealed anew, so the database is marked again at once — not only at
// the next start.
func TestAnErasedBridgeComesBackSealed(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	mw, closers, teardown := wireMatterRuntime(
		ctx, sealTestConfig(t, "ccu-seal-erase"), buildTestRegistry(t, "ccu-seal-erase"), db, testCipher(t, 5), health.NewTracker(), nil, nil, nil,
		slog.New(slog.DiscardHandler), nil, nil,
	)
	t.Cleanup(func() {
		cancel()
		for _, c := range closers {
			c()
		}
		if teardown != nil {
			teardown()
		}
	})
	if _, ok := pagesNode(mw.nodeUI); !ok {
		t.Fatal("the Matter runtime did not start; this test asserts nothing without it")
	}
	if sealed, _ := matterStoreSealed(context.Background(), db); !sealed {
		t.Fatal("the start under a master key left the store unsealed")
	}
	eraseNode(t, newNodeAPI(t, mw.nodeUI))
	if sealed, err := matterStoreSealed(context.Background(), db); err != nil || !sealed {
		t.Fatalf("after the erase: sealed=%v err=%v — the new node runs on a store the erase unmarked", sealed, err)
	}
}

// TestASealingWhoseScrubIsOwedWarnsOnThePages: when a read transaction of
// the daemon keeps go-fabric from scrubbing the clear bytes the sealing
// replaced, the start still runs sealed, and the node pages say the scrub
// is owed. Without a reader the scrub finishes and the pages say nothing —
// the negative control.
func TestASealingWhoseScrubIsOwedWarnsOnThePages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		holdReader bool
	}{
		{name: "scrub finishes", holdReader: false},
		{name: "a read transaction blocks the scrub", holdReader: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestLoomDB(t)
			ctx := context.Background()
			if err := matterstore.New(db).SetSetting(ctx, sealedSettingKey, "clear-secret"); err != nil {
				t.Fatal(err)
			}
			if tc.holdReader {
				conn, err := db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				tx, err := conn.BeginTx(ctx, &gosql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				var one int
				if err := tx.QueryRowContext(ctx, `SELECT 1 FROM boot_record`).Scan(&one); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = tx.Rollback(); _ = conn.Close() })
			}
			runCtx, cancel := context.WithCancel(ctx)
			mw, closers, teardown := wireMatterRuntime(
				runCtx, sealTestConfig(t, "ccu-seal-owed"), buildTestRegistry(t, "ccu-seal-owed"), db, testCipher(t, 8), health.NewTracker(), nil, nil, nil,
				slog.New(slog.DiscardHandler), nil, nil,
			)
			t.Cleanup(func() {
				cancel()
				for _, c := range closers {
					c()
				}
				if teardown != nil {
					teardown()
				}
			})
			if _, ok := pagesNode(mw.nodeUI); !ok {
				t.Fatal("the Matter runtime did not start")
			}
			warned := false
			for _, w := range mw.nodeUI.Warnings(ctx) {
				warned = warned || w.Code == "seal_scrub_owed"
			}
			if warned != tc.holdReader {
				t.Fatalf("seal_scrub_owed on the pages = %v, want %v", warned, tc.holdReader)
			}
		})
	}
}
