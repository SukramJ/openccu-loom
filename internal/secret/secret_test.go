// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package secret_test

import (
	"encoding/base64"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/secret"
)

// validKey returns a base64-encoded 32-byte key for testing.
func validKey() string {
	return base64.StdEncoding.EncodeToString(make([]byte, 32))
}

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func envWithKey(key string) func(string) string {
	return func(name string) string {
		if name == secret.EnvKeyVar {
			return key
		}
		return ""
	}
}

func emptyEnv(_ string) string { return "" }

// Test 1: Load with valid env key → Available; Seal/Open round-trip.
func TestLoad_EnvKey_AvailableAndRoundTrip(t *testing.T) {
	t.Parallel()

	c, err := secret.Load("", envWithKey(validKey()), noopLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if !c.Available() {
		t.Fatal("expected Available() == true")
	}

	const plaintext = "hunter2"
	sealed, err := c.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !strings.HasPrefix(sealed, "enc:v1:") {
		t.Errorf("sealed value %q does not have enc:v1: prefix", sealed)
	}
	if sealed == plaintext {
		t.Error("sealed value must differ from plaintext")
	}

	opened, err := c.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened != plaintext {
		t.Errorf("Open returned %q, want %q", opened, plaintext)
	}
}

// Test 2: Seal("") == ""; Open("") == ""; Open("plain-no-prefix") == "plain-no-prefix".
func TestSealOpen_Passthrough(t *testing.T) {
	t.Parallel()

	c, err := secret.Load("", envWithKey(validKey()), noopLogger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	sealed, err := c.Seal("")
	if err != nil {
		t.Fatalf("Seal empty: %v", err)
	}
	if sealed != "" {
		t.Errorf("Seal(\"\") = %q, want \"\"", sealed)
	}

	opened, err := c.Open("")
	if err != nil {
		t.Fatalf("Open empty: %v", err)
	}
	if opened != "" {
		t.Errorf("Open(\"\") = %q, want \"\"", opened)
	}

	const plain = "plain-no-prefix"
	pt, err := c.Open(plain)
	if err != nil {
		t.Fatalf("Open plain: %v", err)
	}
	if pt != plain {
		t.Errorf("Open(%q) = %q, want same", plain, pt)
	}
}

// Test 3: Seal is idempotent — sealing an already-sealed value is a no-op.
func TestSeal_Idempotent(t *testing.T) {
	t.Parallel()

	c, err := secret.Load("", envWithKey(validKey()), noopLogger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	sealed, err := c.Seal("my-secret")
	if err != nil {
		t.Fatalf("first Seal: %v", err)
	}

	again, err := c.Seal(sealed)
	if err != nil {
		t.Fatalf("second Seal: %v", err)
	}
	if again != sealed {
		t.Errorf("second Seal changed the value: got %q, want %q", again, sealed)
	}
}

// Test 4: Load with malformed env key returns a non-nil error.
func TestLoad_MalformedEnvKey_Error(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		key  string
	}{
		{"not-base64", "not-base64!!"},
		{"too-short", base64.StdEncoding.EncodeToString([]byte("tooshort"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := secret.Load("", envWithKey(tc.key), noopLogger())
			if err == nil {
				t.Error("expected error for malformed key, got nil")
			}
		})
	}
}

// Test 5: Auto-keyfile creation, permissions, and key stability across two Loads.
func TestLoad_AutoKeyfile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	c1, err := secret.Load(dir, emptyEnv, noopLogger())
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if !c1.Available() {
		t.Fatal("expected Available() == true after auto-key creation")
	}

	keyPath := filepath.Join(dir, "secret.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("secret.key not created: %v", err)
	}
	// Windows does not honour Unix permission bits (os.WriteFile(0600)
	// surfaces as 0666), so the 0600 contract only holds on POSIX.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm&0o777 != 0o600 {
			t.Errorf("secret.key has permissions %04o, want 0600", perm)
		}
	}

	// Second Load must read the same key so cross-cipher round-trips work.
	c2, err := secret.Load(dir, emptyEnv, noopLogger())
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}

	const plaintext = "stable-key-test"
	sealed, err := c1.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	opened, err := c2.Open(sealed)
	if err != nil {
		t.Fatalf("Open with second cipher: %v", err)
	}
	if opened != plaintext {
		t.Errorf("cross-cipher Open returned %q, want %q", opened, plaintext)
	}
}

// Test 6: Unavailable Cipher (empty env + empty dataDir) — Load succeeds,
// Available==false, Seal passthrough, Open of enc:v1: prefix returns error.
func TestLoad_Unavailable(t *testing.T) {
	t.Parallel()

	c, err := secret.Load("", emptyEnv, noopLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if c.Available() {
		t.Fatal("expected Available() == false")
	}

	pt, err := c.Seal("x")
	if err != nil {
		t.Fatalf("Seal on unavailable cipher: %v", err)
	}
	if pt != "x" {
		t.Errorf("Seal passthrough returned %q, want \"x\"", pt)
	}

	const fakeEncrypted = "enc:v1:AAAAAAAAAAAAAAAAAAAAAA=="
	_, err = c.Open(fakeEncrypted)
	if err == nil {
		t.Error("Open of enc:v1: value on unavailable cipher must return error")
	}
}
