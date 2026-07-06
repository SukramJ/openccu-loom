// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type stubUpdater struct {
	called atomic.Int32
	err    error
}

func (s *stubUpdater) UpdateFirmware(_ context.Context, _ string) error {
	s.called.Add(1)
	return s.err
}

type stubRefresher struct {
	called atomic.Int32
	err    error
}

func (s *stubRefresher) RefreshFirmwareData(_ context.Context, _ string) error {
	s.called.Add(1)
	return s.err
}

func TestNewUpdateAutoCreatedForUpdatable(t *testing.T) {
	d := newTestDevice(t)
	if d.Update() == nil {
		t.Fatal("Update entity should be auto-created for updatable device")
	}
}

func TestUpdateNilForNonUpdatable(t *testing.T) {
	d := New(Config{Address: "0001", Interface: hmenum.InterfaceHmIPRF, Updatable: false})
	if d.Update() != nil {
		t.Fatal("non-updatable device should not have Update")
	}
}

func TestUpdateAttachAndStart(t *testing.T) {
	d := newTestDevice(t)
	upd := &stubUpdater{}
	refresh := &stubRefresher{}
	d.AttachUpdate(upd, refresh)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done, err := d.Update().Start(ctx, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("refresh worker never finished")
	}
	if upd.called.Load() != 1 {
		t.Fatalf("updater called=%d", upd.called.Load())
	}
	if refresh.called.Load() != 2 {
		t.Fatalf("refresher called=%d", refresh.called.Load())
	}
}

// TestUpdateStartLogsRefreshFirmwareDataError verifies that an error
// returned by the background refresh worker's RefreshFirmwareData call is
// surfaced via slog instead of being silently discarded.
//
// Intentionally NOT t.Parallel(): the test mutates slog.Default() globally.
func TestUpdateStartLogsRefreshFirmwareDataError(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	logger := slog.New(handler)
	old := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(old)

	d := newTestDevice(t)
	boom := errors.New("refresh boom")
	refresh := &stubRefresher{err: boom}
	d.AttachUpdate(&stubUpdater{}, refresh)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done, err := d.Update().Start(ctx, []time.Duration{10 * time.Millisecond})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("refresh worker never finished")
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "refresh boom") {
		t.Errorf("slog output missing the refresher error: %q", logOutput)
	}
	if !strings.Contains(logOutput, d.Address) {
		t.Errorf("slog output missing the device address: %q", logOutput)
	}
}

func TestUpdateStartWithoutUpdaterErrs(t *testing.T) {
	d := newTestDevice(t)
	if _, err := d.Update().Start(context.Background(), nil); err == nil {
		t.Fatal("missing updater must error")
	}
}

func TestUpdateStartPropagatesError(t *testing.T) {
	d := newTestDevice(t)
	boom := errors.New("boom")
	d.AttachUpdate(&stubUpdater{err: boom}, nil)
	if _, err := d.Update().Start(context.Background(), nil); !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}
}

func TestUpdateLatestFirmwareGatedByState(t *testing.T) {
	d := newTestDevice(t)
	d.Firmware().Set(FirmwareInfo{
		Current:     "1.0",
		Available:   "2.0",
		UpdateState: hmenum.DeviceFirmwareStateNewFirmwareAvailable,
	})
	// HmIP-RF + NewFirmwareAvailable → gate says "not ready" → current.
	if got := d.Update().LatestFirmware(); got != "1.0" {
		t.Fatalf("gate should return current, got %s", got)
	}
	d.Firmware().Set(FirmwareInfo{
		Current:     "1.0",
		Available:   "2.0",
		UpdateState: hmenum.DeviceFirmwareStateReadyForUpdate,
	})
	if got := d.Update().LatestFirmware(); got != "2.0" {
		t.Fatalf("ready state should surface available, got %s", got)
	}
}

func TestUpdateInProgressOnlyForHmIPRF(t *testing.T) {
	d := newTestDevice(t)
	d.Firmware().Set(FirmwareInfo{Current: "1.0", UpdateState: hmenum.DeviceFirmwareStatePerformingUpdate})
	if !d.Update().InProgress() {
		t.Fatal("HmIP-RF performing_update → InProgress")
	}

	bcwired := New(Config{Address: "0001", Interface: hmenum.InterfaceBidCosWired, Updatable: true})
	bcwired.Firmware().Set(FirmwareInfo{UpdateState: hmenum.DeviceFirmwareStatePerformingUpdate})
	if bcwired.Update().InProgress() {
		t.Fatal("non-HmIP-RF: InProgress always false")
	}
	if got := bcwired.Update().LatestFirmware(); got != "" {
		t.Fatalf("empty available → empty latest, got %s", got)
	}
	bcwired.Firmware().Set(FirmwareInfo{Current: "1.0", Available: "2.0"})
	if got := bcwired.Update().LatestFirmware(); got != "2.0" {
		t.Fatalf("BidCos wire should expose available unconditionally, got %s", got)
	}
}

func TestDeviceAvailabilityConvenience(t *testing.T) {
	d := newTestDevice(t)
	if !d.Available() {
		t.Fatal("default available")
	}
	if changed := d.SetForcedAvailability(hmenum.ForcedDeviceAvailabilityForceFalse); !changed {
		t.Fatal("should flip")
	}
	if d.Available() {
		t.Fatal("forced false")
	}
	info := d.AvailabilityInfo()
	if info.IsReachable {
		t.Fatal("info should mirror IsReachable")
	}
}
