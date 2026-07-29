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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// discardLogger returns a *slog.Logger that writes nowhere, keeping test
// output free of the package's own Warn-level logging.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// updaterFakeServer serves a GitHub-shaped "latest release" response plus
// its asset and checksums.txt, all three resolvable through one httptest
// server so a real *Checker/*Downloader pair exercises the whole
// Check -> Install pipeline without touching the network. Every field
// that a test needs to mutate mid-flight (gates, checksums, failure mode)
// is behind mu.
type updaterFakeServer struct {
	mu          sync.Mutex
	url         string
	version     string
	payload     []byte
	assetName   string
	checksums   string
	releaseGate chan struct{}
	assetGate   chan struct{}
	failRelease bool
}

func newUpdaterFakeServer(t *testing.T, version string, payload []byte) *updaterFakeServer {
	t.Helper()
	assetName := "openccu-loom-ccu-" + version + ".tar.gz"
	sum := sha256.Sum256(payload)
	s := &updaterFakeServer{
		version:   version,
		payload:   payload,
		assetName: assetName,
		checksums: hex.EncodeToString(sum[:]) + "  " + assetName + "\n",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", s.handleRelease)
	mux.HandleFunc("/asset.tar.gz", s.handleAsset)
	mux.HandleFunc("/checksums.txt", s.handleChecksums)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func (s *updaterFakeServer) handleRelease(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	gate := s.releaseGate
	fail := s.failRelease
	s.mu.Unlock()
	if gate != nil {
		<-gate
	}
	if fail {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	body := fmt.Sprintf(
		`{"tag_name":"v%s","html_url":"https://example.invalid/releases/v%s","assets":[`+
			`{"name":%q,"browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}]}`,
		s.version, s.version, s.assetName, s.url+"/asset.tar.gz", s.url+"/checksums.txt",
	)
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func (s *updaterFakeServer) handleAsset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	gate := s.assetGate
	payload := s.payload
	s.mu.Unlock()
	if gate != nil {
		<-gate
	}
	_, _ = w.Write(payload)
}

func (s *updaterFakeServer) handleChecksums(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	checksums := s.checksums
	s.mu.Unlock()
	_, _ = io.WriteString(w, checksums)
}

func (s *updaterFakeServer) setReleaseGate(ch chan struct{}) {
	s.mu.Lock()
	s.releaseGate = ch
	s.mu.Unlock()
}

func (s *updaterFakeServer) setAssetGate(ch chan struct{}) {
	s.mu.Lock()
	s.assetGate = ch
	s.mu.Unlock()
}

func (s *updaterFakeServer) setChecksums(v string) {
	s.mu.Lock()
	s.checksums = v
	s.mu.Unlock()
}

func (s *updaterFakeServer) setFailRelease(v bool) {
	s.mu.Lock()
	s.failRelease = v
	s.mu.Unlock()
}

// recordingRunner is a fake Installer.Run that records every invocation.
// Its own state is mutex-guarded because InstallAsync/Install's busy
// gating tests invoke it from a goroutine other than the asserting one.
type recordingRunner struct {
	mu    sync.Mutex
	calls int
	args  []string
	err   error
}

func (r *recordingRunner) run(_ context.Context, _ string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.args = append([]string(nil), args...)
	return r.err
}

func (r *recordingRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// newUpdaterForTest wires a real *Checker/*Downloader/*Installer against
// srv plus run, so tests get free integration coverage of the whole
// Check -> Install pipeline instead of stubbing the state machine's
// collaborators.
func newUpdaterForTest(t *testing.T, srv *updaterFakeServer, supported bool, currentVersion string, clk clock.Clock, run Runner) *Updater {
	t.Helper()
	stagePath := filepath.Join(t.TempDir(), "staged.tar.gz")
	if clk == nil {
		clk = clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	}
	return NewUpdater(Deps{
		Capability: CapabilityProbe{
			IsAddonBuild: func() bool { return supported },
			StatInstaller: func(string) (os.FileInfo, error) {
				return fakeFileInfo{mode: 0o755}, nil
			},
		},
		Checker:        &Checker{HTTPClient: &http.Client{}, BaseURL: srv.url},
		Downloader:     &Downloader{HTTPClient: &http.Client{}, StagePath: stagePath},
		Installer:      &Installer{InstallerPath: "/bin/install_addon", TarballPath: stagePath, Run: run},
		Clock:          clk,
		CurrentVersion: currentVersion,
		Logger:         discardLogger(),
	})
}

// waitForState polls u.Status().State until it matches want, bounded by a
// generous real-time timeout. This is test-harness synchronization for a
// genuinely concurrent state transition (a background Check/Install
// goroutine), not a substitute for the fake-clock-driven synchronization
// periodic_test.go uses for timer semantics.
func waitForState(t *testing.T, u *Updater, want State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := u.Status().State; got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for state %v, last state = %v", want, u.Status().State)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestUpdaterUnsupportedPlatform(t *testing.T) {
	t.Parallel()

	srv := newUpdaterFakeServer(t, "2.0.0", []byte("payload"))
	u := newUpdaterForTest(t, srv, false, "1.0.0", nil, (&recordingRunner{}).run)

	if u.Status().Supported {
		t.Fatal("Supported = true, want false")
	}
	if err := u.Check(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Check() error = %v, want ErrUnsupported", err)
	}
	if err := u.InstallAsync(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Errorf("InstallAsync() error = %v, want ErrUnsupported", err)
	}
}

func TestUpdaterCheckSuccess(t *testing.T) {
	t.Parallel()

	srv := newUpdaterFakeServer(t, "2.0.0", []byte("payload"))
	fakeNow := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	clk := clock.NewFake(fakeNow)
	u := newUpdaterForTest(t, srv, true, "1.0.0", clk, (&recordingRunner{}).run)

	if err := u.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	st := u.Status()
	if st.LatestVersion != "2.0.0" {
		t.Errorf("LatestVersion = %q, want 2.0.0", st.LatestVersion)
	}
	if !st.UpdateAvailable {
		t.Error("UpdateAvailable = false, want true")
	}
	if st.State != StateIdle {
		t.Errorf("State = %v, want StateIdle", st.State)
	}
	if !st.LastCheck.Equal(fakeNow) {
		t.Errorf("LastCheck = %v, want %v", st.LastCheck, fakeNow)
	}
	if st.ReleaseURL == "" {
		t.Error("ReleaseURL is empty")
	}
	if rel := u.LastRelease(); rel.Version != "2.0.0" {
		t.Errorf("LastRelease().Version = %q, want 2.0.0", rel.Version)
	}
}

func TestUpdaterCheckAlreadyNewest(t *testing.T) {
	t.Parallel()

	srv := newUpdaterFakeServer(t, "1.0.0", []byte("payload"))
	u := newUpdaterForTest(t, srv, true, "1.0.0", nil, (&recordingRunner{}).run)

	if err := u.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if u.Status().UpdateAvailable {
		t.Error("UpdateAvailable = true, want false")
	}
}

func TestUpdaterCheckFailure(t *testing.T) {
	t.Parallel()

	srv := newUpdaterFakeServer(t, "2.0.0", []byte("payload"))
	srv.setFailRelease(true)
	u := newUpdaterForTest(t, srv, true, "1.0.0", nil, (&recordingRunner{}).run)

	err := u.Check(context.Background())
	if err == nil {
		t.Fatal("Check() error = nil, want non-nil")
	}
	st := u.Status()
	if st.State != StateFailed {
		t.Errorf("State = %v, want StateFailed", st.State)
	}
	if st.Error == "" {
		t.Error("Error is empty, want the failure detail")
	}
}

func TestUpdaterInstallWithoutPriorCheck(t *testing.T) {
	t.Parallel()

	srv := newUpdaterFakeServer(t, "2.0.0", []byte("payload"))
	u := newUpdaterForTest(t, srv, true, "1.0.0", nil, (&recordingRunner{}).run)

	if err := u.Install(context.Background()); !errors.Is(err, ErrNoUpdateAvailable) {
		t.Errorf("Install() error = %v, want ErrNoUpdateAvailable", err)
	}
}

func TestUpdaterInstallAfterCheckFindsNoUpdate(t *testing.T) {
	t.Parallel()

	srv := newUpdaterFakeServer(t, "1.0.0", []byte("payload"))
	u := newUpdaterForTest(t, srv, true, "1.0.0", nil, (&recordingRunner{}).run)

	if err := u.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if err := u.Install(context.Background()); !errors.Is(err, ErrNoUpdateAvailable) {
		t.Errorf("Install() error = %v, want ErrNoUpdateAvailable", err)
	}
}

func TestUpdaterCheckThenInstallHappyPath(t *testing.T) {
	t.Parallel()

	srv := newUpdaterFakeServer(t, "2.0.0", []byte("addon tarball bytes"))
	runner := &recordingRunner{}
	u := newUpdaterForTest(t, srv, true, "1.0.0", nil, runner.run)

	if err := u.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !u.Status().UpdateAvailable {
		t.Fatal("expected UpdateAvailable after Check")
	}
	if err := u.Install(context.Background()); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if st := u.Status(); st.State != StateInstalling {
		t.Errorf("State = %v, want StateInstalling", st.State)
	}
	if got := runner.callCount(); got != 1 {
		t.Errorf("installer Run calls = %d, want 1", got)
	}
}

func TestUpdaterInstallDownloadFailure(t *testing.T) {
	t.Parallel()

	srv := newUpdaterFakeServer(t, "2.0.0", []byte("payload"))
	u := newUpdaterForTest(t, srv, true, "1.0.0", nil, (&recordingRunner{}).run)

	if err := u.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	// Corrupt checksums.txt after the check so Install's download step
	// fails: Check only resolves release metadata, not the asset bytes.
	srv.setChecksums("deadbeef  wrong-file.tar.gz\n")

	err := u.Install(context.Background())
	if !errors.Is(err, ErrChecksumNotFound) {
		t.Fatalf("Install() error = %v, want ErrChecksumNotFound", err)
	}
	if st := u.Status(); st.State != StateFailed {
		t.Errorf("State = %v, want StateFailed", st.State)
	}
}

func TestUpdaterInstallInstallerFailure(t *testing.T) {
	t.Parallel()

	srv := newUpdaterFakeServer(t, "2.0.0", []byte("payload"))
	wantErr := errors.New("addonupdate test: installer exec failed")
	runner := &recordingRunner{err: wantErr}
	u := newUpdaterForTest(t, srv, true, "1.0.0", nil, runner.run)

	if err := u.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	err := u.Install(context.Background())
	if !errors.Is(err, wantErr) {
		t.Errorf("Install() error = %v, want %v", err, wantErr)
	}
	if st := u.Status(); st.State != StateFailed {
		t.Errorf("State = %v, want StateFailed", st.State)
	}
}

func TestUpdaterCheckBusyWhileChecking(t *testing.T) {
	t.Parallel()

	srv := newUpdaterFakeServer(t, "2.0.0", []byte("payload"))
	u := newUpdaterForTest(t, srv, true, "1.0.0", nil, (&recordingRunner{}).run)

	gate := make(chan struct{})
	srv.setReleaseGate(gate)

	errCh := make(chan error, 1)
	go func() { errCh <- u.Check(context.Background()) }()
	waitForState(t, u, StateChecking)

	if err := u.Check(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("second Check() error = %v, want ErrBusy", err)
	}

	close(gate)
	if err := <-errCh; err != nil {
		t.Fatalf("first Check() error = %v", err)
	}
}

func TestUpdaterInstallBusyWhileChecking(t *testing.T) {
	t.Parallel()

	srv := newUpdaterFakeServer(t, "2.0.0", []byte("payload"))
	u := newUpdaterForTest(t, srv, true, "1.0.0", nil, (&recordingRunner{}).run)

	// Establish UpdateAvailable=true via an unblocked check first.
	if err := u.Check(context.Background()); err != nil {
		t.Fatalf("initial Check() error = %v", err)
	}

	gate := make(chan struct{})
	srv.setReleaseGate(gate)
	errCh := make(chan error, 1)
	go func() { errCh <- u.Check(context.Background()) }()
	waitForState(t, u, StateChecking)

	if err := u.Install(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("Install() while checking, error = %v, want ErrBusy", err)
	}

	close(gate)
	if err := <-errCh; err != nil {
		t.Fatalf("Check() error = %v", err)
	}
}

func TestUpdaterCheckBusyWhileInstalling(t *testing.T) {
	t.Parallel()

	srv := newUpdaterFakeServer(t, "2.0.0", []byte("payload"))
	u := newUpdaterForTest(t, srv, true, "1.0.0", nil, (&recordingRunner{}).run)

	if err := u.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	gate := make(chan struct{})
	srv.setAssetGate(gate)
	errCh := make(chan error, 1)
	go func() { errCh <- u.Install(context.Background()) }()
	waitForState(t, u, StateDownloading)

	if err := u.Check(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("Check() while installing, error = %v, want ErrBusy", err)
	}

	close(gate)
	if err := <-errCh; err != nil {
		t.Fatalf("Install() error = %v", err)
	}
}

func TestUpdaterInstallAsync(t *testing.T) {
	t.Parallel()

	srv := newUpdaterFakeServer(t, "2.0.0", []byte("payload"))
	runner := &recordingRunner{}
	u := newUpdaterForTest(t, srv, true, "1.0.0", nil, runner.run)

	if err := u.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if err := u.InstallAsync(context.Background()); err != nil {
		t.Fatalf("InstallAsync() error = %v", err)
	}

	waitForState(t, u, StateInstalling)
	if got := runner.callCount(); got != 1 {
		t.Errorf("installer Run calls = %d, want 1", got)
	}
}

func TestUpdaterOnChange(t *testing.T) {
	t.Parallel()

	srv := newUpdaterFakeServer(t, "2.0.0", []byte("payload"))
	u := newUpdaterForTest(t, srv, true, "1.0.0", nil, (&recordingRunner{}).run)

	var calls1, calls2 int
	var last1 Status
	unsub1 := u.OnChange(func(s Status) { calls1++; last1 = s })
	unsub2 := u.OnChange(func(s Status) { calls2++ })
	defer unsub2()

	if err := u.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if calls1 == 0 {
		t.Error("listener 1 was not called")
	}
	if calls2 == 0 {
		t.Error("listener 2 was not called")
	}
	if last1.State != StateIdle || !last1.UpdateAvailable {
		t.Errorf("listener 1 final snapshot = %+v, want idle + UpdateAvailable", last1)
	}

	unsub1()
	n1Before, n2Before := calls1, calls2

	if err := u.Check(context.Background()); err != nil {
		t.Fatalf("second Check() error = %v", err)
	}
	if calls1 != n1Before {
		t.Errorf("listener 1 fired after unsubscribe: before=%d after=%d", n1Before, calls1)
	}
	if calls2 <= n2Before {
		t.Error("listener 2 did not fire on second Check")
	}
}

func TestUpdaterCurrentVersionOverride(t *testing.T) {
	t.Parallel()

	const override = "9.9.9-custom"
	srv := newUpdaterFakeServer(t, "2.0.0", []byte("payload"))
	u := newUpdaterForTest(t, srv, true, override, nil, (&recordingRunner{}).run)

	if got := u.Status().CurrentVersion; got != override {
		t.Errorf("CurrentVersion = %q, want %q", got, override)
	}
}
