// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package addonupdate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// newHeaderCheckerServer answers every request with status and the given
// response headers, and returns a Checker pointed at it.
func newHeaderCheckerServer(t *testing.T, status int, header map[string]string) *Checker {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	t.Cleanup(srv.Close)
	return &Checker{HTTPClient: &http.Client{}, BaseURL: srv.URL}
}

func TestCheckerLatestReleaseReportsAnExhaustedRateLimit(t *testing.T) {
	t.Parallel()
	reset := time.Date(2026, 11, 22, 19, 42, 0, 0, time.UTC)
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		checker := newHeaderCheckerServer(t, status, map[string]string{
			"X-RateLimit-Remaining": "0",
			"X-RateLimit-Reset":     strconv.FormatInt(reset.Unix(), 10),
		})
		_, err := checker.LatestRelease(context.Background(), false)
		if !errors.Is(err, ErrRateLimited) {
			t.Fatalf("status %d: err = %v, want ErrRateLimited", status, err)
		}
		var rl *RateLimitError
		if !errors.As(err, &rl) || !rl.RetryAt.Equal(reset) {
			t.Errorf("status %d: RetryAt = %v, want %v", status, rl, reset)
		}
	}
}

func TestCheckerLatestReleaseTakesRetryAfterForASecondaryLimit(t *testing.T) {
	t.Parallel()
	checker := newHeaderCheckerServer(t, http.StatusForbidden, map[string]string{"Retry-After": "60"})
	before := time.Now()
	_, err := checker.LatestRelease(context.Background(), false)
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want a RateLimitError", err)
	}
	if rl.RetryAt.Before(before.Add(59*time.Second)) || rl.RetryAt.After(time.Now().Add(61*time.Second)) {
		t.Errorf("RetryAt = %v, want about a minute from now", rl.RetryAt)
	}
}

// A 403 without any rate-limit header is an ordinary refusal: it must not
// be reported as a limit that clears by itself.
func TestCheckerLatestReleaseKeepsAPlain403Generic(t *testing.T) {
	t.Parallel()
	for name, header := range map[string]map[string]string{
		"no headers":         nil,
		"remaining not zero": {"X-RateLimit-Remaining": "12", "X-RateLimit-Reset": "1795000000"},
	} {
		checker := newHeaderCheckerServer(t, http.StatusForbidden, header)
		_, err := checker.LatestRelease(context.Background(), false)
		if err == nil || errors.Is(err, ErrRateLimited) {
			t.Errorf("%s: err = %v, want a generic failure", name, err)
		}
	}
}

// TestUpdaterCheckRecordsTheRateLimitInItsStatus pins that a check refused
// by the limit leaves a machine-readable code and the retry time on the
// status the REST/WS/MCP planes publish, and that the next successful check
// clears both.
func TestUpdaterCheckRecordsTheRateLimitInItsStatus(t *testing.T) {
	t.Parallel()
	reset := time.Date(2026, 11, 22, 19, 42, 0, 0, time.UTC)
	var limited atomic.Bool
	limited.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if limited.Load() {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	stagePath := filepath.Join(t.TempDir(), "staged.tar.gz")
	u := NewUpdater(Deps{
		Capability: CapabilityProbe{
			IsAddonBuild:  func() bool { return true },
			StatInstaller: func(string) (os.FileInfo, error) { return fakeFileInfo{mode: 0o755}, nil },
		},
		Checker:        &Checker{HTTPClient: &http.Client{}, BaseURL: srv.URL},
		Downloader:     &Downloader{HTTPClient: &http.Client{}, StagePath: stagePath},
		Installer:      &Installer{InstallerPath: "/bin/install_addon", TarballPath: stagePath, Run: (&recordingRunner{}).run},
		CurrentVersion: "1.0.0",
		Logger:         discardLogger(),
	})

	if err := u.Check(context.Background()); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Check() = %v, want ErrRateLimited", err)
	}
	st := u.Status()
	if st.State != StateFailed || st.ErrorCode != ErrorCodeRateLimited || !st.RetryAt.Equal(reset) {
		t.Fatalf("status = state %q code %q retry %v, want failed/%q/%v", st.State, st.ErrorCode, st.RetryAt, ErrorCodeRateLimited, reset)
	}

	limited.Store(false)
	// An empty release list fails with ErrNoRelease: a different failure,
	// which must not keep the rate-limit code of the previous one.
	_ = u.Check(context.Background())
	st = u.Status()
	if st.ErrorCode != "" || !st.RetryAt.IsZero() {
		t.Errorf("after a later check: code %q retry %v, want both cleared", st.ErrorCode, st.RetryAt)
	}
}
