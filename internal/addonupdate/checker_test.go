// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package addonupdate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newCheckerTestServer spins up an httptest server that always responds
// with the given status and body, and returns a Checker pointed at it.
func newCheckerTestServer(t *testing.T, status int, body string) *Checker {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Checker{HTTPClient: &http.Client{}, BaseURL: srv.URL}
}

func TestCheckerLatestReleaseSuccess(t *testing.T) {
	t.Parallel()

	body := `{
		"tag_name": "v1.2.3",
		"html_url": "https://example.invalid/releases/v1.2.3",
		"assets": [
			{"name": "openccu-loom-ccu-1.2.3.tar.gz", "browser_download_url": "https://example.invalid/asset.tar.gz"},
			{"name": "checksums.txt", "browser_download_url": "https://example.invalid/checksums.txt"}
		]
	}`
	checker := newCheckerTestServer(t, http.StatusOK, body)

	info, err := checker.LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("LatestRelease() error = %v", err)
	}
	if info.Tag != "v1.2.3" {
		t.Errorf("Tag = %q, want v1.2.3", info.Tag)
	}
	if info.Version != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", info.Version)
	}
	if info.ReleaseURL != "https://example.invalid/releases/v1.2.3" {
		t.Errorf("ReleaseURL = %q", info.ReleaseURL)
	}
	if info.Asset.Name != "openccu-loom-ccu-1.2.3.tar.gz" {
		t.Errorf("Asset.Name = %q", info.Asset.Name)
	}
	if info.Asset.DownloadURL != "https://example.invalid/asset.tar.gz" {
		t.Errorf("Asset.DownloadURL = %q", info.Asset.DownloadURL)
	}
	if info.ChecksumsAsset.DownloadURL != "https://example.invalid/checksums.txt" {
		t.Errorf("ChecksumsAsset.DownloadURL = %q", info.ChecksumsAsset.DownloadURL)
	}
}

func TestCheckerLatestReleaseMissingAddonAsset(t *testing.T) {
	t.Parallel()

	body := `{"tag_name":"v1.2.3","html_url":"https://example.invalid","assets":[
		{"name":"checksums.txt","browser_download_url":"https://example.invalid/checksums.txt"}
	]}`
	checker := newCheckerTestServer(t, http.StatusOK, body)

	_, err := checker.LatestRelease(context.Background())
	if !errors.Is(err, ErrNoAddonAsset) {
		t.Fatalf("LatestRelease() error = %v, want ErrNoAddonAsset", err)
	}
}

func TestCheckerLatestReleaseMissingChecksumsAsset(t *testing.T) {
	t.Parallel()

	body := `{"tag_name":"v1.2.3","html_url":"https://example.invalid","assets":[
		{"name":"openccu-loom-ccu-1.2.3.tar.gz","browser_download_url":"https://example.invalid/asset.tar.gz"}
	]}`
	checker := newCheckerTestServer(t, http.StatusOK, body)

	_, err := checker.LatestRelease(context.Background())
	if !errors.Is(err, ErrNoChecksumsAsset) {
		t.Fatalf("LatestRelease() error = %v, want ErrNoChecksumsAsset", err)
	}
}

func TestCheckerLatestReleaseMalformedJSON(t *testing.T) {
	t.Parallel()

	checker := newCheckerTestServer(t, http.StatusOK, `{not valid json`)

	_, err := checker.LatestRelease(context.Background())
	if !errors.Is(err, ErrMalformedRelease) {
		t.Fatalf("LatestRelease() error = %v, want ErrMalformedRelease", err)
	}
}

func TestCheckerLatestReleaseMissingTagName(t *testing.T) {
	t.Parallel()

	body := `{"html_url":"https://example.invalid","assets":[]}`
	checker := newCheckerTestServer(t, http.StatusOK, body)

	_, err := checker.LatestRelease(context.Background())
	if !errors.Is(err, ErrMalformedRelease) {
		t.Fatalf("LatestRelease() error = %v, want ErrMalformedRelease", err)
	}
}

func TestCheckerLatestReleaseNon200Status(t *testing.T) {
	t.Parallel()

	checker := newCheckerTestServer(t, http.StatusInternalServerError, "boom")

	_, err := checker.LatestRelease(context.Background())
	if err == nil {
		t.Fatal("LatestRelease() error = nil, want non-nil")
	}
}

func TestCheckerLatestReleaseContextCancelled(t *testing.T) {
	t.Parallel()

	body := `{"tag_name":"v1.2.3","html_url":"https://example.invalid","assets":[]}`
	checker := newCheckerTestServer(t, http.StatusOK, body)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := checker.LatestRelease(ctx)
	if err == nil {
		t.Fatal("LatestRelease(cancelled ctx) error = nil, want non-nil")
	}
}

func TestIsNewer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		current, release string
		want             bool
	}{
		{"patch bump is newer", "1.2.3", "1.2.4", true},
		{"patch downgrade is not newer", "1.2.4", "1.2.3", false},
		{"identical semver is not newer", "1.2.3", "1.2.3", false},
		{"leading v tolerated on either side", "v1.2.3", "1.3.0", true},
		{"non-semver current falls back to inequality", "dev", "1.0.0", true},
		{"both non-semver and equal is not newer", "dev-build-1", "dev-build-1", false},
		{"both non-semver and different is newer", "dev-build-1", "dev-build-2", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsNewer(tt.current, tt.release); got != tt.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.release, got, tt.want)
			}
		})
	}
}

// TestCheckerLatestReleasePrefersSha256Sidecar pins the verification
// source order: the per-asset .sha256 sidecar wins over checksums.txt
// regardless of asset order, and stands alone when checksums.txt is
// absent (the release workflow uploads the sidecar because goreleaser's
// checksums.txt is finalized before the add-on package exists).
func TestCheckerLatestReleasePrefersSha256Sidecar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		assets string
		want   string
	}{
		{
			name: "sidecar listed after checksums.txt",
			assets: `{"name": "checksums.txt", "browser_download_url": "https://example.invalid/checksums.txt"},
				{"name": "openccu-loom-ccu-1.2.3.tar.gz", "browser_download_url": "https://example.invalid/asset.tar.gz"},
				{"name": "openccu-loom-ccu-1.2.3.tar.gz.sha256", "browser_download_url": "https://example.invalid/asset.sha256"}`,
			want: "https://example.invalid/asset.sha256",
		},
		{
			name: "sidecar listed before checksums.txt",
			assets: `{"name": "openccu-loom-ccu-1.2.3.tar.gz.sha256", "browser_download_url": "https://example.invalid/asset.sha256"},
				{"name": "openccu-loom-ccu-1.2.3.tar.gz", "browser_download_url": "https://example.invalid/asset.tar.gz"},
				{"name": "checksums.txt", "browser_download_url": "https://example.invalid/checksums.txt"}`,
			want: "https://example.invalid/asset.sha256",
		},
		{
			name: "sidecar only",
			assets: `{"name": "openccu-loom-ccu-1.2.3.tar.gz", "browser_download_url": "https://example.invalid/asset.tar.gz"},
				{"name": "openccu-loom-ccu-1.2.3.tar.gz.sha256", "browser_download_url": "https://example.invalid/asset.sha256"}`,
			want: "https://example.invalid/asset.sha256",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := `{"tag_name": "v1.2.3", "html_url": "https://example.invalid/r", "assets": [` + tc.assets + `]}`
			checker := newCheckerTestServer(t, http.StatusOK, body)
			info, err := checker.LatestRelease(context.Background())
			if err != nil {
				t.Fatalf("LatestRelease() error = %v", err)
			}
			if info.ChecksumsAsset.DownloadURL != tc.want {
				t.Errorf("ChecksumsAsset.DownloadURL = %q, want %q", info.ChecksumsAsset.DownloadURL, tc.want)
			}
		})
	}
}

// TestNewCheckerClientIsBoundedAndOwnsItsTransport pins the two
// properties the periodic check depends on: the GitHub request carries a
// deadline of its own (a server that accepts the connection and never
// answers must not pin the cadence goroutine for the daemon's uptime),
// and the client does not share the process-wide default transport.
func TestNewCheckerClientIsBoundedAndOwnsItsTransport(t *testing.T) {
	t.Parallel()

	clients := map[string]*http.Client{
		"NewChecker":              NewChecker().HTTPClient,
		"zero-value httpClient()": (&Checker{}).httpClient(),
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
