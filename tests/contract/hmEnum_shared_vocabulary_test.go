// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHmEnumClickEventsAndKeypressSourcesAgree ties the two independent
// spellings of one domain fact — which wire parameters are button
// presses — that live on opposite sides of a package boundary.
//
// [hmenum.ClickEvents] decides data-point shape (the resolver builds a
// Button from it) and HmIP-PS event suppression; event.Classify decides
// the KindKeypress verdict that MQTT discovery, the event coordinator
// and the event bridge consume. Neither derives from the other, and the
// damage is one-sided in both directions: a press parameter added to
// the hmenum set alone gets a data point that Classify does not know,
// so no trigger is published; added to the model set alone it gets a
// keypress kind with no data point behind it.
func TestHmEnumClickEventsAndKeypressSourcesAgree(t *testing.T) {
	t.Parallel()

	for p := range hmenum.ClickEvents {
		kind, known := event.Classify(p)
		if !known {
			t.Errorf("%s is a click event but event.Classify does not know it", p)
			continue
		}
		if kind != event.KindKeypress {
			t.Errorf("event.Classify(%s) = %q, want %q", p, kind, event.KindKeypress)
		}
	}

	sources := event.Sources(event.KindKeypress)
	for _, p := range sources {
		if !p.IsClickEvent() {
			t.Errorf("event.Sources(keypress) offers %s, which hmenum.ClickEvents does not carry", p)
		}
	}
	if len(sources) != len(hmenum.ClickEvents) {
		t.Errorf("keypress sources = %d, hmenum.ClickEvents = %d", len(sources), len(hmenum.ClickEvents))
	}
}

// TestHmEnumStatusPairGrammarIsOneGrammar ties the two halves of one
// pairing rule together. The device constructor builds the sibling name
// with generic.DetectStatusParameter's own "_STATUS" literal; the
// callback handler and event bridge recover the base name with
// hmenum's constant. Change one and the pairing breaks in half — the
// sibling data points still get attached, the incoming status events
// stop being recognised — with every test in either package still green.
func TestHmEnumStatusPairGrammarIsOneGrammar(t *testing.T) {
	t.Parallel()

	for _, base := range []hmenum.Parameter{
		hmenum.ParameterLevel,
		hmenum.ParameterLevel2,
		hmenum.ParameterState,
	} {
		want, ok := base.StatusPair()
		if !ok {
			t.Fatalf("%s has no status pair", base)
		}
		paramset := map[string]struct{}{string(base): {}, string(want): {}}
		got, found := generic.DetectStatusParameter(string(base), paramset)
		if !found {
			t.Errorf("generic.DetectStatusParameter(%s) found nothing, hmenum offers %s", base, want)
			continue
		}
		if got != string(want) {
			t.Errorf("generic.DetectStatusParameter(%s) = %q, hmenum.StatusPair = %q", base, got, want)
		}
		back, isPair := hmenum.Parameter(got).BasePair()
		if !isPair || back != base {
			t.Errorf("BasePair(%q) = (%s, %v), want (%s, true)", got, back, isPair, base)
		}
	}
}

// TestHmEnumRollbackReasonVocabularyIsShared pins the producing side's
// rollback vocabulary to the one that is published.
//
// The value crosses from internal/model/generic into hmenum through a
// bare named-string conversion, so a reason renamed or added on the
// producing side compiles cleanly and ships a string hmenum never
// declared. The wire schema types the field as a free string, so
// nothing downstream would reject it either.
func TestHmEnumRollbackReasonVocabularyIsShared(t *testing.T) {
	t.Parallel()

	pairs := []struct {
		produced  generic.RollbackReason
		published hmenum.RollbackReason
	}{
		{generic.RollbackReasonTimeout, hmenum.RollbackReasonTimeout},
		{generic.RollbackReasonSendError, hmenum.RollbackReasonSendError},
		{generic.RollbackReasonValueMismatch, hmenum.RollbackReasonValueMismatch},
	}
	published := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if string(p.produced) != string(p.published) {
			t.Errorf("produced %q, published %q — the conversion would ship the former unchecked",
				p.produced, p.published)
		}
		published = append(published, string(p.published))
	}
	slices.Sort(published)
	want := []string{"mismatch", "send_error", "timeout"}
	if !slices.Equal(published, want) {
		t.Errorf("published rollback vocabulary = %v, want %v", published, want)
	}
}
