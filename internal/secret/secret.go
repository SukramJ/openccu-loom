// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package secret provides at-rest encryption for secret-classed config
// values stored in the database. See ADR 0027.
//
// The master key is resolved hybrid: the OPENCCU_LOOM_SECRET_KEY env var
// (base64-encoded 32 bytes) takes precedence; otherwise an auto-generated
// key file lives under the data directory. When no key can be resolved or
// created, [Load] returns an unavailable [Cipher] that passes values
// through unchanged, so encryption is a hardening layer rather than a
// boot dependency (the resilient fallback in ADR 0027).
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	// EnvKeyVar is the environment variable holding a base64-encoded
	// 32-byte master key. When set it wins over the key file.
	EnvKeyVar = "OPENCCU_LOOM_SECRET_KEY"
	// KeyFileName is the auto-generated key file under the data dir.
	KeyFileName = "secret.key"
	// encPrefix tags a sealed value. Versioned so the scheme can evolve.
	encPrefix = "enc:v1:"
	keySize   = 32 // AES-256
)

// Cipher seals and opens secret strings with AES-256-GCM. A nil or
// unavailable Cipher passes values through unchanged, so callers never
// need to special-case the no-key path.
type Cipher struct {
	aead cipher.AEAD // nil when unavailable
}

// Available reports whether a usable master key was resolved.
func (c *Cipher) Available() bool { return c != nil && c.aead != nil }

// Load resolves the master key and returns a Cipher. It returns an error
// only for a present-but-malformed OPENCCU_LOOM_SECRET_KEY (an operator
// mistake worth surfacing loudly). The "no key / cannot persist a key
// file" case is not an error: it logs a warning and returns an
// unavailable Cipher that stores values in plaintext.
func Load(dataDir string, getenv func(string) string, logger *slog.Logger) (*Cipher, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if logger == nil {
		logger = slog.Default()
	}

	if raw := strings.TrimSpace(getenv(EnvKeyVar)); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(key) != keySize {
			return nil, fmt.Errorf("secret: %s must be base64-encoded %d bytes", EnvKeyVar, keySize)
		}
		return newCipher(key)
	}

	key, err := loadOrCreateKeyFile(dataDir)
	if err != nil {
		logger.Warn("secret.key_unavailable",
			slog.String("err", err.Error()),
			slog.String("effect", "config secrets stored in plaintext"))
		return &Cipher{}, nil
	}
	return newCipher(key)
}

func newCipher(key []byte) (*Cipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secret: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret: new gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// loadOrCreateKeyFile reads <dataDir>/secret.key, generating it (mode
// 0600) on first use. A malformed existing file is an error.
func loadOrCreateKeyFile(dataDir string) ([]byte, error) {
	if dataDir == "" {
		return nil, errors.New("no data_dir for key file")
	}
	path := filepath.Join(dataDir, KeyFileName)
	//nolint:gosec // path is data_dir (operator-controlled config) + a constant filename
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		key, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if derr != nil || len(key) != keySize {
			return nil, fmt.Errorf("key file %s is malformed", path)
		}
		return key, nil
	case errors.Is(err, os.ErrNotExist):
		// fall through to creation
	default:
		return nil, fmt.Errorf("read key file: %w", err)
	}

	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	enc := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(enc+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write key file: %w", err)
	}
	return key, nil
}

// Seal encrypts plaintext into the enc:v1: envelope. An empty string
// returns empty (so env-only secrets stay absent from the DB); an
// already-sealed value is returned unchanged (idempotent); an unavailable
// Cipher returns the plaintext unchanged (resilient fallback).
func (c *Cipher) Seal(plaintext string) (string, error) {
	if plaintext == "" || strings.HasPrefix(plaintext, encPrefix) || !c.Available() {
		return plaintext, nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("secret: nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Open decrypts an enc:v1: envelope. A value without the prefix is
// returned unchanged (plaintext passthrough / pre-encryption rows). A
// prefixed value with no available key is an error — the data cannot be
// recovered without the master key.
func (c *Cipher) Open(stored string) (string, error) {
	if !strings.HasPrefix(stored, encPrefix) {
		return stored, nil
	}
	if !c.Available() {
		return "", errors.New("secret: encrypted value but no master key available")
	}
	raw, err := base64.StdEncoding.DecodeString(stored[len(encPrefix):])
	if err != nil {
		return "", fmt.Errorf("secret: decode: %w", err)
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("secret: ciphertext too short")
	}
	pt, err := c.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("secret: open: %w", err)
	}
	return string(pt), nil
}
