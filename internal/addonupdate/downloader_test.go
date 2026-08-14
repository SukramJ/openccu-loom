// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package addonupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newDownloaderTestServer serves assetBody at /asset and checksumsBody at
// /checksums.txt, so DownloadAndStage's two GETs (checksums, then asset)
// both resolve against one httptest server.
func newDownloaderTestServer(t *testing.T, assetBody []byte, checksumsBody string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(assetBody)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(checksumsBody))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDownloaderDownloadAndStageSuccess(t *testing.T) {
	t.Parallel()

	payload := []byte("fake tarball payload bytes")
	sum := sha256.Sum256(payload)
	assetName := "openccu-loom-ccu-1.2.3.tar.gz"
	checksums := hex.EncodeToString(sum[:]) + "  " + assetName + "\n"

	srv := newDownloaderTestServer(t, payload, checksums)
	stagePath := filepath.Join(t.TempDir(), "new_addon.tar.gz")
	d := &Downloader{HTTPClient: &http.Client{}, StagePath: stagePath}
	info := ReleaseInfo{
		Asset:          ReleaseAsset{Name: assetName, DownloadURL: srv.URL + "/asset"},
		ChecksumsAsset: ReleaseAsset{Name: "checksums.txt", DownloadURL: srv.URL + "/checksums.txt"},
	}

	if err := d.DownloadAndStage(context.Background(), info); err != nil {
		t.Fatalf("DownloadAndStage() error = %v", err)
	}

	got, err := os.ReadFile(stagePath)
	if err != nil {
		t.Fatalf("ReadFile(stagePath) error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("staged content = %q, want %q", got, payload)
	}
}

func TestDownloaderDownloadAndStageChecksumMismatch(t *testing.T) {
	t.Parallel()

	payload := []byte("fake tarball payload bytes")
	assetName := "openccu-loom-ccu-1.2.3.tar.gz"
	wrongSum := strings.Repeat("0", 64)
	checksums := wrongSum + "  " + assetName + "\n"

	srv := newDownloaderTestServer(t, payload, checksums)
	stagePath := filepath.Join(t.TempDir(), "new_addon.tar.gz")
	d := &Downloader{HTTPClient: &http.Client{}, StagePath: stagePath}
	info := ReleaseInfo{
		Asset:          ReleaseAsset{Name: assetName, DownloadURL: srv.URL + "/asset"},
		ChecksumsAsset: ReleaseAsset{Name: "checksums.txt", DownloadURL: srv.URL + "/checksums.txt"},
	}

	err := d.DownloadAndStage(context.Background(), info)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("DownloadAndStage() error = %v, want ErrChecksumMismatch", err)
	}
	if _, statErr := os.Stat(stagePath); !os.IsNotExist(statErr) {
		t.Errorf("stage path exists after checksum mismatch: stat error = %v", statErr)
	}
}

func TestDownloaderDownloadAndStageChecksumNotFound(t *testing.T) {
	t.Parallel()

	payload := []byte("fake tarball payload bytes")
	sum := sha256.Sum256(payload)
	assetName := "openccu-loom-ccu-1.2.3.tar.gz"
	// checksums.txt lists a different filename than the one we ask for.
	checksums := hex.EncodeToString(sum[:]) + "  some-other-file.tar.gz\n"

	srv := newDownloaderTestServer(t, payload, checksums)
	stagePath := filepath.Join(t.TempDir(), "new_addon.tar.gz")
	d := &Downloader{HTTPClient: &http.Client{}, StagePath: stagePath}
	info := ReleaseInfo{
		Asset:          ReleaseAsset{Name: assetName, DownloadURL: srv.URL + "/asset"},
		ChecksumsAsset: ReleaseAsset{Name: "checksums.txt", DownloadURL: srv.URL + "/checksums.txt"},
	}

	err := d.DownloadAndStage(context.Background(), info)
	if !errors.Is(err, ErrChecksumNotFound) {
		t.Fatalf("DownloadAndStage() error = %v, want ErrChecksumNotFound", err)
	}
	if _, statErr := os.Stat(stagePath); !os.IsNotExist(statErr) {
		t.Errorf("stage path exists after missing checksum line: stat error = %v", statErr)
	}
}

func TestDownloaderDownloadAndStageNetworkError(t *testing.T) {
	t.Parallel()

	payload := []byte("fake tarball payload bytes")
	sum := sha256.Sum256(payload)
	assetName := "openccu-loom-ccu-1.2.3.tar.gz"
	checksums := hex.EncodeToString(sum[:]) + "  " + assetName + "\n"

	// checksums.txt resolves fine, but the asset URL points at a server
	// that has already been shut down.
	checksumSrv := newDownloaderTestServer(t, payload, checksums)
	closedSrv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedSrv.Close()

	stagePath := filepath.Join(t.TempDir(), "new_addon.tar.gz")
	d := &Downloader{HTTPClient: &http.Client{}, StagePath: stagePath}
	info := ReleaseInfo{
		Asset:          ReleaseAsset{Name: assetName, DownloadURL: closedSrv.URL + "/asset"},
		ChecksumsAsset: ReleaseAsset{Name: "checksums.txt", DownloadURL: checksumSrv.URL + "/checksums.txt"},
	}

	err := d.DownloadAndStage(context.Background(), info)
	if err == nil {
		t.Fatal("DownloadAndStage() error = nil, want non-nil")
	}
	if _, statErr := os.Stat(stagePath); !os.IsNotExist(statErr) {
		t.Errorf("stage path exists after network error: stat error = %v", statErr)
	}
}

// TestNewDownloaderClientIsBoundedAndOwnsItsTransport pins the two
// properties the install sequence depends on: the tarball GET carries a
// deadline of its own (a stalled CDN would otherwise latch the state
// machine in StateDownloading for the daemon's uptime, making every
// later Check/Install return ErrBusy), and the client does not share the
// process-wide default transport.
func TestNewDownloaderClientIsBoundedAndOwnsItsTransport(t *testing.T) {
	t.Parallel()

	clients := map[string]*http.Client{
		"NewDownloader":           NewDownloader().HTTPClient,
		"zero-value httpClient()": (&Downloader{}).httpClient(),
	}
	for name, client := range clients {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if client == nil {
				t.Fatal("client is nil")
			}
			if client == http.DefaultClient {
				t.Fatal("client is http.DefaultClient: no request deadline and the shared default transport")
			}
			if client.Timeout <= 0 {
				t.Errorf("client.Timeout = %v, want a positive deadline", client.Timeout)
			}
			if client.Transport == nil {
				t.Error("client.Transport is nil: falls back to the shared http.DefaultTransport")
			}
			if client.Transport == http.DefaultTransport {
				t.Error("client.Transport is http.DefaultTransport: connection pool shared with unrelated callers")
			}
		})
	}
}
