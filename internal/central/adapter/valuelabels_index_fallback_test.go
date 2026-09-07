// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
)

// The HmIP-DLP carries CHANNEL_OPERATION_MODE on three channels, each with
// a different VALUE_LIST. Captured read-only from CCU 172.18.4.29 on
// 2026-09-07; the full capture including the raw paramset description is
// in notes/parity/fixtures/hmip-dlp-channel-operation-mode.json.
var doorLockOperationModes = []struct {
	channel     int
	channelType string
	values      []string
}{
	{2, "ACCELERATION_TRANSCEIVER", []string{"OFF", "TILT_DETECTION", "ANY_MOTION", "TILT_AND_MOTION_DETECTION"}},
	{3, "DOOR_STATE_TRANSCEIVER", []string{"OFF", "ON", "ON_AUTO_CALIBRATION"}},
	{12, "DOOR_LOCK_TRANSCEIVER", []string{
		"IGNORE_DOOR_OPEN", "SKIP_HOLD_TIME_OPENING",
		"SKIP_RELOCK_DELAY_CLOSING", "SKIP_HOLD_TIME_OPENING_RELOCK_DELAY_CLOSING",
	}},
}

// TestValueListLabelsDoNotBorrowAnotherEnumsLabelsByIndex pins that a
// VALUE_LIST token no table knows renders as the humanised token, never as
// a label lifted from a different enum.
//
// The door lock's operation modes came out as "Inaktiv Aktiv Ein RGB". The
// tokens are unknown, so the lookup retried with the *index* — and an index
// retry that is allowed to fall through the unqualified stages answers from
// somewhere else entirely: "channel_operation_mode=0/1" was extracted from a
// device whose enum starts Inaktiv/Aktiv, and indices 2 and 3 reached the
// value-only reverse index, which returns "the shortest label any parameter
// has for the VALUE 2 / 3" — hence a colour on a door lock.
func TestValueListLabelsDoNotBorrowAnotherEnumsLabelsByIndex(t *testing.T) {
	t.Parallel()
	tr, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	// Labels the extract holds for a different enum under the same
	// parameter name, plus the value-only index answers for "2" and "3".
	borrowed := map[string]bool{
		"Inaktiv": true, "Aktiv": true, "RGB": true,
		"Inactive": true, "Active": true,
	}
	for _, tc := range doorLockOperationModes {
		for _, locale := range []string{"de", "en"} {
			got := ValueListLabels(tr, locale, tc.channelType, "CHANNEL_OPERATION_MODE", tc.values)
			for i, label := range got {
				if borrowed[label] {
					t.Errorf("channel %d (%s) %s: value %q rendered as %q — that label belongs to a different enum",
						tc.channel, tc.channelType, locale, tc.values[i], label)
				}
			}
		}
	}
}

// TestValueListLabelsOfTheDoorLockOperationMode records what the three
// channels render as, so a later change to the lookup chain or to the
// embedded extract has to state its effect rather than drift silently.
//
// "Aus" / "Ein" are not borrowed: those come from the *token* lookup, which
// keeps all four of its stages — the tokens OFF and ON are values the
// tables genuinely carry. Only the index retry is restricted.
func TestValueListLabelsOfTheDoorLockOperationMode(t *testing.T) {
	t.Parallel()
	tr, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	for _, tc := range []struct {
		channelType string
		values      []string
		want        []string
	}{
		{
			"ACCELERATION_TRANSCEIVER",
			doorLockOperationModes[0].values,
			[]string{"Aus", "Tilt Detection", "Any Motion", "Tilt And Motion Detection"},
		},
		{
			"DOOR_STATE_TRANSCEIVER",
			doorLockOperationModes[1].values,
			[]string{"Aus", "Ein", "On Auto Calibration"},
		},
		{
			"DOOR_LOCK_TRANSCEIVER",
			doorLockOperationModes[2].values,
			[]string{
				"Ignore Door Open", "Skip Hold Time Opening",
				"Skip Relock Delay Closing", "Skip Hold Time Opening Relock Delay Closing",
			},
		},
	} {
		got := ValueListLabels(tr, "de", tc.channelType, "CHANNEL_OPERATION_MODE", tc.values)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s de: got %v, want %v", tc.channelType, got, tc.want)
		}
	}
}

// TestValueListLabelsStillResolveAnIndexForAParameterTheTableNeverQualifies
// is the counterweight: the restriction must not take the parameters with
// it whose only label source IS the unqualified index key.
//
// Measured against the embedded extract: 137 parameters carry unqualified
// index keys and 105 of them have no token key at all — DST_START_MONTH is
// one of them, and its labels have to survive. A restriction that silenced
// those would trade one wrong enum for a hundred untranslated ones.
func TestValueListLabelsStillResolveAnIndexForAParameterTheTableNeverQualifies(t *testing.T) {
	t.Parallel()
	tr, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	// Tokens the tables do not know, so only the index retry can answer.
	got := ValueListLabels(tr, "de", "HEATING_CLIMATECONTROL_TRANSCEIVER",
		"DST_START_MONTH", []string{"UNKNOWN_A", "UNKNOWN_B", "UNKNOWN_C"})
	want := []string{"Sonntag", "Montag", "Dienstag"}
	if !slices.Equal(got, want) {
		t.Errorf("DST_START_MONTH by index: got %v, want %v — the index retry must keep working "+
			"for a parameter the table never keys by channel type", got, want)
	}
}

// TestValueListLabelsKeepTheChannelTypedIndexEntry pins the one index
// lookup that stays sound: an entry that names both the parameter and the
// channel type describes that very enum, so LEVEL_STATUS on a blind keeps
// its wording while a channel type the table does not name no longer
// borrows it.
func TestValueListLabelsKeepTheChannelTypedIndexEntry(t *testing.T) {
	t.Parallel()
	tr, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	unknownTokens := []string{"UNKNOWN_A", "UNKNOWN_B"}
	got := ValueListLabels(tr, "de", "BLIND_TRANSMITTER", "LEVEL_STATUS", unknownTokens)
	want := []string{"Wert Behanghöhe: Normal", "Wert Behanghöhe: unbekannt"}
	if !slices.Equal(got, want) {
		t.Errorf("BLIND_TRANSMITTER LEVEL_STATUS: got %v, want %v — the channel-typed index entry is the sound one", got, want)
	}
	borrowed := ValueListLabels(tr, "de", "SOME_OTHER_TRANSCEIVER", "LEVEL_STATUS", unknownTokens)
	for i, label := range borrowed {
		if label != humanizeRaw(unknownTokens[i]) {
			t.Errorf("SOME_OTHER_TRANSCEIVER LEVEL_STATUS[%d] = %q, want the humanised token — "+
				"the table keys LEVEL_STATUS by channel type, so an entry naming none cannot be attributed to this one",
				i, label)
		}
	}
}
