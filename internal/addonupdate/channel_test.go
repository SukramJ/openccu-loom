// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package addonupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// releaseJSON renders one release object carrying both assets the
// checker requires.
func releaseJSON(tag string, prerelease, draft bool) string {
	v := strings.TrimPrefix(tag, "v")
	return fmt.Sprintf(`{"tag_name":%q,"html_url":"https://example.invalid/%s","draft":%t,"prerelease":%t,"assets":[`+
		`{"name":"openccu-loom-ccu-%s.tar.gz","browser_download_url":"https://example.invalid/%s.tar.gz"},`+
		`{"name":"checksums.txt","browser_download_url":"https://example.invalid/%s.txt"}]}`,
		tag, v, draft, prerelease, v, v, v)
}

func releaseList(items ...string) string { return "[" + strings.Join(items, ",") + "]" }

// TestCheckerLatestReleaseChannel pins the channel filter: the final
// channel never resolves a pre-release (flagged by GitHub or by its
// SemVer suffix), the pre-release channel resolves the highest SemVer
// tag of either kind, drafts never count, and list order is irrelevant.
func TestCheckerLatestReleaseChannel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		body       string
		includePre bool
		wantTag    string
		wantPre    bool
		wantErr    error
	}{
		{
			name:    "final channel skips a newer pre-release",
			body:    releaseList(releaseJSON("v1.24.0-beta.1", true, false), releaseJSON("v1.23.0", false, false)),
			wantTag: "v1.23.0",
		},
		{
			name:       "pre-release channel takes the newer pre-release",
			body:       releaseList(releaseJSON("v1.24.0-beta.1", true, false), releaseJSON("v1.23.0", false, false)),
			includePre: true,
			wantTag:    "v1.24.0-beta.1",
			wantPre:    true,
		},
		{
			name:       "pre-release channel takes the final once it is out",
			body:       releaseList(releaseJSON("v1.24.0", false, false), releaseJSON("v1.24.0-beta.2", true, false)),
			includePre: true,
			wantTag:    "v1.24.0",
		},
		{
			name:       "beta numbers compare numerically",
			body:       releaseList(releaseJSON("v1.24.0-beta.2", true, false), releaseJSON("v1.24.0-beta.10", true, false)),
			includePre: true,
			wantTag:    "v1.24.0-beta.10",
			wantPre:    true,
		},
		{
			name:    "list order does not decide",
			body:    releaseList(releaseJSON("v1.22.0", false, false), releaseJSON("v1.23.0", false, false)),
			wantTag: "v1.23.0",
		},
		{
			name:    "an unflagged SemVer pre-release tag stays off the final channel",
			body:    releaseList(releaseJSON("v1.24.0-beta.1", false, false), releaseJSON("v1.23.0", false, false)),
			wantTag: "v1.23.0",
		},
		{
			name:       "drafts never count",
			body:       releaseList(releaseJSON("v1.25.0", false, true), releaseJSON("v1.23.0", false, false)),
			includePre: true,
			wantTag:    "v1.23.0",
		},
		{
			name:    "only pre-releases on the final channel",
			body:    releaseList(releaseJSON("v1.24.0-beta.1", true, false)),
			wantErr: ErrNoRelease,
		},
		{
			name:    "empty list",
			body:    "[]",
			wantErr: ErrNoRelease,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checker := newCheckerTestServer(t, http.StatusOK, tc.body)
			info, err := checker.LatestRelease(context.Background(), tc.includePre)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("LatestRelease() error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LatestRelease() error = %v", err)
			}
			if info.Tag != tc.wantTag {
				t.Errorf("Tag = %q, want %q", info.Tag, tc.wantTag)
			}
			if info.Prerelease != tc.wantPre {
				t.Errorf("Prerelease = %v, want %v", info.Prerelease, tc.wantPre)
			}
		})
	}
}

// TestUpdaterCheckResolvesChannelPerCheck pins that the channel is read
// at every Check, not captured at construction: flipping the resolver
// between two checks changes what the second one reports.
func TestUpdaterCheckResolvesChannelPerCheck(t *testing.T) {
	t.Parallel()

	body := releaseList(releaseJSON("v1.24.0-beta.1", true, false), releaseJSON("v1.23.0", false, false))
	mux := http.NewServeMux()
	mux.HandleFunc("/releases", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
	var includePre atomic.Bool
	u := NewUpdater(Deps{
		Capability: CapabilityProbe{
			IsAddonBuild:  func() bool { return true },
			StatInstaller: func(string) (os.FileInfo, error) { return fakeFileInfo{mode: 0o755}, nil },
		},
		Checker:            &Checker{HTTPClient: &http.Client{Transport: handlerTransport{h: mux}}, BaseURL: "http://releases.invalid"},
		CurrentVersion:     "1.22.0",
		IncludePrereleases: func(context.Context) bool { return includePre.Load() },
	})

	if err := u.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if st := u.Status(); st.LatestVersion != "1.23.0" || st.LatestPrerelease {
		t.Fatalf("final channel: LatestVersion = %q, LatestPrerelease = %v; want 1.23.0, false", st.LatestVersion, st.LatestPrerelease)
	}

	includePre.Store(true)
	if err := u.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if st := u.Status(); st.LatestVersion != "1.24.0-beta.1" || !st.LatestPrerelease || !st.UpdateAvailable {
		t.Fatalf("pre-release channel: status = %+v; want 1.24.0-beta.1, prerelease, update available", st)
	}
}

// TestIsNewerAcrossPrereleases pins the version ordering the channel
// relies on: a running pre-release is superseded by its own final and
// by a later beta, never by an older final.
func TestIsNewerAcrossPrereleases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		current, candidate string
		want               bool
	}{
		{"1.24.0-beta.1", "1.24.0", true},
		{"1.24.0-beta.1", "1.24.0-beta.2", true},
		{"1.24.0-beta.2", "1.24.0-beta.10", true},
		{"1.24.0-beta.1", "1.23.0", false},
		{"1.24.0", "1.24.0-beta.3", false},
	}
	for _, tc := range cases {
		if got := IsNewer(tc.current, tc.candidate); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.current, tc.candidate, got, tc.want)
		}
	}
}
