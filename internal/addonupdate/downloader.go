// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package addonupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/httpx"
)

// DefaultStagePath is where the firmware's install_addon expects the
// verified archive (ADR 0057 decisions 2/3).
const DefaultStagePath = "/usr/local/tmp/new_addon.tar.gz"

// maxDownloadBytes bounds both the add-on tarball and checksums.txt
// downloads. The real tarball bundles three architectures' binaries
// plus the embedded SPA; this ceiling is generous but finite so a
// misbehaving or compromised endpoint cannot exhaust disk.
const maxDownloadBytes = 512 << 20 // 512 MiB

// Sentinel errors surfaced by [Downloader.DownloadAndStage].
var (
	ErrChecksumMismatch = errors.New("addonupdate: downloaded asset failed checksum verification")
	ErrChecksumNotFound = errors.New("addonupdate: asset not listed in checksums.txt")
	ErrDownloadTooLarge = errors.New("addonupdate: download exceeded the size ceiling")
)

// Downloader fetches a release asset, verifies its SHA256 against the
// release's checksums.txt BEFORE staging (ADR 0057 decision 2), and
// atomically renames the verified file onto StagePath. Every seam is
// injectable so tests never touch the real network or filesystem.
type Downloader struct {
	HTTPClient *http.Client
	// StagePath is the final, atomically-renamed destination the
	// firmware installer reads from. Defaults to [DefaultStagePath].
	StagePath string
}

// downloadTimeout bounds one asset GET end to end. It is generous
// enough for the multi-arch tarball over a slow link, but finite: a
// stalled CDN or half-open connection would otherwise leave the install
// goroutine blocked with the state machine latched in StateDownloading,
// which makes every later Check/Install return ErrBusy until the daemon
// is restarted.
const downloadTimeout = 10 * time.Minute

// downloaderClient is the HTTP client every Downloader uses unless the
// caller injects one. It owns its transport (see [internal/httpx]) so a
// long-running download does not share a connection pool with the rest
// of the daemon, and carries [downloadTimeout] as its request deadline.
var downloaderClient = sync.OnceValue(func() *http.Client {
	return httpx.NewClient(downloadTimeout)
})

// NewDownloader returns a Downloader wired to a bounded HTTP client of
// its own and [DefaultStagePath].
func NewDownloader() *Downloader {
	return &Downloader{HTTPClient: downloaderClient(), StagePath: DefaultStagePath}
}

func (d *Downloader) httpClient() *http.Client {
	if d.HTTPClient != nil {
		return d.HTTPClient
	}
	return downloaderClient()
}

func (d *Downloader) stagePath() string {
	if d.StagePath != "" {
		return d.StagePath
	}
	return DefaultStagePath
}

// DownloadAndStage fetches info.ChecksumsAsset, resolves the checksum
// line for info.Asset.Name, downloads info.Asset into a temp file next
// to the stage path, verifies its SHA256, and only then renames it
// onto StagePath. A failure at any step (network, missing checksum
// line, mismatch) leaves the stage path untouched — verify-before-move
// is the atomicity guarantee ADR 0057's consequence #1 documents.
func (d *Downloader) DownloadAndStage(ctx context.Context, info ReleaseInfo) error {
	sum, err := d.fetchChecksum(ctx, info)
	if err != nil {
		return err
	}

	dir := filepath.Dir(d.stagePath())
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // staging dir for a root-owned add-on process; not exposed to other users
		return fmt.Errorf("addonupdate: stage dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".new_addon-*.tmp")
	if err != nil {
		return fmt.Errorf("addonupdate: temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	if err := d.download(ctx, info.Asset.DownloadURL, io.MultiWriter(tmp, hasher)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("addonupdate: download asset: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("addonupdate: close temp file: %w", err)
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, sum) {
		return fmt.Errorf("%w: got %s want %s", ErrChecksumMismatch, got, sum)
	}
	if err := os.Rename(tmpPath, d.stagePath()); err != nil {
		return fmt.Errorf("addonupdate: stage rename: %w", err)
	}
	cleanup = false
	return nil
}

// fetchChecksum downloads the release's checksums.txt and resolves the
// hash for info.Asset.Name.
func (d *Downloader) fetchChecksum(ctx context.Context, info ReleaseInfo) (string, error) {
	var buf strings.Builder
	if err := d.download(ctx, info.ChecksumsAsset.DownloadURL, &buf); err != nil {
		return "", fmt.Errorf("addonupdate: download checksums.txt: %w", err)
	}
	return findChecksumLine(buf.String(), info.Asset.Name)
}

// download GETs url and copies the (size-bounded) body to dst.
func (d *Downloader) download(ctx context.Context, url string, dst io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := d.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected status %d", url, resp.StatusCode)
	}
	// Read one byte past the ceiling so an oversized body is detected
	// as an error instead of silently truncating into a checksum
	// mismatch that hides the real cause.
	n, err := io.Copy(dst, io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return err
	}
	if n > maxDownloadBytes {
		return ErrDownloadTooLarge
	}
	return nil
}

// findChecksumLine parses the `sha256sum`-format checksums.txt —
// "<hash>  <filename>" per line, an optional leading "*" on the
// filename marking binary mode — and returns the hash for filename.
func findChecksumLine(content, filename string) (string, error) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == filename {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrChecksumNotFound, filename)
}
