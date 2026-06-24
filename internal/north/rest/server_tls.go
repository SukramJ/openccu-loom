// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
)

// CertReloader holds the active TLS certificate behind an atomic pointer
// so it can be swapped without restarting the listener. The TLS stack
// calls GetCertificate on every handshake; an upload (or an external
// file change followed by Reload) installs a fresh certificate that the
// next handshake picks up. This is the hot-reload path: the SPA shares
// the REST port, so rotating the certificate re-secures both at once
// with zero downtime.
type CertReloader struct {
	certPath string
	keyPath  string
	logger   *slog.Logger
	current  atomic.Pointer[tls.Certificate]
}

// NewCertReloader loads the initial key pair from disk. It fails when
// the files are missing or malformed so a misconfigured TLS setup is
// caught at boot rather than on the first handshake.
func NewCertReloader(certPath, keyPath string, logger *slog.Logger) (*CertReloader, error) {
	if logger == nil {
		logger = slog.Default()
	}
	r := &CertReloader{certPath: certPath, keyPath: keyPath, logger: logger}
	if err := r.Reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// Reload re-reads the key pair from disk and atomically swaps it in.
// Existing connections keep their certificate; new handshakes use the
// reloaded one. On error the previous certificate stays active.
func (r *CertReloader) Reload() error {
	cert, err := tls.LoadX509KeyPair(r.certPath, r.keyPath)
	if err != nil {
		return fmt.Errorf("tls: load key pair: %w", err)
	}
	r.current.Store(&cert)
	r.logger.Info("rest.tls.cert_loaded", slog.String("cert", r.certPath))
	return nil
}

// GetCertificate implements the tls.Config.GetCertificate hook.
func (r *CertReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	if c := r.current.Load(); c != nil {
		return c, nil
	}
	return nil, errors.New("tls: no certificate loaded")
}

// SaveAndReload validates the PEM key pair, writes it to the configured
// cert/key paths (0600), then hot-reloads it. Invalid input is rejected
// before any file is written, so a bad upload never replaces a working
// certificate. Satisfies the handlers' TLS-upload port.
func (r *CertReloader) SaveAndReload(certPEM, keyPEM []byte) error {
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return fmt.Errorf("tls: invalid key pair: %w", err)
	}
	if err := os.WriteFile(r.certPath, certPEM, 0o600); err != nil {
		return fmt.Errorf("tls: write cert: %w", err)
	}
	if err := os.WriteFile(r.keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("tls: write key: %w", err)
	}
	return r.Reload()
}

// TLSConfig returns a config that resolves certificates through the
// reloader. A minimum of TLS 1.2 mirrors the platform defaults the
// Matter / web stacks assume.
func (r *CertReloader) TLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: r.GetCertificate,
		MinVersion:     tls.VersionTLS12,
	}
}
