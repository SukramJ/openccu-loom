// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stubbedOutputCandidatesFixture substitutes a fixed OutputCandidates
// result over an otherwise real alarmPanelFixture, recording every class
// argument ListAlarmOutputCandidates forwards to the facade. Mirrors the
// embed-and-override pattern of stubbedPanelsFixture in alarm_panels_test.go.
type stubbedOutputCandidatesFixture struct {
	*alarmPanelFixture
	candidates  []alarm.OutputCandidate
	seenClasses []hmenum.AlarmOutputClass
}

func (s *stubbedOutputCandidatesFixture) OutputCandidates(class hmenum.AlarmOutputClass) []alarm.OutputCandidate {
	s.seenClasses = append(s.seenClasses, class)
	return s.candidates
}

var _ AlarmPanel = (*stubbedOutputCandidatesFixture)(nil)

// newStubbedOutputCandidatesFixture wraps a fresh alarmPanelFixture so the
// facade's OutputCandidates answer is fully controlled by the test while
// every other AlarmPanel method stays the real fixture's implementation.
func newStubbedOutputCandidatesFixture(t *testing.T, candidates []alarm.OutputCandidate) *stubbedOutputCandidatesFixture {
	t.Helper()
	return &stubbedOutputCandidatesFixture{alarmPanelFixture: newAlarmPanelFixture(t), candidates: candidates}
}

// twoOutputCandidateRows builds two fully-populated domain candidates that
// together exercise every AlarmOutputCandidate field, including a
// deliberately non-canonical Classes order so the test can prove the
// handler passes the facade's order straight through rather than resorting
// it, and the full spread of the picker-extras fields (tones, lights,
// soundfiles, dimmable).
func twoOutputCandidateRows() []alarm.OutputCandidate {
	return []alarm.OutputCandidate{
		{
			Central:         alarmFixtureCentral,
			DeviceAddress:   "VCU0000001",
			DeviceName:      "Front Siren",
			Model:           "HmIP-ASIR-2",
			ChannelAddress:  "VCU0000001:3",
			ChannelNo:       3,
			ChannelName:     "Acoustic",
			Rooms:           []string{"Hallway"},
			Functions:       []string{"Security"},
			Classes:         []hmenum.AlarmOutputClass{hmenum.AlarmOutputClassChirp, hmenum.AlarmOutputClassAcousticSiren, hmenum.AlarmOutputClassOpticalSiren},
			Kind:            "siren",
			AvailableTones:  []string{"frequency_rising", "standard"},
			AvailableLights: []string{"flash", "double_flash"},
		},
		{
			Central:             alarmFixtureCentral,
			DeviceAddress:       "VCU0000002",
			DeviceName:          "Hallway Light",
			Model:               "HmIP-BSL",
			ChannelAddress:      "VCU0000002:4",
			ChannelNo:           4,
			ChannelName:         "Light",
			Classes:             []hmenum.AlarmOutputClass{hmenum.AlarmOutputClassAlarmLight, hmenum.AlarmOutputClassSwitchedSiren},
			Kind:                "light",
			AvailableSoundfiles: []string{"alarm.mp3", "chime.mp3"},
			Dimmable:            true,
		},
	}
}

// wantOutputCandidateDTOs is the wire-DTO mirror of twoOutputCandidateRows,
// field for field.
func wantOutputCandidateDTOs() []hmapi.AlarmOutputCandidate {
	return []hmapi.AlarmOutputCandidate{
		{
			Central:         alarmFixtureCentral,
			DeviceAddress:   "VCU0000001",
			DeviceName:      "Front Siren",
			Model:           "HmIP-ASIR-2",
			ChannelAddress:  "VCU0000001:3",
			ChannelNo:       3,
			ChannelName:     "Acoustic",
			Rooms:           []string{"Hallway"},
			Functions:       []string{"Security"},
			Classes:         []string{"chirp", "acoustic_siren", "optical_siren"},
			Kind:            "siren",
			AvailableTones:  []string{"frequency_rising", "standard"},
			AvailableLights: []string{"flash", "double_flash"},
		},
		{
			Central:             alarmFixtureCentral,
			DeviceAddress:       "VCU0000002",
			DeviceName:          "Hallway Light",
			Model:               "HmIP-BSL",
			ChannelAddress:      "VCU0000002:4",
			ChannelNo:           4,
			ChannelName:         "Light",
			Classes:             []string{"alarm_light", "switched_siren"},
			Kind:                "light",
			AvailableSoundfiles: []string{"alarm.mp3", "chime.mp3"},
			Dimmable:            true,
		},
	}
}

// TestListAlarmOutputCandidates_MirrorsAllFields verifies GET
// /alarm/output-candidates renders every AlarmOutputCandidate field
// straight through from the facade, preserving the Classes order exactly
// as the facade returned it.
func TestListAlarmOutputCandidates_MirrorsAllFields(t *testing.T) {
	t.Parallel()
	fx := newStubbedOutputCandidatesFixture(t, twoOutputCandidateRows())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/output-candidates", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmOutputCandidates(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var got []hmapi.AlarmOutputCandidate
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if want := wantOutputCandidateDTOs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates =\n%+v\nwant\n%+v", got, want)
	}
	if len(fx.seenClasses) != 1 || fx.seenClasses[0] != "" {
		t.Errorf("seenClasses = %+v, want one empty-class call (no filter)", fx.seenClasses)
	}
}

// TestListAlarmOutputCandidates_ClassFilter_ForwardsToFacade verifies
// ?class=acoustic_siren is forwarded verbatim to OutputCandidates rather
// than being filtered client-side by the handler.
func TestListAlarmOutputCandidates_ClassFilter_ForwardsToFacade(t *testing.T) {
	t.Parallel()
	fx := newStubbedOutputCandidatesFixture(t, twoOutputCandidateRows())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/output-candidates?class=acoustic_siren", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmOutputCandidates(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if len(fx.seenClasses) != 1 || fx.seenClasses[0] != hmenum.AlarmOutputClassAcousticSiren {
		t.Fatalf("seenClasses = %+v, want exactly one acoustic_siren call", fx.seenClasses)
	}
}

// TestListAlarmOutputCandidates_NonDeviceBackedOrUnknownClass_Returns400
// verifies notification and sysvar_mirror (valid classes, but not
// device-backed) and a wholly unknown class token are all rejected as a
// client error, and never reach the facade.
func TestListAlarmOutputCandidates_NonDeviceBackedOrUnknownClass_Returns400(t *testing.T) {
	t.Parallel()
	cases := []string{"notification", "sysvar_mirror", "bogus"}
	for _, class := range cases {
		t.Run(class, func(t *testing.T) {
			t.Parallel()
			fx := newStubbedOutputCandidatesFixture(t, nil)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/output-candidates?class="+class, http.NoBody)
			w := httptest.NewRecorder()
			ListAlarmOutputCandidates(fx, nil).ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
			}
			if ct := w.Header().Get("Content-Type"); ct != problem.ContentType {
				t.Errorf("content-type = %q, want %q", ct, problem.ContentType)
			}
			var problemBody problem.Details
			if err := json.Unmarshal(w.Body.Bytes(), &problemBody); err != nil {
				t.Fatalf("unmarshal problem: %v", err)
			}
			if problemBody.Status != http.StatusBadRequest {
				t.Errorf("problem status = %d, want 400", problemBody.Status)
			}
			if len(fx.seenClasses) != 0 {
				t.Errorf("facade must not be called for a rejected filter, seenClasses = %+v", fx.seenClasses)
			}
		})
	}
}

// TestListAlarmOutputCandidates_EmptyList_RendersEmptyArrayNotNull
// verifies an empty candidate set renders the JSON array literal `[]`
// rather than `null`, so clients can range over the response unconditionally.
func TestListAlarmOutputCandidates_EmptyList_RendersEmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	fx := newStubbedOutputCandidatesFixture(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/output-candidates", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmOutputCandidates(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if body := strings.TrimSpace(w.Body.String()); body != "[]" {
		t.Fatalf("body = %q, want the empty array literal []", body)
	}
}

// --- ListAlarmRemoteKeyCandidates ----------------------------------------

// stubbedRemoteKeyCandidatesFixture substitutes a fixed RemoteKeyCandidates
// result over an otherwise real alarmPanelFixture, mirroring
// stubbedOutputCandidatesFixture above.
type stubbedRemoteKeyCandidatesFixture struct {
	*alarmPanelFixture
	candidates []alarm.RemoteKeyCandidate
}

func (s *stubbedRemoteKeyCandidatesFixture) RemoteKeyCandidates() []alarm.RemoteKeyCandidate {
	return s.candidates
}

var _ AlarmPanel = (*stubbedRemoteKeyCandidatesFixture)(nil)

// newStubbedRemoteKeyCandidatesFixture wraps a fresh alarmPanelFixture so the
// facade's RemoteKeyCandidates answer is fully controlled by the test while
// every other AlarmPanel method stays the real fixture's implementation.
func newStubbedRemoteKeyCandidatesFixture(t *testing.T, candidates []alarm.RemoteKeyCandidate) *stubbedRemoteKeyCandidatesFixture {
	t.Helper()
	return &stubbedRemoteKeyCandidatesFixture{alarmPanelFixture: newAlarmPanelFixture(t), candidates: candidates}
}

// twoRemoteKeyCandidateRows builds two fully-populated domain candidates
// that together exercise every AlarmRemoteKeyCandidate field.
func twoRemoteKeyCandidateRows() []alarm.RemoteKeyCandidate {
	return []alarm.RemoteKeyCandidate{
		{
			Central:        alarmFixtureCentral,
			DeviceAddress:  "VCU0000003",
			DeviceName:     "Living Room Remote",
			Model:          "HmIP-WRC6",
			ChannelAddress: "VCU0000003:1",
			ChannelNo:      1,
			ChannelName:    "Button 1",
			Parameters:     []string{"PRESS_SHORT", "PRESS_LONG"},
		},
		{
			Central:        alarmFixtureCentral,
			DeviceAddress:  "VCU0000004",
			DeviceName:     "Front Door Wall Button",
			Model:          "HmIP-WRC2",
			ChannelAddress: "VCU0000004:2",
			ChannelNo:      2,
			ChannelName:    "Button 2",
			Parameters:     []string{"PRESS_SHORT"},
		},
	}
}

// wantRemoteKeyCandidateDTOs is the wire-DTO mirror of
// twoRemoteKeyCandidateRows, field for field.
func wantRemoteKeyCandidateDTOs() []hmapi.AlarmRemoteKeyCandidate {
	return []hmapi.AlarmRemoteKeyCandidate{
		{
			Central:        alarmFixtureCentral,
			DeviceAddress:  "VCU0000003",
			DeviceName:     "Living Room Remote",
			Model:          "HmIP-WRC6",
			ChannelAddress: "VCU0000003:1",
			ChannelNo:      1,
			ChannelName:    "Button 1",
			Parameters:     []string{"PRESS_SHORT", "PRESS_LONG"},
		},
		{
			Central:        alarmFixtureCentral,
			DeviceAddress:  "VCU0000004",
			DeviceName:     "Front Door Wall Button",
			Model:          "HmIP-WRC2",
			ChannelAddress: "VCU0000004:2",
			ChannelNo:      2,
			ChannelName:    "Button 2",
			Parameters:     []string{"PRESS_SHORT"},
		},
	}
}

// TestListAlarmRemoteKeyCandidates_MirrorsAllFields verifies GET
// /alarm/remote-key-candidates renders every AlarmRemoteKeyCandidate field
// straight through from the facade.
func TestListAlarmRemoteKeyCandidates_MirrorsAllFields(t *testing.T) {
	t.Parallel()
	fx := newStubbedRemoteKeyCandidatesFixture(t, twoRemoteKeyCandidateRows())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/remote-key-candidates", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmRemoteKeyCandidates(fx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var got []hmapi.AlarmRemoteKeyCandidate
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if want := wantRemoteKeyCandidateDTOs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates =\n%+v\nwant\n%+v", got, want)
	}
}

// TestListAlarmRemoteKeyCandidates_EmptyList_RendersEmptyArrayNotNull
// verifies an empty candidate set renders the JSON array literal `[]`
// rather than `null`, so clients can range over the response unconditionally.
func TestListAlarmRemoteKeyCandidates_EmptyList_RendersEmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	fx := newStubbedRemoteKeyCandidatesFixture(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/remote-key-candidates", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmRemoteKeyCandidates(fx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if body := strings.TrimSpace(w.Body.String()); body != "[]" {
		t.Fatalf("body = %q, want the empty array literal []", body)
	}
}

// TestListAlarmRemoteKeyCandidates_NilParameters_RendersEmptyArrayNotNull
// verifies a candidate whose Parameters slice is nil (the domain zero
// value) still renders "parameters": [] rather than null — the handler
// must normalise it before it ever reaches the JSON encoder, since
// AlarmRemoteKeyCandidate.Parameters carries no `omitempty`.
func TestListAlarmRemoteKeyCandidates_NilParameters_RendersEmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	fx := newStubbedRemoteKeyCandidatesFixture(t, []alarm.RemoteKeyCandidate{
		{
			Central:        alarmFixtureCentral,
			DeviceAddress:  "VCU0000005",
			Model:          "HmIP-WRC6",
			ChannelAddress: "VCU0000005:1",
			ChannelNo:      1,
			Parameters:     nil,
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/remote-key-candidates", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmRemoteKeyCandidates(fx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"parameters":[]`) {
		t.Fatalf("body = %s, want parameters rendered as [] not null", w.Body.String())
	}
	var got []hmapi.AlarmRemoteKeyCandidate
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Parameters == nil || len(got[0].Parameters) != 0 {
		t.Fatalf("got = %+v, want one candidate with a non-nil empty Parameters slice", got)
	}
}
