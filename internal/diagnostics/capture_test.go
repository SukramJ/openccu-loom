// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package diagnostics_test

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/diagnostics"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// --------------------------------------------------------------------------
// Fakes
// --------------------------------------------------------------------------

type fakeTee struct {
	attaches int
	detaches int
	current  *hmlog.CaptureSink
}

func (f *fakeTee) Attach(s *hmlog.CaptureSink) { f.attaches++; f.current = s }
func (f *fakeTee) Detach() *hmlog.CaptureSink {
	f.detaches++
	s := f.current
	f.current = nil
	return s
}

type setCall struct {
	Path  string
	Level slog.Level
	TTL   time.Duration
}

type fakeLevels struct {
	sets   []setCall
	resets []string
}

func (f *fakeLevels) Set(p string, l slog.Level, ttl time.Duration) {
	f.sets = append(f.sets, setCall{Path: p, Level: l, TTL: ttl})
}

func (f *fakeLevels) Reset(p string) bool {
	f.resets = append(f.resets, p)
	return true
}

// --------------------------------------------------------------------------
// NewManager
// --------------------------------------------------------------------------

func TestNewManager_NilArgs_NoPanic(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	if mgr == nil {
		t.Fatal("expected non-nil Manager")
	}
}

// --------------------------------------------------------------------------
// Start — duration defaults
// --------------------------------------------------------------------------

func TestStart_ZeroDuration_UsesDefault(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	sum, err := mgr.Start(diagnostics.StartOptions{Duration: 0})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := diagnostics.DefaultCaptureDuration
	got := sum.EndsAt.Sub(sum.StartedAt)
	// Allow a small delta for clock granularity.
	if got < want-time.Second || got > want+time.Second {
		t.Errorf("effective duration = %v, want ~%v", got, want)
	}
}

// --------------------------------------------------------------------------
// Start — anonymise gating
// --------------------------------------------------------------------------

// TestStart_TriggeredOperator_HonoursAnonymiseFalse pins that an operator-
// initiated capture (a non-empty Triggered subject) honours an explicit
// Anonymise=false. The REST/SPA path stamps the authenticated operator into
// Triggered precisely so this choice is not silently overridden.
func TestStart_TriggeredOperator_HonoursAnonymiseFalse(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	sum, err := mgr.Start(diagnostics.StartOptions{Anonymise: false, Triggered: "operator@ccu"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if sum.Anonymised {
		t.Error("operator-triggered capture with Anonymise=false must not be anonymised")
	}
}

// TestStart_Untriggered_ForcesAnonymise pins the safe default: a capture with
// no operator subject (auto/scheduled, or an unauthenticated request whose
// Triggered stays empty) is always anonymised, even when Anonymise=false, so
// raw device addresses never leak on a capture nobody is accountable for.
func TestStart_Untriggered_ForcesAnonymise(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	sum, err := mgr.Start(diagnostics.StartOptions{Anonymise: false, Triggered: ""})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !sum.Anonymised {
		t.Error("untriggered capture must be anonymised regardless of Anonymise=false")
	}
}

// --------------------------------------------------------------------------
// Start — duration cap
// --------------------------------------------------------------------------

func TestStart_TooLong_ReturnsErrCaptureDurationTooLong(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	_, err := mgr.Start(diagnostics.StartOptions{Duration: diagnostics.MaxCaptureDuration + time.Second})
	if !errors.Is(err, diagnostics.ErrCaptureDurationTooLong) {
		t.Errorf("error = %v, want ErrCaptureDurationTooLong", err)
	}
}

// --------------------------------------------------------------------------
// Start — busy
// --------------------------------------------------------------------------

func TestStart_Twice_ReturnsErrCaptureBusy(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	if _, err := mgr.Start(diagnostics.StartOptions{}); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	_, err := mgr.Start(diagnostics.StartOptions{})
	if !errors.Is(err, diagnostics.ErrCaptureBusy) {
		t.Errorf("second Start error = %v, want ErrCaptureBusy", err)
	}
}

// --------------------------------------------------------------------------
// Stop — no active capture
// --------------------------------------------------------------------------

func TestStop_NoActive_ReturnsErrCaptureNotActive(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	_, err := mgr.Stop("")
	if !errors.Is(err, diagnostics.ErrCaptureNotActive) {
		t.Errorf("Stop with no active: error = %v, want ErrCaptureNotActive", err)
	}
}

// --------------------------------------------------------------------------
// Stop — wrong ID
// --------------------------------------------------------------------------

func TestStop_WrongID_ReturnsErrCaptureNotFound(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	if _, err := mgr.Start(diagnostics.StartOptions{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, err := mgr.Stop("cap_doesnotexist")
	if !errors.Is(err, diagnostics.ErrCaptureNotFound) {
		t.Errorf("Stop(wrong id): error = %v, want ErrCaptureNotFound", err)
	}
}

// --------------------------------------------------------------------------
// Stop — finalises and detaches Tee
// --------------------------------------------------------------------------

func TestStop_Finalise_DetachesTee(t *testing.T) {
	t.Parallel()
	tee := &fakeTee{}
	mgr := diagnostics.NewManager(tee, nil)

	sum, err := mgr.Start(diagnostics.StartOptions{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if tee.attaches != 1 {
		t.Errorf("attaches after Start = %d, want 1", tee.attaches)
	}

	if _, err := mgr.Stop(sum.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if tee.detaches != 1 {
		t.Errorf("detaches after Stop = %d, want 1", tee.detaches)
	}
}

func TestStop_Finalise_StatusStopped(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	sum, err := mgr.Start(diagnostics.StartOptions{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stopped, err := mgr.Stop(sum.ID)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stopped.Status != diagnostics.StatusStopped {
		t.Errorf("status = %q, want stopped", stopped.Status)
	}
}

// --------------------------------------------------------------------------
// LogLevelOverrides — set on Start, reset on Stop
// --------------------------------------------------------------------------

func TestLogLevelOverrides_SetAndReset(t *testing.T) {
	t.Parallel()
	levels := &fakeLevels{}
	mgr := diagnostics.NewManager(nil, levels)

	dur := 10 * time.Minute
	sum, err := mgr.Start(diagnostics.StartOptions{
		Duration:          dur,
		LogLevelOverrides: map[string]string{"openccu-loom.client": "debug"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(levels.sets) == 0 {
		t.Error("expected Set call for log-level override on Start")
	}
	if levels.sets[0].Path != "openccu-loom.client" {
		t.Errorf("set path = %q, want openccu-loom.client", levels.sets[0].Path)
	}
	if levels.sets[0].TTL != dur {
		t.Errorf("set TTL = %v, want %v", levels.sets[0].TTL, dur)
	}

	if _, err := mgr.Stop(sum.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(levels.resets) == 0 {
		t.Error("expected Reset call for log-level override on Stop")
	}
}

// --------------------------------------------------------------------------
// List — active + archived, sorted by StartedAt desc
// --------------------------------------------------------------------------

func TestList_ActiveAndArchived(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr := diagnostics.NewManager(nil, nil, diagnostics.WithClock(func() time.Time { return now }))

	s1, err := mgr.Start(diagnostics.StartOptions{})
	if err != nil {
		t.Fatalf("Start 1: %v", err)
	}
	if _, err := mgr.Stop(s1.ID); err != nil {
		t.Fatalf("Stop 1: %v", err)
	}

	now = now.Add(time.Minute)

	if _, err := mgr.Start(diagnostics.StartOptions{}); err != nil {
		t.Fatalf("Start 2: %v", err)
	}

	list := mgr.List()
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}
	// Most recent first.
	if !list[0].StartedAt.After(list[1].StartedAt) {
		t.Errorf("list not sorted by StartedAt desc: [0]=%v [1]=%v", list[0].StartedAt, list[1].StartedAt)
	}
}

// --------------------------------------------------------------------------
// OpenArchive — active → ErrCaptureNotActive
// --------------------------------------------------------------------------

func TestOpenArchive_Active_ReturnsErrCaptureNotActive(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	sum, err := mgr.Start(diagnostics.StartOptions{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, err = mgr.OpenArchive(sum.ID)
	if !errors.Is(err, diagnostics.ErrCaptureNotActive) {
		t.Errorf("OpenArchive(active): error = %v, want ErrCaptureNotActive", err)
	}
}

// --------------------------------------------------------------------------
// OpenArchive — stopped → tar.gz bytes, non-empty
// --------------------------------------------------------------------------

func TestOpenArchive_Stopped_ReturnsTarGz(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	sum, err := mgr.Start(diagnostics.StartOptions{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := mgr.Stop(sum.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	data, err := mgr.OpenArchive(sum.ID)
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty archive bytes")
	}
}

// --------------------------------------------------------------------------
// Sweep — expiry transition
// --------------------------------------------------------------------------

func TestSweep_Expiry_SetsStatusExpired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr := diagnostics.NewManager(nil, nil, diagnostics.WithClock(func() time.Time { return now }))

	sum, err := mgr.Start(diagnostics.StartOptions{Duration: time.Minute})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if sum.Status != diagnostics.StatusRunning {
		t.Fatalf("status after Start = %q, want running", sum.Status)
	}

	// Advance past EndsAt.
	now = now.Add(2 * time.Minute)
	mgr.Sweep()

	list := mgr.List()
	if len(list) == 0 {
		t.Fatal("list empty after Sweep")
	}
	found := false
	for _, s := range list {
		if s.ID == sum.ID {
			found = true
			if s.Status != diagnostics.StatusExpired {
				t.Errorf("status = %q, want expired", s.Status)
			}
		}
	}
	if !found {
		t.Error("capture not found in list after expiry sweep")
	}
}

// TestCaptureExpiresWithoutAnExternalSweep pins that a capture the operator
// never stops finalises itself.
//
// Nothing outside this package polls the manager, so expiry has to be
// self-driven: an unstopped capture otherwise kept the log tee attached for
// the daemon's whole life, never built the archive the operator asked for, and
// answered 409 to every later Start until a restart.
func TestCaptureExpiresWithoutAnExternalSweep(t *testing.T) {
	t.Parallel()
	tee := &fakeTee{}
	mgr := diagnostics.NewManager(tee, nil)

	sum, err := mgr.Start(diagnostics.StartOptions{Duration: 20 * time.Millisecond, Triggered: "operator"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := mgr.Get(sum.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status == diagnostics.StatusExpired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("capture status = %q after its window elapsed, want expired", got.Status)
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Reads below are ordered after the finalise by mgr.Get's lock.
	if tee.detaches != 1 {
		t.Errorf("tee detaches = %d, want 1 — the log tee stays attached to a dead capture", tee.detaches)
	}
	if _, err := mgr.OpenArchive(sum.ID); err != nil {
		t.Errorf("OpenArchive after expiry: %v", err)
	}
	if _, err := mgr.Start(diagnostics.StartOptions{Duration: time.Minute}); err != nil {
		t.Errorf("Start after an expired capture: %v", err)
	}
}

// --------------------------------------------------------------------------
// Sweep — ArchiveRetention eviction
// --------------------------------------------------------------------------

func TestSweep_ArchiveRetention_RemovesOldEntries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr := diagnostics.NewManager(nil, nil, diagnostics.WithClock(func() time.Time { return now }))

	sum, err := mgr.Start(diagnostics.StartOptions{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := mgr.Stop(sum.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Advance past ArchiveRetention.
	now = now.Add(diagnostics.ArchiveRetention + time.Minute)
	mgr.Sweep()

	list := mgr.List()
	for _, s := range list {
		if s.ID == sum.ID {
			t.Errorf("archived capture still present after ArchiveRetention elapsed")
		}
	}
}

// --------------------------------------------------------------------------
// Archive FIFO — more than MaxArchivedCaptures stops evicts oldest
// --------------------------------------------------------------------------

func TestArchiveFIFO_OverMax_EvictsOldest(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr := diagnostics.NewManager(nil, nil, diagnostics.WithClock(func() time.Time { return now }))

	var firstID string
	for i := 0; i <= diagnostics.MaxArchivedCaptures; i++ {
		sum, err := mgr.Start(diagnostics.StartOptions{})
		if err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
		if i == 0 {
			firstID = sum.ID
		}
		if _, err := mgr.Stop(sum.ID); err != nil {
			t.Fatalf("Stop %d: %v", i, err)
		}
		now = now.Add(time.Second)
	}

	list := mgr.List()
	if len(list) > diagnostics.MaxArchivedCaptures {
		t.Errorf("archived list len = %d, exceeds MaxArchivedCaptures %d", len(list), diagnostics.MaxArchivedCaptures)
	}
	for _, s := range list {
		if s.ID == firstID {
			t.Error("oldest capture still present after FIFO eviction")
		}
	}
}
