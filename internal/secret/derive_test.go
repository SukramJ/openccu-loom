// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package secret

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
)

// TestDeriveKeySeparatesUses: one master key and one label give one key on
// every call; another label or another master key gives another, and none
// of them is the master key itself. An unavailable Cipher derives nothing.
func TestDeriveKeySeparatesUses(t *testing.T) {
	t.Parallel()
	master := bytes.Repeat([]byte{0x42}, keySize)
	other := bytes.Repeat([]byte{0x24}, keySize)
	c, err := NewCipher(master)
	if err != nil {
		t.Fatal(err)
	}
	o, err := NewCipher(other)
	if err != nil {
		t.Fatal(err)
	}

	a1, err := c.DeriveKey("use a")
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := c.DeriveKey("use a")
	b, _ := c.DeriveKey("use b")
	oa, _ := o.DeriveKey("use a")
	switch {
	case len(a1) != keySize:
		t.Fatalf("derived key is %d bytes, want %d", len(a1), keySize)
	case !bytes.Equal(a1, a2):
		t.Fatal("the same label derived two keys")
	case bytes.Equal(a1, b):
		t.Fatal("two labels derived the same key")
	case bytes.Equal(a1, oa):
		t.Fatal("two master keys derived the same key")
	case bytes.Equal(a1, master):
		t.Fatal("the derived key is the master key")
	}

	if _, err := (&Cipher{}).DeriveKey("use a"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("unavailable cipher: err = %v, want ErrNoKey", err)
	}
}

// TestKeyFileRoundTrip: a key written with WriteKeyFile reads back with
// ReadKeyFile and with LoadExisting, which the daemon resolves through.
func TestKeyFileRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteKeyFile(filepath.Join(dir, KeyFileName), key); err != nil {
		t.Fatal(err)
	}
	got, err := ReadKeyFile(filepath.Join(dir, KeyFileName))
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("ReadKeyFile = %x, %v; want the written key", got, err)
	}
	c, err := LoadExisting(dir, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	want, _ := NewCipher(key)
	d1, _ := c.DeriveKey("probe")
	d2, _ := want.DeriveKey("probe")
	if !bytes.Equal(d1, d2) {
		t.Fatal("LoadExisting did not resolve the key WriteKeyFile wrote")
	}
}
