// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"testing"
	"time"
)

// TestParseOnDurationReadsOneVocabulary pins the keys and the unit a siren
// turn_on accepts.
//
// A bare number is seconds. It used to be milliseconds on the invoke plane and
// seconds on the service plane, so {"duration": 30} wrote DURATION_VALUE=0
// through REST/WS and DURATION_VALUE=30 through MQTT — same key, same device,
// two wire values. Home Assistant's MQTT siren sends `duration` in seconds,
// which is what settles the unit.
func TestParseOnDurationReadsOneVocabulary(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		params map[string]any
		want   time.Duration
		wantOK bool
	}{
		{"canonical seconds key", map[string]any{"seconds": 30.0}, 30 * time.Second, true},
		{"duration as a bare number is seconds", map[string]any{"duration": 30.0}, 30 * time.Second, true},
		{"duration as an integer", map[string]any{"duration": 5}, 5 * time.Second, true},
		{"duration as a Go duration string", map[string]any{"duration": "1m30s"}, 90 * time.Second, true},
		{"duration_seconds", map[string]any{"duration_seconds": 12.0}, 12 * time.Second, true},
		{"seconds wins over duration", map[string]any{"seconds": 7.0, "duration": 9.0}, 7 * time.Second, true},
		{"absent", map[string]any{}, 0, false},
	} {
		got, ok, err := ParseOnDuration(tc.params)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("%s: got (%v, %v), want (%v, %v)", tc.name, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestParseOnDurationRejectsWhatItCannotRead pins that an unreadable duration
// fails the command instead of falling through to the device default. The
// service handler used to drop a value it could not parse: {"duration": "5s"}
// was silently ignored there while the invoke plane honoured it.
func TestParseOnDurationRejectsWhatItCannotRead(t *testing.T) {
	t.Parallel()

	for _, params := range []map[string]any{
		{"duration": "not a duration"},
		{"duration": []any{1}},
		{"seconds": "abc"},
	} {
		if _, _, err := ParseOnDuration(params); err == nil {
			t.Errorf("%v: want an error", params)
		}
	}
}
