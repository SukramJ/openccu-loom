// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package secret_test

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/secret"
)

type subConfig struct {
	Token  string `cfg:"secret"`
	Public string
}

type sampleConfig struct {
	Password string            `cfg:"secret"`
	Users    map[string]string `cfg:"secret"`
	Pin      uint32            `cfg:"secret"`
	Name     string
	Nested   subConfig
}

func availableCipher(t *testing.T) *secret.Cipher {
	t.Helper()
	c, err := secret.Load("", envWithKey(validKey()), noopLogger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.Available() {
		t.Fatal("cipher must be available")
	}
	return c
}

// SealStruct encrypts tagged string fields and map values; leaves non-secret
// and non-string-typed secret fields unchanged.
func TestSealStruct_EncryptsTaggedFields(t *testing.T) {
	t.Parallel()

	c := availableCipher(t)

	s := sampleConfig{
		Password: "s3cret",
		Users:    map[string]string{"alice": "pw1", "bob": "pw2"},
		Pin:      1234,
		Name:     "public-name",
		Nested:   subConfig{Token: "tok", Public: "visible"},
	}

	if err := c.SealStruct(&s); err != nil {
		t.Fatalf("SealStruct: %v", err)
	}

	// Secret string fields must carry the enc:v1: prefix.
	if !strings.HasPrefix(s.Password, "enc:v1:") {
		t.Errorf("Password not sealed: %q", s.Password)
	}
	for k, v := range s.Users {
		if !strings.HasPrefix(v, "enc:v1:") {
			t.Errorf("Users[%q] not sealed: %q", k, v)
		}
	}
	if !strings.HasPrefix(s.Nested.Token, "enc:v1:") {
		t.Errorf("Nested.Token not sealed: %q", s.Nested.Token)
	}

	// Non-secret fields must be untouched.
	if s.Name != "public-name" {
		t.Errorf("Name changed: %q", s.Name)
	}
	if s.Nested.Public != "visible" {
		t.Errorf("Nested.Public changed: %q", s.Nested.Public)
	}

	// Non-string secret field must be untouched.
	if s.Pin != 1234 {
		t.Errorf("Pin changed: %d", s.Pin)
	}
}

// OpenStruct decrypts what SealStruct sealed and restores original values.
func TestOpenStruct_RoundTrip(t *testing.T) {
	t.Parallel()

	c := availableCipher(t)

	original := sampleConfig{
		Password: "s3cret",
		Users:    map[string]string{"alice": "pw1", "bob": "pw2"},
		Pin:      1234,
		Name:     "public-name",
		Nested:   subConfig{Token: "tok", Public: "visible"},
	}
	s := original

	if err := c.SealStruct(&s); err != nil {
		t.Fatalf("SealStruct: %v", err)
	}
	if err := c.OpenStruct(&s); err != nil {
		t.Fatalf("OpenStruct: %v", err)
	}

	if s.Password != original.Password {
		t.Errorf("Password: got %q, want %q", s.Password, original.Password)
	}
	for k, want := range original.Users {
		if s.Users[k] != want {
			t.Errorf("Users[%q]: got %q, want %q", k, s.Users[k], want)
		}
	}
	if s.Pin != original.Pin {
		t.Errorf("Pin: got %d, want %d", s.Pin, original.Pin)
	}
	if s.Name != original.Name {
		t.Errorf("Name: got %q, want %q", s.Name, original.Name)
	}
	if s.Nested.Token != original.Nested.Token {
		t.Errorf("Nested.Token: got %q, want %q", s.Nested.Token, original.Nested.Token)
	}
	if s.Nested.Public != original.Nested.Public {
		t.Errorf("Nested.Public: got %q, want %q", s.Nested.Public, original.Nested.Public)
	}
}

// SealStruct on nil or non-pointer must return an error.
func TestSealStruct_InvalidInput(t *testing.T) {
	t.Parallel()

	c := availableCipher(t)

	if err := c.SealStruct(nil); err == nil {
		t.Error("SealStruct(nil) must return error")
	}

	s := sampleConfig{Password: "x"}
	if err := c.SealStruct(s); err == nil {
		t.Error("SealStruct(non-pointer) must return error")
	}
}
