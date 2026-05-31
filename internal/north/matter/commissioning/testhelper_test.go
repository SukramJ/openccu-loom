// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package commissioning_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// testChain bundles a three-level X.509 chain for use in DAC tests.
type testChain struct {
	PAAKey  *ecdsa.PrivateKey
	PAACert *x509.Certificate
	PAADer  []byte

	PAIKey  *ecdsa.PrivateKey
	PAICert *x509.Certificate
	PAIDer  []byte

	DACKey  *ecdsa.PrivateKey
	DACCert *x509.Certificate
	DACDer  []byte
}

// newTestChain builds a self-signed PAA → PAI → DAC chain. The validity
// window is centred on `now` (±1 year). This is the canonical test
// fixture for all DAC tests.
func newTestChain(t *testing.T, now time.Time) *testChain {
	t.Helper()

	serial := func() *big.Int {
		t.Helper()
		n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			t.Fatalf("serial: %v", err)
		}
		return n
	}

	genKey := func() *ecdsa.PrivateKey {
		t.Helper()
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("ecdsa.GenerateKey: %v", err)
		}
		return k
	}

	notBefore := now.Add(-time.Hour)
	notAfter := now.Add(365 * 24 * time.Hour)

	// PAA — self-signed root.
	paaKey := genKey()
	paaTmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Test PAA"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	paaDer, err := x509.CreateCertificate(rand.Reader, paaTmpl, paaTmpl, &paaKey.PublicKey, paaKey)
	if err != nil {
		t.Fatalf("create PAA: %v", err)
	}
	paaCert, err := x509.ParseCertificate(paaDer)
	if err != nil {
		t.Fatalf("parse PAA: %v", err)
	}

	// PAI — intermediate signed by PAA.
	paiKey := genKey()
	paiTmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Test PAI"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	paiDer, err := x509.CreateCertificate(rand.Reader, paiTmpl, paaCert, &paiKey.PublicKey, paaKey)
	if err != nil {
		t.Fatalf("create PAI: %v", err)
	}
	paiCert, err := x509.ParseCertificate(paiDer)
	if err != nil {
		t.Fatalf("parse PAI: %v", err)
	}

	// DAC — leaf signed by PAI.
	dacKey := genKey()
	dacTmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Test DAC"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		BasicConstraintsValid: true,
		// ExtKeyUsages deliberately empty; Matter uses its own extensions.
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
	dacDer, err := x509.CreateCertificate(rand.Reader, dacTmpl, paiCert, &dacKey.PublicKey, paiKey)
	if err != nil {
		t.Fatalf("create DAC: %v", err)
	}
	dacCert, err := x509.ParseCertificate(dacDer)
	if err != nil {
		t.Fatalf("parse DAC: %v", err)
	}

	return &testChain{
		PAAKey:  paaKey,
		PAACert: paaCert,
		PAADer:  paaDer,
		PAIKey:  paiKey,
		PAICert: paiCert,
		PAIDer:  paiDer,
		DACKey:  dacKey,
		DACCert: dacCert,
		DACDer:  dacDer,
	}
}

// paaPEM encodes tc.PAACert as a PEM block for use with LoadPAAPoolFromPEM.
func (tc *testChain) paaPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: tc.PAADer,
	})
}

// paaPool returns a CertPool containing only the PAA certificate.
func (tc *testChain) paaPool(t *testing.T) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(tc.PAACert)
	return pool
}

// validChallenge returns a 16-byte all-zero challenge suitable as a
// placeholder AttestationChallenge in tests that do not exercise PASE.
func validChallenge() []byte { return make([]byte, 16) }

// validNonce32 returns a 32-byte all-zero nonce.
func validNonce32() []byte { return make([]byte, 32) }
