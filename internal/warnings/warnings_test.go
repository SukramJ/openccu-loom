// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package warnings

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/restapi"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

type fakeHealth struct{ components []health.Component }

func (f fakeHealth) Snapshot() []health.Component { return f.components }

type fakeIncidents struct{ incidents []hmapi.Incident }

func (f fakeIncidents) Incidents() []hmapi.Incident { return f.incidents }

type fakeHubs struct{ hubs []restapi.NamedHub }

func (f fakeHubs) Hubs() []restapi.NamedHub { return f.hubs }

type memSilences struct {
	rows map[string]map[string]time.Time // user → id → until
}

func newMemSilences() *memSilences { return &memSilences{rows: map[string]map[string]time.Time{}} }

func (m *memSilences) Silences(_ context.Context, user string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	maps.Copy(out, m.rows[user])
	return out, nil
}

func (m *memSilences) Set(_ context.Context, user, id string, until, _ time.Time) error {
	if m.rows[user] == nil {
		m.rows[user] = map[string]time.Time{}
	}
	m.rows[user][id] = until
	return nil
}

func (m *memSilences) Delete(_ context.Context, user, id string) error {
	delete(m.rows[user], id)
	return nil
}

func (m *memSilences) DeleteExpired(_ context.Context, now time.Time) error {
	for _, ids := range m.rows {
		for id, until := range ids {
			if !until.After(now) {
				delete(ids, id)
			}
		}
	}
	return nil
}

func (m *memSilences) DeleteOtherThan(_ context.Context, user string, active []string) error {
	keep := map[string]bool{}
	for _, id := range active {
		keep[id] = true
	}
	for id := range m.rows[user] {
		if !keep[id] {
			delete(m.rows[user], id)
		}
	}
	return nil
}

var t0 = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func clock(at time.Time) func() time.Time { return func() time.Time { return at } }

func TestActiveAggregatesAllThreeSources(t *testing.T) {
	h := hub.NewHub("c1")
	a := New(
		fakeHealth{[]health.Component{
			{Name: "mqtt", Status: health.StatusUnhealthy},
			{Name: "rest", Status: health.StatusHealthy},
			{Name: "ccu", Status: health.StatusDegraded},
		}},
		fakeIncidents{[]hmapi.Incident{
			{Component: "xmlrpc", Severity: "error", When: t0.Add(-time.Hour)},
			{Component: "xmlrpc", Severity: "error", When: t0.Add(-2 * time.Hour)},
			{Component: "xmlrpc", Severity: "error", When: t0.Add(-48 * time.Hour)}, // outside window
			{Component: "mqtt", Severity: "info", When: t0.Add(-time.Hour)},         // below grade
		}},
		fakeHubs{[]restapi.NamedHub{{Central: "c1", Hub: h}}},
		nil, clock(t0),
	)
	got := a.Active()

	byID := map[string]Warning{}
	for _, w := range got {
		byID[w.ID] = w
	}
	if w := byID["health:mqtt"]; w.Severity != SeverityError {
		t.Errorf("health:mqtt = %+v", w)
	}
	if w := byID["health:ccu"]; w.Severity != SeverityWarning {
		t.Errorf("health:ccu = %+v", w)
	}
	if _, ok := byID["health:rest"]; ok {
		t.Error("healthy component produced a warning")
	}
	if w := byID["incident:xmlrpc"]; w.Args["count"] != "2" {
		t.Errorf("incident:xmlrpc = %+v (the 48h-old one must not count)", w)
	}
	if _, ok := byID["incident:mqtt"]; ok {
		t.Error("info-grade incident produced a warning")
	}
	// A fresh hub has no service messages, so no servicemsg warning.
	if _, ok := byID["servicemsg:c1"]; ok {
		t.Error("empty service-message list produced a warning")
	}
	// Errors sort before warnings.
	if len(got) > 0 && got[0].Severity != SeverityError {
		t.Errorf("order: first = %+v", got[0])
	}
}

func TestForUserAnnotatesAndPrunesSilences(t *testing.T) {
	silences := newMemSilences()
	active := fakeHealth{[]health.Component{{Name: "mqtt", Status: health.StatusUnhealthy}}}
	a := New(active, nil, nil, silences, clock(t0))

	if err := a.SilenceWarning(context.Background(), "markus", "health:mqtt", 7); err != nil {
		t.Fatal(err)
	}
	// A silence for a warning that is not active is refused.
	if err := a.SilenceWarning(context.Background(), "markus", "health:gone", 7); !errors.Is(err, ErrUnknownWarning) {
		t.Fatalf("silencing an inactive warning: %v", err)
	}
	if err := a.SilenceWarning(context.Background(), "markus", "health:mqtt", 3); !errors.Is(err, ErrBadPeriod) {
		t.Fatalf("bad period: %v", err)
	}

	got, err := a.ForUser(context.Background(), "markus")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Silenced || got[0].SilencedUntil == nil {
		t.Fatalf("got %+v", got)
	}
	// Another user sees the warning unsilenced.
	other, err := a.ForUser(context.Background(), "gast")
	if err != nil {
		t.Fatal(err)
	}
	if other[0].Silenced {
		t.Error("silence leaked across users")
	}

	// The condition clears → the silence is pruned, so a re-occurrence
	// alerts again.
	a.health = fakeHealth{}
	if _, err := a.ForUser(context.Background(), "markus"); err != nil {
		t.Fatal(err)
	}
	a.health = active
	back, err := a.ForUser(context.Background(), "markus")
	if err != nil {
		t.Fatal(err)
	}
	if back[0].Silenced {
		t.Error("silence survived its condition clearing")
	}
}

func TestForUserPrunesExpiredSilences(t *testing.T) {
	silences := newMemSilences()
	a := New(fakeHealth{[]health.Component{{Name: "mqtt", Status: health.StatusUnhealthy}}}, nil, nil, silences, clock(t0))
	if err := a.SilenceWarning(context.Background(), "markus", "health:mqtt", 1); err != nil {
		t.Fatal(err)
	}
	a.now = clock(t0.Add(25 * time.Hour))
	got, err := a.ForUser(context.Background(), "markus")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Silenced {
		t.Error("expired silence still annotates")
	}
}

type fakePairing struct{ n int }

func (f fakePairing) PendingCount() int { return f.n }

func TestPairingSourceFeedsAWarning(t *testing.T) {
	a := New(nil, nil, nil, nil, clock(t0)).WithPairing(fakePairing{n: 2})
	got := a.Active()
	if len(got) != 1 || got[0].ID != "pairing:pending" || got[0].Args["count"] != "2" || got[0].Severity != SeverityWarning {
		t.Fatalf("got %+v", got)
	}
	if got := New(nil, nil, nil, nil, clock(t0)).WithPairing(fakePairing{n: 0}).Active(); len(got) != 0 {
		t.Fatalf("zero pending produced %+v", got)
	}
}

func TestNilSourcesContributeNothing(t *testing.T) {
	a := New(nil, nil, nil, nil, clock(t0))
	if got := a.Active(); len(got) != 0 {
		t.Errorf("nil sources produced %+v", got)
	}
	got, err := a.ForUser(context.Background(), "markus")
	if err != nil || len(got) != 0 {
		t.Errorf("ForUser on nil sources: %v %+v", err, got)
	}
}
