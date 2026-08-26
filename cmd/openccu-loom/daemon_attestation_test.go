// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// writePEMCert writes an ECDSA cert (self-signed) and returns the DER bytes +
// file path.
func writePEMCert(t *testing.T, dir string, priv *ecdsa.PrivateKey, name string) (der []byte, path string) {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	path = filepath.Join(dir, name+".pem")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("pem.Encode: %v", err)
	}
	return der, path
}

// writePEMKey writes a PKCS#8 private key PEM and returns the path.
func writePEMKey(t *testing.T, dir string, priv *ecdsa.PrivateKey, name string) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalPKCS8: %v", err)
	}
	path := filepath.Join(dir, name+".pem")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		t.Fatalf("pem.Encode: %v", err)
	}
	return path
}

// ── loadVendorAttestation ─────────────────────────────────────────────────────

func TestLoadVendorAttestation_MissingPaths_ReturnsFalse(t *testing.T) {
	t.Parallel()
	// All empty → false without reading any file.
	_, _, _, _, ok := loadVendorAttestation(config.NorthMatterAttestation{}, slog.Default())
	if ok {
		t.Error("expected ok=false for empty paths")
	}
}

func TestLoadVendorAttestation_PartialPaths_ReturnsFalse(t *testing.T) {
	t.Parallel()
	cfg := config.NorthMatterAttestation{
		DACPath:    "/some/dac.pem",
		DACKeyPath: "", // missing → should return false
		PAIPath:    "/pai.pem",
		CDPath:     "/cd.bin",
	}
	_, _, _, _, ok := loadVendorAttestation(cfg, slog.Default())
	if ok {
		t.Error("expected ok=false when any path is empty")
	}
}

func TestLoadVendorAttestation_DACFileNotFound_ReturnsFalse(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	cfg := config.NorthMatterAttestation{
		DACPath:    "/nonexistent/dac.pem",
		DACKeyPath: "/nonexistent/key.pem",
		PAIPath:    "/nonexistent/pai.pem",
		CDPath:     "/nonexistent/cd.bin",
	}
	_, _, _, _, ok := loadVendorAttestation(cfg, logger)
	if ok {
		t.Error("expected ok=false when DAC file is missing")
	}
}

func TestLoadVendorAttestation_KeyMismatch_ReturnsFalse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Create a DAC cert signed by privA, but supply privB as the key.
	privA, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	privB, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	_, dacPath := writePEMCert(t, dir, privA, "dac")
	keyPath := writePEMKey(t, dir, privB, "key") // wrong key

	// PAI (same self-signed cert, doesn't matter for this test).
	_, paiPath := writePEMCert(t, dir, privA, "pai")

	// Empty CD file.
	cdPath := filepath.Join(dir, "cd.bin")
	if err := os.WriteFile(cdPath, []byte{}, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := config.NorthMatterAttestation{
		DACPath:    dacPath,
		DACKeyPath: keyPath,
		PAIPath:    paiPath,
		CDPath:     cdPath,
	}
	_, _, _, _, ok := loadVendorAttestation(cfg, logger)
	if ok {
		t.Error("expected ok=false when DAC key does not match cert public key")
	}
	if !bytes.Contains(buf.Bytes(), []byte("key_mismatch")) {
		t.Errorf("expected key_mismatch warning; got:\n%s", buf.String())
	}
}

func TestLoadVendorAttestation_MatchingKeyAndCert_ReturnsTrue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logger := slog.Default()

	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, dacPath := writePEMCert(t, dir, priv, "dac")
	keyPath := writePEMKey(t, dir, priv, "key")

	// PAI.
	_, paiPath := writePEMCert(t, dir, priv, "pai")

	cdPath := filepath.Join(dir, "cd.bin")
	if err := os.WriteFile(cdPath, []byte{0xDE, 0xAD}, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := config.NorthMatterAttestation{
		DACPath:    dacPath,
		DACKeyPath: keyPath,
		PAIPath:    paiPath,
		CDPath:     cdPath,
	}
	key, gotDAC, _, _, ok := loadVendorAttestation(cfg, logger)
	if !ok {
		t.Fatal("expected ok=true for matching key/cert")
	}
	if key == nil {
		t.Error("expected non-nil private key")
	}
	if !bytes.Equal(gotDAC, der) {
		t.Error("returned DAC bytes do not match written cert")
	}
}
