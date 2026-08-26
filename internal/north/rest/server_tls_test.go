// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// selfSignedPEM generates a self-signed ECDSA certificate and returns the
// PEM-encoded certificate and private key. Both blobs are ready for use
// with tls.X509KeyPair.
func selfSignedPEM(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// writePair writes cert and key PEM blobs to dir and returns their paths.
func writePair(t *testing.T, dir string, certPEM, keyPEM []byte) (certPath, keyPath string) {
	t.Helper()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}

func TestNewCertReloader_ValidPaths_LoadsWithoutError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPEM, keyPEM := selfSignedPEM(t)
	certPath, keyPath := writePair(t, dir, certPEM, keyPEM)

	r, err := NewCertReloader(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate error: %v", err)
	}
	if c == nil {
		t.Fatal("GetCertificate returned nil certificate")
	}
}

func TestNewCertReloader_MissingFile_ReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := NewCertReloader(
		filepath.Join(dir, "no-cert.pem"),
		filepath.Join(dir, "no-key.pem"),
		nil,
	)
	if err == nil {
		t.Fatal("expected error for missing files, got nil")
	}
}

func TestSaveAndReload_NewPair_SwapsCertificate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cert1PEM, key1PEM := selfSignedPEM(t)
	certPath, keyPath := writePair(t, dir, cert1PEM, key1PEM)

	r, err := NewCertReloader(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	before, _ := r.GetCertificate(nil)

	cert2PEM, key2PEM := selfSignedPEM(t)
	if err := r.SaveAndReload(cert2PEM, key2PEM); err != nil {
		t.Fatalf("SaveAndReload error: %v", err)
	}

	after, _ := r.GetCertificate(nil)
	if after == nil {
		t.Fatal("GetCertificate returned nil after SaveAndReload")
	}

	// The certificate bytes on disk must have been replaced.
	onDisk, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert from disk: %v", err)
	}
	if !bytes.Equal(onDisk, cert2PEM) {
		t.Error("cert file on disk was not updated")
	}

	// The in-memory certificate must be different from the initial one.
	if bytes.Equal(before.Certificate[0], after.Certificate[0]) {
		t.Error("active certificate was not swapped after SaveAndReload")
	}
}

func TestSaveAndReload_InvalidPEM_RejectsAndPreservesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPEM, keyPEM := selfSignedPEM(t)
	certPath, keyPath := writePair(t, dir, certPEM, keyPEM)

	r, err := NewCertReloader(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	before, _ := r.GetCertificate(nil)

	if err := r.SaveAndReload([]byte("not a pem"), []byte("x")); err == nil {
		t.Fatal("expected error for invalid PEM, got nil")
	}

	// The original file must not have been touched.
	onDisk, _ := os.ReadFile(certPath)
	if !bytes.Equal(onDisk, certPEM) {
		t.Error("cert file was modified despite invalid upload")
	}

	// The active certificate must still be the original one.
	after, _ := r.GetCertificate(nil)
	if after == nil {
		t.Fatal("GetCertificate returned nil after failed SaveAndReload")
	}
	if !bytes.Equal(before.Certificate[0], after.Certificate[0]) {
		t.Error("active certificate changed after failed SaveAndReload")
	}
}

func TestTLSConfig_HasGetCertificateAndMinTLS12(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPEM, keyPEM := selfSignedPEM(t)
	certPath, keyPath := writePair(t, dir, certPEM, keyPEM)

	r, err := NewCertReloader(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := r.TLSConfig()
	if cfg.GetCertificate == nil {
		t.Error("TLSConfig.GetCertificate is nil")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion=%d, want %d (TLS 1.2)", cfg.MinVersion, tls.VersionTLS12)
	}
}
