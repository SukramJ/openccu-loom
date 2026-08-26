// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

// Ground-truth checks for the alarm enrollment candidate surfaces
// (internal/alarm/candidates.go) against the godevccu wire stack: the
// HmIP-KRCA keyfob must surface as a remote-key candidate (press
// parameters are ordinary VALUES data points, not generic events),
// the HmIP-ASIR siren must carry its acoustic/optical ENUM label
// lists, and the HmIP-MP3P must offer its soundfile list for the
// chirp class. Unit tests pin the mapping against hand-built
// channels; this file pins it against real device descriptions.
package integration

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/central"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newCandidatesAlarmService boots the central/godevccu stack with the
// three candidate-relevant models and wraps it in an alarm.Service.
// The service is not started — the candidate surfaces only walk the
// central registry's device model.
func newCandidatesAlarmService(t *testing.T) *alarm.Service {
	t.Helper()
	h := newSPAHarness(t, []string{"HmIP-ASIR", "HmIP-KRCA", "HmIP-MP3P"})

	reg := central.NewRegistry()
	if err := reg.Register(h.central); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(t.TempDir(), "openccu-loom.db")))
	if err != nil {
		t.Fatalf("sqlitestore.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc, err := alarm.NewService(alarm.Deps{
		Settings: alarm.Settings{Enabled: true},
		Registry: reg,
		Stores:   alarm.NewStores(db),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("alarm.NewService: %v", err)
	}
	return svc
}

// TestRemoteKeyCandidatesIncludeKeyfobFromWire verifies the KRCA's key
// channels surface as remote-key candidates with both press
// parameters in dispatch order.
func TestRemoteKeyCandidatesIncludeKeyfobFromWire(t *testing.T) {
	svc := newCandidatesAlarmService(t)

	rows := svc.RemoteKeyCandidates()
	var keyfob []alarm.RemoteKeyCandidate
	for _, c := range rows {
		if strings.Contains(c.Model, "KRCA") {
			keyfob = append(keyfob, c)
		}
	}
	if len(keyfob) == 0 {
		t.Fatalf("no HmIP-KRCA remote-key candidates; got %d rows total", len(rows))
	}
	for _, c := range keyfob {
		if !slices.Contains(c.Parameters, string(hmenum.ParameterPressShort)) {
			t.Errorf("KRCA channel %s: PRESS_SHORT missing from %v", c.ChannelAddress, c.Parameters)
		}
	}
	first := keyfob[0]
	if slices.Contains(first.Parameters, string(hmenum.ParameterPressLong)) &&
		first.Parameters[0] != string(hmenum.ParameterPressShort) {
		t.Errorf("dispatch order: want PRESS_SHORT first, got %v", first.Parameters)
	}
}

// TestOutputCandidatesCarryDeviceEnumLists verifies the ASIR exposes
// its acoustic tone and optical pattern ENUM labels and the MP3P its
// soundfile list — the value sources for the SPA dropdowns.
func TestOutputCandidatesCarryDeviceEnumLists(t *testing.T) {
	svc := newCandidatesAlarmService(t)

	acoustic := svc.OutputCandidates(hmenum.AlarmOutputClassAcousticSiren)
	var asir *alarm.OutputCandidate
	for i := range acoustic {
		if strings.Contains(acoustic[i].Model, "ASIR") {
			asir = &acoustic[i]
			break
		}
	}
	if asir == nil {
		t.Fatalf("no HmIP-ASIR acoustic-siren candidate; got %d rows", len(acoustic))
	}
	if len(asir.AvailableTones) == 0 {
		t.Errorf("ASIR %s: AvailableTones empty", asir.ChannelAddress)
	}
	if len(asir.AvailableLights) == 0 {
		t.Errorf("ASIR %s: AvailableLights empty", asir.ChannelAddress)
	}

	chirp := svc.OutputCandidates(hmenum.AlarmOutputClassChirp)
	var mp3p *alarm.OutputCandidate
	for i := range chirp {
		if strings.Contains(chirp[i].Model, "MP3P") {
			mp3p = &chirp[i]
			break
		}
	}
	if mp3p == nil {
		t.Fatalf("no HmIP-MP3P chirp candidate; got %d rows", len(chirp))
	}
	if len(mp3p.AvailableSoundfiles) == 0 {
		t.Errorf("MP3P %s: AvailableSoundfiles empty", mp3p.ChannelAddress)
	}
}
