// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/warnings"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// trackerHealth is the health source of a warning aggregator over one
// tracker.
type trackerHealth struct{ t *health.Tracker }

func (h trackerHealth) Snapshot() []health.Component { return h.t.Snapshot() }

// boxNoticeFixture starts the notice mirror through startBoxNotices on a
// host staged as the openccu-lite add-on, against a fake box.
type boxNoticeFixture struct {
	fake    *litefake.Fake
	tracker *health.Tracker
	reg     *central.Registry
	mirror  *boxNotices
}

func startBoxNoticeFixture(t *testing.T, cfg *config.Config) *boxNoticeFixture {
	t.Helper()
	liteAddonStampForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	fake, err := litefake.Start(ctx, litefake.Options{})
	cancel()
	if err != nil {
		t.Fatalf("litefake.Start: %v", err)
	}
	t.Cleanup(func() { _ = fake.Close() })
	control := filepath.Join(t.TempDir(), "openccu-loom")
	if err := os.WriteFile(control, []byte(litefake.DefaultAddonControlToken+"\n"), 0o600); err != nil {
		t.Fatalf("write control token: %v", err)
	}
	t.Setenv(liteAddonControlTokenPathEnv, control)
	t.Setenv(liteAddonBaseURLEnv, fake.URL())

	catalogs, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("i18n.NewCatalogs: %v", err)
	}
	fx := &boxNoticeFixture{fake: fake, tracker: health.NewTracker(), reg: central.NewRegistry()}
	fx.mirror = startBoxNotices(t.Context(), cfg, boxNoticeDeps{
		warnings: warnings.New(trackerHealth{fx.tracker}, nil, nil, nil),
		catalogs: catalogs, health: fx.tracker, reg: fx.reg,
	}, slog.New(slog.DiscardHandler))
	t.Cleanup(fx.mirror.Stop)
	if fx.mirror != nil {
		// The mirror reconciles once when it starts. A test that changed a
		// source before that would see the start's work, not an event's.
		waitBoxNotices(t, "the mirror's first reconciliation", func() bool { return fx.noticeReads() > 0 })
	}
	return fx
}

// noticeReads counts the mirror's reads of the box's notice list: one per
// reconciliation.
func (fx *boxNoticeFixture) noticeReads() int {
	n := 0
	calls := fx.fake.Calls()
	for i := range calls {
		if calls[i].Method == http.MethodGet && calls[i].Path == "/api/system/v1/addon-notices" {
			n++
		}
	}
	return n
}

func (fx *boxNoticeFixture) titles() string {
	notices := fx.fake.AddonNotices()
	out := make([]string, 0, len(notices))
	for i := range notices {
		out = append(out, notices[i].ID+"="+notices[i].Title.EN+"|"+notices[i].Title.DE)
	}
	return strings.Join(out, ";")
}

func waitBoxNotices(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestBoxNoticesFollowTheHealthTracker pins the mirror's wiring through
// the composition root's own entry point: a component the daemon's health
// tracker reports unhealthy stands on the box's Status page, worded in
// both languages from the daemon's catalogue, and goes when the component
// recovers. The resynchronisation is minutes away, so each step is the
// tracker's change hook at work.
func TestBoxNoticesFollowTheHealthTracker(t *testing.T) {
	fx := startBoxNoticeFixture(t, &config.Config{})
	if fx.mirror == nil {
		t.Fatal("startBoxNotices returned no mirror on an openccu-lite add-on host with the switch unset")
	}
	fx.tracker.RecordUnhealthy("mqtt", health.Sample{Note: "broker unreachable"})
	waitBoxNotices(t, "the notice for the unhealthy component", func() bool { return len(fx.fake.AddonNotices()) == 1 })
	if got, want := fx.titles(), "health.mqtt=mqtt is unhealthy.|mqtt ist nicht funktionsfähig."; got != want {
		t.Errorf("box holds %q, want %q", got, want)
	}
	if got := fx.fake.AddonNotices()[0].Severity; got != "error" {
		t.Errorf("severity = %q, want error", got)
	}

	fx.tracker.Record("mqtt", health.Sample{Healthy: true})
	waitBoxNotices(t, "the notice to go once the component recovered", func() bool { return len(fx.fake.AddonNotices()) == 0 })
}

// TestBoxNoticesFollowACentralRegisteredLater pins the per-central seam:
// a central that joins the registry after the mirror started is watched
// too — its health and its recorded incidents reach the mirror.
func TestBoxNoticesFollowACentralRegisteredLater(t *testing.T) {
	fx := startBoxNoticeFixture(t, &config.Config{})
	if fx.mirror == nil {
		t.Fatal("no mirror")
	}
	unit, err := central.New(central.Config{Name: "later"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := fx.reg.Register(unit); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// The aggregator in this fixture reads the daemon tracker alone, so
	// the central's own change has to be what makes the mirror look.
	puts := fx.fake.AddonNoticePuts()
	fx.tracker.RecordUnhealthy("matter", health.Sample{})
	waitBoxNotices(t, "the first notice", func() bool { return fx.fake.AddonNoticePuts() > puts })

	fx.fake.DropAddonNotices() // the box lost it; only a reconciliation puts it back
	unit.EventBus.Publish(hmevent.IncidentRecordedEvent{})
	waitBoxNotices(t, "the notice to be restored after the central recorded an incident", func() bool {
		return len(fx.fake.AddonNotices()) == 1
	})

	fx.fake.DropAddonNotices()
	unit.Health.RecordUnhealthy("iface", health.Sample{})
	waitBoxNotices(t, "the notice to be restored after the central's health moved", func() bool {
		return len(fx.fake.AddonNotices()) == 1
	})
}

// TestBoxNoticesAreOffWhereThereIsNoBox pins the switch: explicitly off
// on the add-on, and inert on a host that is not the add-on whatever the
// switch says.
func TestBoxNoticesAreOffWhereThereIsNoBox(t *testing.T) {
	off, on := false, true
	t.Run("switched off on the add-on", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.North.UI.BoxNotices = &off
		if fx := startBoxNoticeFixture(t, cfg); fx.mirror != nil {
			t.Error("a mirror runs although north.ui.box_notices is false")
		}
	})
	t.Run("switched on off the add-on", func(t *testing.T) {
		t.Setenv(liteAddonVersionPathEnv, filepath.Join(t.TempDir(), "absent"))
		t.Setenv(liteAddonTokenPathEnv, filepath.Join(t.TempDir(), "absent"))
		catalogs, err := i18n.NewCatalogs()
		if err != nil {
			t.Fatal(err)
		}
		cfg := &config.Config{}
		cfg.North.UI.BoxNotices = &on
		m := startBoxNotices(t.Context(), cfg, boxNoticeDeps{
			warnings: warnings.New(nil, nil, nil, nil), catalogs: catalogs,
		}, slog.New(slog.DiscardHandler))
		if m != nil {
			m.Stop()
			t.Error("a mirror runs on a host that is not the openccu-lite add-on")
		}
	})
}

// TestBoxNoticeTextsCoverEveryWarning pins the server-side catalogue: each
// sentence a warning can carry exists in German and English, with its
// arguments filled in. A missing entry would post the bare key to the
// box.
func TestBoxNoticeTextsCoverEveryWarning(t *testing.T) {
	catalogs, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	texts := boxNoticeTexts{catalogs: catalogs}
	for _, w := range []warnings.Warning{
		{MessageKey: "warnings.health.unhealthy", Args: map[string]string{"component": "mqtt"}},
		{MessageKey: "warnings.health.degraded", Args: map[string]string{"component": "mqtt"}},
		{MessageKey: "warnings.incidents", Args: map[string]string{"component": "mqtt", "count": "3"}},
		{MessageKey: "warnings.service_messages", Args: map[string]string{"central": "attic", "count": "3"}},
		{MessageKey: "warnings.pairing_pending", Args: map[string]string{"count": "3"}},
	} {
		got := texts.Warning(w)
		for lang, s := range map[string]string{"de": got.DE, "en": got.EN} {
			if s == "" || strings.Contains(s, "box_notice.") || strings.Contains(s, "{") {
				t.Errorf("%s in %s renders %q: a missing entry or an unfilled argument", w.MessageKey, lang, s)
			}
		}
		if got.DE == got.EN {
			t.Errorf("%s renders the same sentence in both languages: %q", w.MessageKey, got.DE)
		}
	}
	if more := texts.More(4); !strings.Contains(more.EN, "4") || !strings.Contains(more.DE, "4") || more.DE == more.EN {
		t.Errorf("the summary sentence renders %+v", more)
	}
}
