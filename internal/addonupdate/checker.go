// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package addonupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"github.com/SukramJ/openccu-loom/internal/httpx"
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
	// Prerelease reports that GitHub marks the release as a pre-release.
	Prerelease bool
}

// ErrNoAddonAsset / ErrNoChecksumsAsset are returned when the latest
// release does not carry the asset the updater needs. Both fail the
// check closed — Install never runs against a release the daemon
// cannot verify.
var (
	ErrNoAddonAsset     = errors.New("addonupdate: release has no matching add-on asset")
	ErrNoChecksumsAsset = errors.New("addonupdate: release has no checksums.txt asset")
	ErrMalformedRelease = errors.New("addonupdate: malformed release metadata")
	// ErrNoRelease is returned when no published release passes the
	// channel filter (only drafts, or only pre-releases on the final
	// channel).
	ErrNoRelease = errors.New("addonupdate: no release on the selected channel")
)

// Checker resolves the latest published release of this project via
// the GitHub REST API. HTTPClient and BaseURL are injectable so tests
// run against an httptest server instead of the real network.
type Checker struct {
	HTTPClient *http.Client
	// BaseURL overrides [defaultGitHubAPIBase]; empty uses the default.
	BaseURL string
}

// releaseCheckTimeout bounds one GitHub API round trip end to end. The
// response is a few KB, so anything still open after this has stalled
// rather than progressed — and the check runs on the recurring cadence's
// single goroutine, which a request without a deadline parks for the
// daemon's remaining uptime.
const releaseCheckTimeout = 30 * time.Second

// checkerClient is the HTTP client every Checker uses unless the caller
// injects one. It owns its transport (see [internal/httpx]) so the
// release check does not share a connection pool with unrelated callers,
// and carries [releaseCheckTimeout] as its request deadline.
var checkerClient = sync.OnceValue(func() *http.Client {
	return httpx.NewClient(releaseCheckTimeout)
})

// NewChecker returns a Checker wired to the real GitHub API over a
// bounded HTTP client of its own.
func NewChecker() *Checker {
	return &Checker{HTTPClient: checkerClient(), BaseURL: defaultGitHubAPIBase}
}

func (c *Checker) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return checkerClient()
}

func (c *Checker) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return defaultGitHubAPIBase
}

// githubReleaseResponse is the subset of one GitHub release object the
// checker needs. The "list releases" endpoint returns an array of these.
type githubReleaseResponse struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// releaseListPageSize bounds how many of the newest releases one check
// considers. GitHub lists releases newest first, so a page this size
// spans many release cycles of final and pre-releases alike.
const releaseListPageSize = 30

// LatestRelease resolves the newest release the operator's channel
// admits and its ccu-addon tarball + checksum assets.
//
// It lists releases (GET {base}/releases) instead of asking for
// /releases/latest, because GitHub's "latest" never names a
// pre-release, so an opted-in pre-release channel could not see one.
// Drafts are always skipped; pre-releases only when includePrereleases
// is false. Among the admitted releases the highest SemVer tag wins, so
// the answer does not depend on the order GitHub lists them in; a tag
// that is not valid SemVer is ignored.
//
// Returns ErrNoRelease when no release is admitted, and ErrNoAddonAsset
// / ErrNoChecksumsAsset when the chosen one lacks either asset, so
// callers fail closed rather than staging an unverifiable archive.
func (c *Checker) LatestRelease(ctx context.Context, includePrereleases bool) (ReleaseInfo, error) {
	url := fmt.Sprintf("%s/releases?per_page=%d", c.baseURL(), releaseListPageSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return ReleaseInfo{}, fmt.Errorf("addonupdate: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return ReleaseInfo{}, fmt.Errorf("addonupdate: fetch releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ReleaseInfo{}, fmt.Errorf("addonupdate: releases: unexpected status %d", resp.StatusCode)
	}

	var list []githubReleaseResponse
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxReleaseBodyBytes))
	if err := dec.Decode(&list); err != nil {
		return ReleaseInfo{}, fmt.Errorf("%w: %w", ErrMalformedRelease, err)
	}
	body, ok := pickRelease(list, includePrereleases)
	if !ok {
		return ReleaseInfo{}, ErrNoRelease
	}
	return resolveAssets(body)
}

// pickRelease returns the admitted release with the highest SemVer tag.
func pickRelease(list []githubReleaseResponse, includePrereleases bool) (githubReleaseResponse, bool) {
	var best githubReleaseResponse
	bestVer := ""
	for _, r := range list {
		if r.Draft || (r.Prerelease && !includePrereleases) {
			continue
		}
		v := "v" + strings.TrimPrefix(r.TagName, "v")
		if !semver.IsValid(v) {
			continue
		}
		// A pre-release flag GitHub did not set still marks the tag as
		// one when it carries a SemVer pre-release suffix.
		if semver.Prerelease(v) != "" && !includePrereleases {
			continue
		}
		if bestVer == "" || semver.Compare(v, bestVer) > 0 {
			best, bestVer = r, v
		}
	}
	return best, bestVer != ""
}

// resolveAssets maps a chosen release onto [ReleaseInfo], locating the
// add-on tarball and its checksum source.
func resolveAssets(body githubReleaseResponse) (ReleaseInfo, error) {
	version := strings.TrimPrefix(body.TagName, "v")
	wantAsset := ccuAddonAssetPrefix + version + ccuAddonAssetSuffix

	info := ReleaseInfo{Tag: body.TagName, Version: version, ReleaseURL: body.HTMLURL, Prerelease: body.Prerelease}
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
