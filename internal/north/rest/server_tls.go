// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rest

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// CertReloader holds the active TLS certificate behind an atomic pointer
// so it can be swapped without restarting the listener. The TLS stack
// calls GetCertificate on every handshake, and that is also where the
// on-disk pair is polled: an ACME client overwriting the files is
// picked up by the next handshake. This is the hot-reload path: the SPA
// shares the REST port, so rotating the certificate re-secures both at
// once with zero downtime.
type CertReloader struct {
	certPath string
	keyPath  string
	logger   *slog.Logger
	current  atomic.Pointer[tls.Certificate]

	// mu guards loaded, the stat fingerprint of the pair currently in
	// current. It is taken only around the fingerprint compare, never
	// across the file reads.
	mu     sync.Mutex
	loaded string
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
	// Fingerprint before reading: a change landing between the two is
	// then seen as still-pending and reloaded again, never missed.
	fp := r.diskFingerprint()
	cert, err := tls.LoadX509KeyPair(r.certPath, r.keyPath)
	if err != nil {
		return fmt.Errorf("tls: load key pair: %w", err)
	}
	r.current.Store(&cert)
	r.setLoaded(fp)
	r.logger.Info("rest.tls.cert_loaded", slog.String("cert", r.certPath))
	return nil
}

// diskFingerprint identifies the on-disk pair by size and modification
// time. An unreadable file contributes an empty part, which differs
// from every readable state and so never masks a later change.
func (r *CertReloader) diskFingerprint() string {
	stamp := func(path string) string {
		fi, err := os.Stat(path)
		if err != nil {
			return "-"
		}
		return fmt.Sprintf("%d@%d", fi.Size(), fi.ModTime().UnixNano())
	}
	return stamp(r.certPath) + "|" + stamp(r.keyPath)
}

// setLoaded records the fingerprint the current certificate was loaded
// from.
func (r *CertReloader) setLoaded(fp string) {
	r.mu.Lock()
	r.loaded = fp
	r.mu.Unlock()
}

// reloadIfChanged re-reads the pair when the files changed since the
// last load. The daemon runs no file watcher, and the certificate is
// documented to operators as hot-reloaded on change, so the handshake
// is the poll point: two stat calls per handshake, and a rotation is
// live on the next connection without a restart. A failed reload keeps
// the previous certificate and records the fingerprint anyway, so a
// broken pair is reported once per change rather than per handshake.
func (r *CertReloader) reloadIfChanged() {
	fp := r.diskFingerprint()
	r.mu.Lock()
	unchanged := fp == r.loaded
	r.mu.Unlock()
	if unchanged {
		return
	}
	if err := r.Reload(); err != nil {
		r.setLoaded(fp)
		r.logger.Warn("rest.tls.reload_failed",
			slog.String("cert", r.certPath), slog.String("err", err.Error()))
	}
}

// GetCertificate implements the tls.Config.GetCertificate hook and
// polls the on-disk pair for an external rotation first.
func (r *CertReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.reloadIfChanged()
	if c := r.current.Load(); c != nil {
		return c, nil
	}
	return nil, errors.New("tls: no certificate loaded")
}

// SaveAndReload validates the PEM key pair, installs it at the
// configured cert/key paths (0600), then hot-reloads it. Invalid input
// is rejected before any file is touched, so a bad upload never
// replaces a working certificate. Both halves are staged as temporary
// files next to their targets and only then renamed into place: two
// plain writes left a new certificate beside the old key whenever the
// second write failed, and the next boot could not load that pair and
// fell back to plain HTTP. Satisfies the handlers' TLS-upload port.
func (r *CertReloader) SaveAndReload(certPEM, keyPEM []byte) error {
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return fmt.Errorf("tls: invalid key pair: %w", err)
	}
	certTmp, err := stageFile(r.certPath, certPEM)
	if err != nil {
		return fmt.Errorf("tls: stage cert: %w", err)
	}
	keyTmp, err := stageFile(r.keyPath, keyPEM)
	if err != nil {
		_ = os.Remove(certTmp)
		return fmt.Errorf("tls: stage key: %w", err)
	}
	// The key goes first and its previous content is kept: if the
	// certificate rename then fails, the old pair is restored whole
	// rather than left half-rotated.
	prevKey, prevKeyErr := os.ReadFile(r.keyPath)
	if err := os.Rename(keyTmp, r.keyPath); err != nil {
		_ = os.Remove(certTmp)
		_ = os.Remove(keyTmp)
		return fmt.Errorf("tls: install key: %w", err)
	}
	if err := os.Rename(certTmp, r.certPath); err != nil {
		if prevKeyErr == nil {
			// keyPath is the operator's configured path, never request
			// input, and this only restores the bytes just read from it.
			_ = os.WriteFile(r.keyPath, prevKey, 0o600) //nolint:gosec // configured path, restoring its own previous content
		}
		_ = os.Remove(certTmp)
		return fmt.Errorf("tls: install cert: %w", err)
	}
	return r.Reload()
}

// stageFile writes data to a 0600 temporary file next to path and
// returns its name, ready to be renamed over path. The caller owns the
// temporary file from here on.
func stageFile(path string, data []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
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
