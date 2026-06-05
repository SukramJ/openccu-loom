// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configstore

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/secret"
)

// loadTestCipher returns an available Cipher backed by an auto-generated
// key file in t's temp directory.
func loadTestCipher(t *testing.T) *secret.Cipher {
	t.Helper()
	c, err := secret.Load(t.TempDir(), func(string) string { return "" }, nil)
	if err != nil {
		t.Fatalf("secret.Load: %v", err)
	}
	if !c.Available() {
		t.Fatal("cipher not available — auto key-file creation failed")
	}
	return c
}

// TestTransformSectionJSONMQTTRoundTrip seals secret fields in a north.mqtt
// payload and verifies that the sealed form hides the plaintext while the
// unsealed form recovers it exactly.
func TestTransformSectionJSONMQTTRoundTrip(t *testing.T) {
	t.Parallel()
	c := loadTestCipher(t)

	raw := []byte(`{"broker_url":"tcp://x:1883","password":"hunter2","topic_base":"t"}`)

	sealed, err := TransformSectionJSON(c, SectionMQTT, raw, true)
	if err != nil {
		t.Fatalf("TransformSectionJSON seal: %v", err)
	}

	// Sealed payload must not expose the plaintext password.
	if strings.Contains(string(sealed), "hunter2") {
		t.Errorf("sealed payload contains plaintext password: %s", sealed)
	}
	// Sealed payload must carry the enc:v1: marker somewhere.
	if !strings.Contains(string(sealed), "enc:v1:") {
		t.Errorf("sealed payload does not contain enc:v1: marker: %s", sealed)
	}
	// Non-secret field must survive the seal round-trip.
	if !strings.Contains(string(sealed), "tcp://x:1883") {
		t.Errorf("sealed payload lost broker_url: %s", sealed)
	}

	opened, err := TransformSectionJSON(c, SectionMQTT, sealed, false)
	if err != nil {
		t.Fatalf("TransformSectionJSON open: %v", err)
	}

	var got struct {
		BrokerURL string `json:"broker_url"`
		Password  string `json:"password"`
		TopicBase string `json:"topic_base"`
	}
	if err := json.Unmarshal(opened, &got); err != nil {
		t.Fatalf("unmarshal opened: %v", err)
	}
	if got.Password != "hunter2" {
		t.Errorf("password after open: %q want hunter2", got.Password)
	}
	if got.BrokerURL != "tcp://x:1883" {
		t.Errorf("broker_url after open: %q want tcp://x:1883", got.BrokerURL)
	}
	if got.TopicBase != "t" {
		t.Errorf("topic_base after open: %q want t", got.TopicBase)
	}
}

// TestTransformSectionJSONNoTargetPassthrough verifies that sections with no
// struct target (SectionLocale, SectionSecurity) are returned byte-for-byte
// unchanged on both seal and open.
func TestTransformSectionJSONNoTargetPassthrough(t *testing.T) {
	t.Parallel()
	c := loadTestCipher(t)

	for _, sec := range []Section{SectionLocale, SectionSecurity} {
		t.Run(string(sec), func(t *testing.T) {
			t.Parallel()
			raw := []byte(`{"locale":"de"}`)

			sealed, err := TransformSectionJSON(c, sec, raw, true)
			if err != nil {
				t.Fatalf("seal: %v", err)
			}
			if !bytes.Equal(sealed, raw) {
				t.Errorf("seal changed payload for %s: got %s want %s", sec, sealed, raw)
			}

			opened, err := TransformSectionJSON(c, sec, raw, false)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if !bytes.Equal(opened, raw) {
				t.Errorf("open changed payload for %s: got %s want %s", sec, opened, raw)
			}
		})
	}
}

// TestTransformSectionJSONUnavailableCipherPassthrough verifies that an
// unavailable Cipher (zero-value &secret.Cipher{}) leaves values in plaintext
// on seal — resilient fallback per ADR 0027.
func TestTransformSectionJSONUnavailableCipherPassthrough(t *testing.T) {
	t.Parallel()
	unavailable := &secret.Cipher{}
	if unavailable.Available() {
		t.Fatal("expected unavailable cipher, got available")
	}

	raw := []byte(`{"broker_url":"tcp://x:1883","password":"hunter2","topic_base":"t"}`)

	sealed, err := TransformSectionJSON(unavailable, SectionMQTT, raw, true)
	if err != nil {
		t.Fatalf("seal with unavailable cipher: %v", err)
	}
	// With no key the password must survive unchanged (plaintext passthrough).
	if strings.Contains(string(sealed), "enc:v1:") {
		t.Errorf("unavailable cipher produced encrypted output: %s", sealed)
	}
	if !strings.Contains(string(sealed), "hunter2") {
		t.Errorf("unavailable cipher clobbered plaintext password: %s", sealed)
	}
}
