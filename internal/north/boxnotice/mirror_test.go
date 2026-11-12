// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package boxnotice_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/north/boxnotice"
	"github.com/SukramJ/openccu-loom/internal/warnings"
)

// source is a warning list a test sets.
type source struct {
	mu   sync.Mutex
	list []warnings.Warning
}

func (s *source) Active() []warnings.Warning {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]warnings.Warning(nil), s.list...)
}

func (s *source) set(list ...warnings.Warning) {
	s.mu.Lock()
	s.list = list
	s.mu.Unlock()
}

// texts renders a recognisable sentence per language.
type texts struct{}

func (texts) Warning(w warnings.Warning) occulited.NoticeText {
	return occulited.NoticeText{DE: "de " + w.ID, EN: "en " + w.ID}
}

func (texts) More(n int) occulited.NoticeText {
	return occulited.NoticeText{DE: fmt.Sprintf("de %d weitere", n), EN: fmt.Sprintf("en %d more", n)}
}

type fixture struct {
	fake   *litefake.Fake
	src    *source
	mirror *boxnotice.Mirror
	token  func() string
	setTok func(string)
}

// start runs a mirror against a fresh fake box. The resynchronisation is
// an hour away, so whatever a test observes after the first
// reconciliation came from a nudge.
func start(t *testing.T, skip func(warnings.Warning) bool, initial ...warnings.Warning) *fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	f, err := litefake.Start(ctx, litefake.Options{})
	cancel()
	if err != nil {
		t.Fatalf("litefake.Start: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	client, err := occulited.New(occulited.Config{BaseURL: f.URL(), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("occulited.New: %v", err)
	}
	var mu sync.Mutex
	tok := litefake.DefaultAddonControlToken
	fx := &fixture{fake: f, src: &source{}}
	fx.token = func() string { mu.Lock(); defer mu.Unlock(); return tok }
	fx.setTok = func(s string) { mu.Lock(); tok = s; mu.Unlock() }
	fx.src.set(initial...)
	fx.mirror = boxnotice.New(boxnotice.Config{
		Warnings: fx.src, Box: client, Texts: texts{}, Skip: skip, Resync: time.Hour,
		Token:  func() (string, error) { return fx.token(), nil },
		Logger: slog.New(slog.DiscardHandler),
	})
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); fx.mirror.Run(runCtx) }()
	t.Cleanup(func() { stop(); <-done })
	return fx
}

// ids are the notice ids the box holds, joined in posting order.
func (fx *fixture) ids() string {
	notices := fx.fake.AddonNotices()
	out := make([]string, 0, len(notices))
	for i := range notices {
		out = append(out, notices[i].ID)
	}
	return strings.Join(out, ",")
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func warn(id string) warnings.Warning {
	return warnings.Warning{ID: id, Severity: warnings.SeverityWarning}
}

func fail(id string) warnings.Warning {
	return warnings.Warning{ID: id, Severity: warnings.SeverityError}
}

// TestMirrorPostsAndClearsWithTheWarnings pins the mirror's job: an active
// warning stands on the box in both languages, a changed one is replaced,
// and a cleared one is removed — each after a nudge, not on a clock.
func TestMirrorPostsAndClearsWithTheWarnings(t *testing.T) {
	t.Parallel()
	fx := start(t, nil, warn("health:mqtt"))
	waitFor(t, "the first notice", func() bool { return fx.ids() == "health.mqtt" })
	n := fx.fake.AddonNotices()[0]
	if n.Severity != "warning" || n.Title.DE != "de health:mqtt" || n.Title.EN != "en health:mqtt" {
		t.Errorf("notice = %+v, want the warning's severity and both sentences", n)
	}

	fx.src.set(fail("health:mqtt"), warn("pairing:pending"))
	fx.mirror.Nudge()
	waitFor(t, "the escalated and the new notice", func() bool {
		got := fx.fake.AddonNotices()
		return len(got) == 2 && got[0].Severity == "error"
	})

	fx.src.set(warn("pairing:pending"))
	fx.mirror.Nudge()
	waitFor(t, "the cleared notice to go", func() bool { return fx.ids() == "pairing.pending" })

	fx.src.set()
	fx.mirror.Nudge()
	waitFor(t, "every notice to go", func() bool { return fx.ids() == "" })
}

// TestMirrorLeavesAnUnchangedNoticeAlone pins that a reconciliation which
// finds a notice as it should be does not write it again: an
// administrator's silence on the box rests on the notice staying put.
func TestMirrorLeavesAnUnchangedNoticeAlone(t *testing.T) {
	t.Parallel()
	fx := start(t, nil, warn("health:mqtt"))
	waitFor(t, "the first notice", func() bool { return fx.ids() == "health.mqtt" })
	puts := fx.fake.AddonNoticePuts()

	fx.src.set(warn("health:mqtt"), warn("health:matter"))
	fx.mirror.Nudge()
	waitFor(t, "the second notice", func() bool { return len(fx.fake.AddonNotices()) == 2 })
	if got := fx.fake.AddonNoticePuts() - puts; got != 1 {
		t.Errorf("notice writes for one new warning = %d, want 1: the unchanged notice was written again", got)
	}
}

// TestMirrorRemovesWhatItDoesNotWant pins that the mirror owns the
// add-on's notice set: a notice no warning calls for is cleared, whoever
// posted it.
func TestMirrorRemovesWhatItDoesNotWant(t *testing.T) {
	t.Parallel()
	fx := start(t, nil, warn("health:mqtt"))
	waitFor(t, "the first notice", func() bool { return fx.ids() == "health.mqtt" })
	client, err := occulited.New(occulited.Config{BaseURL: fx.fake.URL(), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	stray := occulited.Notice{ID: "left-over", Severity: occulited.NoticeInfo, Title: occulited.NoticeText{DE: "x", EN: "x"}}
	if err := client.PutAddonNotice(context.Background(), litefake.DefaultAddonControlToken, stray); err != nil {
		t.Fatalf("posting the stray notice: %v", err)
	}
	fx.mirror.Nudge()
	waitFor(t, "the stray notice to go", func() bool { return fx.ids() == "health.mqtt" })
}

// TestMirrorSkipsWhatItIsToldToSkip pins the filter: a skipped warning
// never reaches the box.
func TestMirrorSkipsWhatItIsToldToSkip(t *testing.T) {
	t.Parallel()
	skip := func(w warnings.Warning) bool { return strings.HasPrefix(w.ID, "servicemsg:") }
	fx := start(t, skip, warn("servicemsg:box"), warn("health:mqtt"))
	waitFor(t, "the notice", func() bool { return fx.ids() != "" })
	if got := fx.ids(); got != "health.mqtt" {
		t.Errorf("box holds %q, want the unskipped warning alone", got)
	}
}

// TestMirrorSummarisesWhatDoesNotFit pins the box's limit of twenty:
// errors come first, and one notice counts what was left out.
func TestMirrorSummarisesWhatDoesNotFit(t *testing.T) {
	t.Parallel()
	list := make([]warnings.Warning, 0, 25)
	for i := range 24 {
		list = append(list, warn(fmt.Sprintf("health:w%02d", i)))
	}
	list = append(list, fail("incident:zz-last-by-name"))
	fx := start(t, nil, list...)
	waitFor(t, "twenty notices", func() bool { return len(fx.fake.AddonNotices()) == 20 })
	var more *litefake.AddonNotice
	sawError := false
	notices := fx.fake.AddonNotices()
	for i := range notices {
		if notices[i].ID == "more" {
			more = &notices[i]
		}
		if notices[i].ID == "incident.zz-last-by-name" {
			sawError = true
		}
	}
	if !sawError {
		t.Error("the error is not among the twenty: errors must come before warnings")
	}
	// 25 warnings, 19 shown beside the summary: 6 left out.
	if more == nil || more.Title.EN != "en 6 more" {
		t.Errorf("summary notice = %+v, want one that counts 6 more", more)
	}
}

// TestMirrorFollowsANewControlToken pins the recovery after the box's
// service restarted: the token is read anew for a reconciliation, so the
// next one succeeds once the new token is there.
func TestMirrorFollowsANewControlToken(t *testing.T) {
	t.Parallel()
	fx := start(t, nil, warn("health:mqtt"))
	waitFor(t, "the first notice", func() bool { return fx.ids() == "health.mqtt" })

	fx.fake.SetAddonControlToken("after-restart")
	fx.fake.DropAddonNotices()
	fx.mirror.Nudge() // refused: the mirror still reads the old token
	time.Sleep(500 * time.Millisecond)
	if got := fx.ids(); got != "" {
		t.Fatalf("box holds %q although the mirror's token is the old one", got)
	}
	fx.setTok("after-restart")
	fx.mirror.Nudge()
	waitFor(t, "the notice to be posted with the new token", func() bool { return fx.ids() == "health.mqtt" })
}

// TestNoticeIDIsWhatTheBoxTakes pins the id derivation against the box's
// own rule: the box accepts every derived id.
func TestNoticeIDIsWhatTheBoxTakes(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"health:mqtt":         "health.mqtt",
		"servicemsg:Kearney":  "servicemsg.kearney",
		"incident:HmIP-RF/Üx": "incident.hmip-rf..x",
		"pairing:pending":     "pairing.pending",
	} {
		if got := boxnotice.NoticeID(in); got != want {
			t.Errorf("NoticeID(%q) = %q, want %q", in, got, want)
		}
	}
	long := boxnotice.NoticeID("incident:" + strings.Repeat("x", 100))
	if len(long) != 64 {
		t.Errorf("a long id is %d characters, want 64", len(long))
	}
	if a, b := boxnotice.NoticeID("incident:"+strings.Repeat("x", 100)), boxnotice.NoticeID("incident:"+strings.Repeat("x", 101)); a == b {
		t.Error("two long warning ids derive the same notice id")
	}
	fx := start(t, nil, warn(":::"), warn("incident:"+strings.Repeat("x", 100)), warn("incident:HmIP-RF/Üx"))
	waitFor(t, "the box to take every derived id", func() bool { return len(fx.fake.AddonNotices()) == 3 })
}
