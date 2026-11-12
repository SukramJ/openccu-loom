// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// Package boxnotice mirrors the daemon's operator warnings onto the
// openccu-lite box the daemon runs on, as the add-on's notices on the
// box's Status page (ADR 0090).
//
// The mirror owns the add-on's notice set on the box. It computes the
// notices the active warnings call for, reads what the box holds, and
// writes the difference. It is told when a warning source changed and
// reconciles then; a slow resynchronisation covers what no event
// announces — an incident leaving its window, a component going stale, a
// box whose service restarted.
package boxnotice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/warnings"
)

const (
	// DefaultResync is how often the mirror reconciles without being
	// told to.
	DefaultResync = 5 * time.Minute
	// maxNoticeID is the longest notice id the box takes.
	maxNoticeID = 64
	// overflowID names the notice that stands for the warnings the box
	// has no room for.
	overflowID = "more"
	// settle lets a burst of changes arrive before one reconciliation
	// answers all of them.
	settle = 200 * time.Millisecond
)

// WarningSource is the warning list the mirror follows.
type WarningSource interface {
	Active() []warnings.Warning
}

// Box is the part of the box's API the mirror uses. Each call carries the
// add-on's control token.
type Box interface {
	AddonNotices(ctx context.Context, controlToken string) (occulited.NoticeList, error)
	PutAddonNotice(ctx context.Context, controlToken string, n occulited.Notice) error
	DeleteAddonNotice(ctx context.Context, controlToken, id string) error
}

// Texts renders what the notices say, in German and English.
type Texts interface {
	// Warning is the sentence of one warning.
	Warning(w warnings.Warning) occulited.NoticeText
	// More is the sentence that stands for count further warnings.
	More(count int) occulited.NoticeText
}

// Config wires a [Mirror].
type Config struct {
	Warnings WarningSource
	Box      Box
	Texts    Texts
	// Token reads the add-on's control token. It is read for every
	// reconciliation: the box mints a new one whenever its service
	// starts.
	Token func() (string, error)
	// Skip leaves a warning off the box; nil mirrors every warning.
	Skip func(w warnings.Warning) bool
	// Resync is the interval of the reconciliation nobody asked for;
	// zero means [DefaultResync].
	Resync time.Duration
	Logger *slog.Logger
}

// Mirror keeps the box's notices equal to the daemon's warnings.
type Mirror struct {
	cfg   Config
	nudge chan struct{}

	mu sync.Mutex
	// failing remembers that the last reconciliation failed, so a box
	// that stays away is logged once, not at every attempt.
	failing bool
}

// New builds a mirror. It does nothing until [Mirror.Run].
func New(cfg Config) *Mirror {
	if cfg.Resync <= 0 {
		cfg.Resync = DefaultResync
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Mirror{cfg: cfg, nudge: make(chan struct{}, 1)}
}

// Nudge says that a warning source changed. It never blocks: any number
// of nudges before the next reconciliation ask for exactly one.
func (m *Mirror) Nudge() {
	select {
	case m.nudge <- struct{}{}:
	default:
	}
}

// Run reconciles once, then on every nudge and at every resynchronisation
// interval, until ctx ends. It returns when ctx is done.
func (m *Mirror) Run(ctx context.Context) {
	ticker := time.NewTicker(m.cfg.Resync)
	defer ticker.Stop()
	for {
		m.reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-m.nudge:
			// Let the rest of a burst arrive; whatever comes later than
			// this leaves its own nudge behind.
			select {
			case <-ctx.Done():
				return
			case <-time.After(settle):
			}
		}
	}
}

// reconcile brings the box's notices in line with the active warnings.
// A failure is logged when it starts and when it ends, and otherwise
// left to the next attempt.
func (m *Mirror) reconcile(ctx context.Context) {
	err := m.reconcileOnce(ctx)
	if ctx.Err() != nil {
		return
	}
	m.mu.Lock()
	was := m.failing
	m.failing = err != nil
	m.mu.Unlock()
	switch {
	case err != nil && !was:
		m.cfg.Logger.Warn("boxnotice.reconcile_failed — the box's Status page may not show the daemon's warnings",
			slog.String("err", err.Error()))
	case err == nil && was:
		m.cfg.Logger.Info("boxnotice.reconcile_recovered")
	}
}

func (m *Mirror) reconcileOnce(ctx context.Context) error {
	token, err := m.cfg.Token()
	if err != nil {
		return err
	}
	have, err := m.cfg.Box.AddonNotices(ctx, token)
	if err != nil {
		return err
	}
	want := m.desired(have.Max)
	held := make(map[string]occulited.Notice, len(have.Notices))
	for _, n := range have.Notices {
		held[n.ID] = n
	}
	var errs []error
	// Clear first: the box holds a fixed number, and a new notice may
	// need the room.
	for id := range held {
		if _, wanted := want[id]; !wanted {
			if err := m.cfg.Box.DeleteAddonNotice(ctx, token, id); err != nil {
				errs = append(errs, err)
			}
		}
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		n := want[id]
		if h, ok := held[id]; ok && sameNotice(h, n) {
			continue
		}
		if err := m.cfg.Box.PutAddonNotice(ctx, token, n); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sameNotice reports whether the box already holds n as it should be.
func sameNotice(held, n occulited.Notice) bool {
	if held.Severity != n.Severity || held.Title != n.Title || held.Link != n.Link {
		return false
	}
	switch {
	case held.Text == nil && n.Text == nil:
		return true
	case held.Text == nil || n.Text == nil:
		return false
	}
	return *held.Text == *n.Text
}

// desired is the notice set the active warnings call for, at most limit
// notices: errors before warnings, and one notice that counts whatever
// did not fit. A limit below one is read as no limit the box announced.
func (m *Mirror) desired(limit int) map[string]occulited.Notice {
	var active []warnings.Warning
	for _, w := range m.cfg.Warnings.Active() {
		if m.cfg.Skip != nil && m.cfg.Skip(w) {
			continue
		}
		active = append(active, w)
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].Severity != active[j].Severity {
			return active[i].Severity == warnings.SeverityError
		}
		return active[i].ID < active[j].ID
	})
	shown := active
	if limit >= 1 && len(active) > limit {
		shown = active[:limit-1]
	}
	out := make(map[string]occulited.Notice, len(shown)+1)
	for _, w := range shown {
		id := NoticeID(w.ID)
		if _, taken := out[id]; taken || id == overflowID {
			id = disambiguate(id, w.ID)
		}
		out[id] = occulited.Notice{ID: id, Severity: severity(w.Severity), Title: m.cfg.Texts.Warning(w)}
	}
	if rest := len(active) - len(shown); rest > 0 {
		sev := occulited.NoticeWarning
		for _, w := range active[len(shown):] {
			if w.Severity == warnings.SeverityError {
				sev = occulited.NoticeError
			}
		}
		out[overflowID] = occulited.Notice{ID: overflowID, Severity: sev, Title: m.cfg.Texts.More(rest)}
	}
	return out
}

func severity(s warnings.Severity) string {
	if s == warnings.SeverityError {
		return occulited.NoticeError
	}
	return occulited.NoticeWarning
}

// NoticeID derives a notice id from a warning id: lower-cased, every
// character the box does not take replaced by a dot, at most 64
// characters, starting with a letter or digit.
func NoticeID(warningID string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(warningID) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('.')
		}
	}
	id := strings.TrimLeft(b.String(), "._-")
	if id == "" {
		return disambiguate("w", warningID)
	}
	if len(id) > maxNoticeID {
		return disambiguate(id, warningID)
	}
	return id
}

// disambiguate makes id unique for warningID by a short hash of the
// warning id, keeping the result within the box's length.
func disambiguate(id, warningID string) string {
	sum := sha256.Sum256([]byte(warningID))
	suffix := "-" + hex.EncodeToString(sum[:4])
	if len(id)+len(suffix) > maxNoticeID {
		id = id[:maxNoticeID-len(suffix)]
	}
	return id + suffix
}
