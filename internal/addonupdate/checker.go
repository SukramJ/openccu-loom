// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package addonupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/mod/semver"
)

// defaultGitHubAPIBase is the REST API root for this project's own
// releases. ADR 0057 decision 2 restricts the updater to HTTPS
// GitHub releases of this repo only — no third-party feeds.
const defaultGitHubAPIBase = "https://api.github.com/repos/SukramJ/openccu-loom"

// ccuAddonAssetPrefix/Suffix build the exact asset filename
// script/build_ccu_addon.sh emits: "openccu-loom-ccu-<version>.tar.gz"
// (version has no leading "v" — see build_ccu_addon.sh's
// `VERSION="${VERSION#v}"`).
const (
	ccuAddonAssetPrefix = "openccu-loom-ccu-"
	ccuAddonAssetSuffix = ".tar.gz"
	checksumsAssetName  = "checksums.txt"
)

// maxReleaseBodyBytes bounds the GitHub API response decode — a
// defensive ceiling against a misbehaving or compromised endpoint,
// not a realistic release-metadata size (real responses are a few KB).
const maxReleaseBodyBytes = 1 << 20 // 1 MiB

// ReleaseAsset is one downloadable file attached to a GitHub release.
type ReleaseAsset struct {
	Name        string
	DownloadURL string
}

// ReleaseInfo is what [Checker.LatestRelease] resolves.
type ReleaseInfo struct {
	// Tag is the raw GitHub tag (e.g. "v1.2.3").
	Tag string
	// Version is Tag with a leading "v" stripped.
	Version string
	// ReleaseURL is the release-notes page.
	ReleaseURL string
	// Asset is the ccu-addon tarball for this release.
	Asset ReleaseAsset
	// ChecksumsAsset is the release's checksums.txt, used to verify
	// Asset's SHA256 before it is staged (ADR 0057 decision 2).
	ChecksumsAsset ReleaseAsset
}

// ErrNoAddonAsset / ErrNoChecksumsAsset are returned when the latest
// release does not carry the asset the updater needs. Both fail the
// check closed — Install never runs against a release the daemon
// cannot verify.
var (
	ErrNoAddonAsset     = errors.New("addonupdate: release has no matching add-on asset")
	ErrNoChecksumsAsset = errors.New("addonupdate: release has no checksums.txt asset")
	ErrMalformedRelease = errors.New("addonupdate: malformed release metadata")
)

// Checker resolves the latest published release of this project via
// the GitHub REST API. HTTPClient and BaseURL are injectable so tests
// run against an httptest server instead of the real network.
type Checker struct {
	HTTPClient *http.Client
	// BaseURL overrides [defaultGitHubAPIBase]; empty uses the default.
	BaseURL string
}

// NewChecker returns a Checker wired to the real GitHub API over the
// default HTTP client.
func NewChecker() *Checker {
	return &Checker{HTTPClient: http.DefaultClient, BaseURL: defaultGitHubAPIBase}
}

func (c *Checker) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *Checker) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return defaultGitHubAPIBase
}

// githubReleaseResponse is the subset of the GitHub "get the latest
// release" response shape the checker needs.
type githubReleaseResponse struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// LatestRelease fetches GET {base}/releases/latest and resolves the
// ccu-addon tarball + checksums.txt assets. Returns ErrNoAddonAsset /
// ErrNoChecksumsAsset when either is missing so callers fail closed
// rather than staging an unverifiable archive.
func (c *Checker) LatestRelease(ctx context.Context) (ReleaseInfo, error) {
	url := c.baseURL() + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return ReleaseInfo{}, fmt.Errorf("addonupdate: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return ReleaseInfo{}, fmt.Errorf("addonupdate: fetch latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ReleaseInfo{}, fmt.Errorf("addonupdate: latest release: unexpected status %d", resp.StatusCode)
	}

	var body githubReleaseResponse
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxReleaseBodyBytes))
	if err := dec.Decode(&body); err != nil {
		return ReleaseInfo{}, fmt.Errorf("%w: %w", ErrMalformedRelease, err)
	}
	if body.TagName == "" {
		return ReleaseInfo{}, fmt.Errorf("%w: missing tag_name", ErrMalformedRelease)
	}

	version := strings.TrimPrefix(body.TagName, "v")
	wantAsset := ccuAddonAssetPrefix + version + ccuAddonAssetSuffix

	info := ReleaseInfo{Tag: body.TagName, Version: version, ReleaseURL: body.HTMLURL}
	// The per-asset .sha256 sidecar is the primary verification source —
	// the release workflow uploads it right next to the tarball because
	// goreleaser's checksums.txt is finalized before the add-on package
	// is built. checksums.txt stays accepted as a fallback so a release
	// that covers the tarball there verifies too.
	wantSidecar := wantAsset + ".sha256"
	for _, a := range body.Assets {
		switch a.Name {
		case wantAsset:
			info.Asset = ReleaseAsset{Name: a.Name, DownloadURL: a.BrowserDownloadURL}
		case wantSidecar, checksumsAssetName:
			if a.Name == wantSidecar || info.ChecksumsAsset.DownloadURL == "" {
				info.ChecksumsAsset = ReleaseAsset{Name: a.Name, DownloadURL: a.BrowserDownloadURL}
			}
		}
	}
	if info.Asset.DownloadURL == "" {
		return ReleaseInfo{}, fmt.Errorf("%w: %s", ErrNoAddonAsset, wantAsset)
	}
	if info.ChecksumsAsset.DownloadURL == "" {
		return ReleaseInfo{}, fmt.Errorf("%w: release %s", ErrNoChecksumsAsset, info.Tag)
	}
	return info, nil
}

// IsNewer reports whether candidate is a newer version than current.
// Both are normalised to a "v"-prefixed semver string for comparison.
// When either side is not valid semver (a `git describe` dev build
// like "0.50.0-5-gabc1234-dirty", or the placeholder "dev"/"none"),
// it falls back to a plain inequality: any different candidate is
// treated as "newer" so a dev build never permanently masks a real
// release, while an exact string match never claims one.
func IsNewer(current, candidate string) bool {
	cv := "v" + strings.TrimPrefix(current, "v")
	kv := "v" + strings.TrimPrefix(candidate, "v")
	if semver.IsValid(cv) && semver.IsValid(kv) {
		return semver.Compare(kv, cv) > 0
	}
	return current != candidate && candidate != ""
}
