// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	mattercore "github.com/SukramJ/go-fabric/cluster/core"
	matterstore "github.com/SukramJ/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestResolveBridgeUniqueIDStableAcrossRename pins Matter §11.1.5.13 quality F:
// BasicInformation.UniqueID must not change once the bridge is commissioned. A
// bridge rename (a node_label change, which also moves the derived serial) must
// leave the persisted-and-pinned UniqueID untouched, or every bridged accessory
// looks new to Apple Home / Google Home and has to be re-paired.
func TestResolveBridgeUniqueIDStableAcrossRename(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := matterstore.New(openTestLoomDB(t))
	logger := slog.New(slog.DiscardHandler)

	mc := config.NorthMatter{VendorID: 0xFFF1, ProductID: 0x8000, NodeLabel: "openccu-loom"}
	const rootSerial1 = "aaaabbbbccccdddd"

	// First boot: nothing persisted, so seed from the current derivation and
	// store it. The seed must equal what an un-pinned BasicInformation derives,
	// so upgrading an already-commissioned bridge keeps its identity.
	first := resolveBridgeUniqueID(ctx, store, mc, rootSerial1, logger)
	if first == "" {
		t.Fatal("resolveBridgeUniqueID returned empty on first boot")
	}
	if want := mattercore.DeriveUniqueID(mc.VendorID, mc.ProductID, mc.NodeLabel, rootSerial1); first != want {
		t.Fatalf("first-boot seed = %q, want the un-pinned derivation %q", first, want)
	}

	// The bridge is renamed: node_label changes and the derived serial with it.
	renamed := mc
	renamed.NodeLabel = "Living Room"
	const rootSerial2 = "1111222233334444"

	// Guard against an inert test: the renamed inputs must derive a DIFFERENT
	// value, so stability can only come from the persisted pin.
	if moved := mattercore.DeriveUniqueID(renamed.VendorID, renamed.ProductID, renamed.NodeLabel, rootSerial2); moved == first {
		t.Fatal("test is inert: the renamed derivation coincidentally equals the original")
	}

	second := resolveBridgeUniqueID(ctx, store, renamed, rootSerial2, logger)
	if second != first {
		t.Errorf("UniqueID changed across rename: %q -> %q (Matter §11.1.5.13 quality F violated)", first, second)
	}
}

// TestResolveBridgeUniqueIDReadErrorDoesNotOverwritePinnedValue is the
// regression guard for a store read error being treated as "not persisted":
// on a transient read failure (SQLite busy, a cancelled context) the fixed
// function returns an in-memory derived value for that boot WITHOUT
// attempting to persist it, so a genuinely pinned row is never overwritten
// by a spurious re-derive. The unfixed code fell through the same branch
// on any non-nil error (`err == nil && ok && v != ""` is false whenever
// err != nil) and then unconditionally called SetSetting — logging a
// distinct "persist_unique_id" record this test asserts never fires.
//
// The database is closed to force the read to fail, which means a
// subsequent SetSetting attempt would fail too, so the persisted row
// itself cannot be used to distinguish old from new behaviour here — the
// log record can: only the fixed code skips the write attempt entirely.
func TestResolveBridgeUniqueIDReadErrorDoesNotOverwritePinnedValue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestLoomDB(t)
	store := matterstore.New(db)

	mc := config.NorthMatter{VendorID: 0xFFF1, ProductID: 0x8000, NodeLabel: "openccu-loom"}
	const rootSerial = "aaaabbbbccccdddd"

	pinned := resolveBridgeUniqueID(ctx, store, mc, rootSerial, slog.New(slog.DiscardHandler))
	if pinned == "" {
		t.Fatal("resolveBridgeUniqueID returned empty on first boot")
	}

	// Force the next GetSetting to fail.
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	var logBuf bytes.Buffer
	capturingLogger := slog.New(slog.NewTextHandler(&logBuf, nil))
	got := resolveBridgeUniqueID(ctx, store, mc, rootSerial, capturingLogger)
	if want := mattercore.DeriveUniqueID(mc.VendorID, mc.ProductID, mc.NodeLabel, rootSerial); got != want {
		t.Errorf("read-error return value = %q, want the in-memory derivation %q", got, want)
	}
	logged := logBuf.String()
	if !strings.Contains(logged, "matter.bridge.basicinfo.read_unique_id") {
		t.Errorf("expected a read_unique_id warning, got log: %s", logged)
	}
	if strings.Contains(logged, "matter.bridge.basicinfo.persist_unique_id") {
		t.Errorf("a persist_unique_id record fired on a read error: the fixed code must not attempt to overwrite a value it could not read, got log: %s", logged)
	}
}

// TestResolveBridgeUniqueIDDevRotateReDerivesEveryBoot is the regression
// guard for north.matter.dev_rotate_unique_ids no longer rotating the root
// UniqueID after the first boot that persisted a value: with the flag on,
// every call must re-derive (a fresh bootid.Salt() per boot), never
// short-circuit on a previously-stored value.
func TestResolveBridgeUniqueIDDevRotateReDerivesEveryBoot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := matterstore.New(openTestLoomDB(t))
	logger := slog.New(slog.DiscardHandler)

	mc := config.NorthMatter{VendorID: 0xFFF1, ProductID: 0x8000, NodeLabel: "openccu-loom", DevRotateUniqueIDs: true}
	const rootSerial = "aaaabbbbccccdddd"

	first := resolveBridgeUniqueID(ctx, store, mc, rootSerial, logger)
	if first == "" {
		t.Fatal("resolveBridgeUniqueID returned empty on first boot")
	}
	// The rotated value is persisted AND tagged: bootid.Salt() is a
	// process-lifetime value (tested independently at the bootid package
	// level), so a same-process second call cannot observe a different
	// return value — the fix under test is that the store gets re-written
	// (and marked rotated) on every call instead of the pinned path's
	// short-circuit, which is what actually lets a later real boot's fresh
	// process-lifetime salt take effect.
	if v, ok, err := store.GetSetting(ctx, matterstore.SettingUniqueID); err != nil || !ok || v != first {
		t.Fatalf("rotated value was not persisted: v=%q ok=%v err=%v, want %q", v, ok, err, first)
	}
	if rotated, ok, err := store.GetSetting(ctx, matterstore.SettingUniqueIDRotated); err != nil || !ok || rotated != "1" {
		t.Fatalf("rotated marker not set after a dev_rotate_unique_ids boot: rotated=%q ok=%v err=%v", rotated, ok, err)
	}

	// A second call with the SAME flag must re-persist rather than
	// short-circuit on the stored value — the pinned path's `ok && v != ""`
	// early return must never fire while DevRotateUniqueIDs is on. Flip the
	// stored value to a sentinel the pinned path would otherwise return
	// unchanged; a fixed resolveBridgeUniqueID overwrites it again.
	if err := store.SetSetting(ctx, matterstore.SettingUniqueID, "stale-sentinel"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	second := resolveBridgeUniqueID(ctx, store, mc, rootSerial, logger)
	if second == "stale-sentinel" {
		t.Error("resolveBridgeUniqueID returned the sentinel: it short-circuited on the stored value instead of re-deriving")
	}
	if second != first {
		t.Errorf("second rotated call = %q, want the re-derived value %q (same process salt)", second, first)
	}
}

// TestResolveBridgeUniqueIDDevRotateDisabledClearsStaleRotatedValue is the
// second half of the dev-rotate regression: a value salted while the flag
// was on must not stay pinned forever once the flag is turned back off —
// the next boot must recognise the rotated marker and re-derive the
// deterministic (un-salted) identity instead.
//
// The "boot with rotation enabled" is simulated by writing the rotated
// state directly (rather than via a genuinely salted resolveBridgeUniqueID
// call): bootid.Salt() is a real process-lifetime crypto/rand value shared
// by the whole test binary, with no way to reset it once enabled, so
// relying on it to differ from the deterministic derivation would leak
// rotation state into every other test in this package. Seeding the store
// directly isolates the test to exactly the mechanism under test — the
// rotated-marker staleness check — without depending on bootid at all.
func TestResolveBridgeUniqueIDDevRotateDisabledClearsStaleRotatedValue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := matterstore.New(openTestLoomDB(t))
	logger := slog.New(slog.DiscardHandler)

	mc := config.NorthMatter{VendorID: 0xFFF1, ProductID: 0x8000, NodeLabel: "openccu-loom"}
	const rootSerial = "aaaabbbbccccdddd"
	const staleRotatedValue = "stale-rotated-salted-value"

	if err := store.SetSetting(ctx, matterstore.SettingUniqueID, staleRotatedValue); err != nil {
		t.Fatalf("seed SettingUniqueID: %v", err)
	}
	if err := store.SetSetting(ctx, matterstore.SettingUniqueIDRotated, "1"); err != nil {
		t.Fatalf("seed SettingUniqueIDRotated: %v", err)
	}

	// Rotation is off for this boot. It must not pin the leftover salted
	// value — it must recognise the rotated marker and re-derive the
	// deterministic identity a normal (never-rotated) boot would.
	got := resolveBridgeUniqueID(ctx, store, mc, rootSerial, logger)
	want := mattercore.DeriveUniqueID(mc.VendorID, mc.ProductID, mc.NodeLabel, rootSerial)
	if got != want {
		t.Errorf("post-rotation value = %q, want the deterministic derivation %q", got, want)
	}
	if got == staleRotatedValue {
		t.Error("returned the stale rotated sentinel: the rotated marker was not honoured")
	}
	if rotated, ok, err := store.GetSetting(ctx, matterstore.SettingUniqueIDRotated); err != nil || (ok && rotated == "1") {
		t.Errorf("rotated marker still set after the deterministic value was re-persisted: rotated=%q ok=%v err=%v", rotated, ok, err)
	}

	// A SECOND boot, still with rotation off, must now pin the deterministic
	// value — the marker was cleared, so this is the ordinary pinned path.
	second := resolveBridgeUniqueID(ctx, store, mc, rootSerial, logger)
	if second != got {
		t.Errorf("deterministic value did not stay pinned on the next boot: %q -> %q", got, second)
	}
}

// TestResolveBridgeUniqueIDNilStore covers the no-persistence path: without a
// store the value is derived but not pinned, matching the un-pinned behaviour.
func TestResolveBridgeUniqueIDNilStore(t *testing.T) {
	t.Parallel()
	mc := config.NorthMatter{VendorID: 0xFFF1, ProductID: 0x8000, NodeLabel: "x"}
	got := resolveBridgeUniqueID(context.Background(), nil, mc, "serial", slog.New(slog.DiscardHandler))
	if want := mattercore.DeriveUniqueID(mc.VendorID, mc.ProductID, mc.NodeLabel, "serial"); got != want {
		t.Fatalf("nil-store UniqueID = %q, want the derived value %q", got, want)
	}
}
