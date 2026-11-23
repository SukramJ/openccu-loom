// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

type staticFindings []matteradapter.Finding

func (s staticFindings) Findings() []matteradapter.Finding { return s }

// TestMatterWarningsRenderEveryFindingInTheDaemonLocale: each finding
// becomes one warning with its code, its first-seen time and a message in
// the daemon's locale that names the device — per locale, so a key missing
// from one catalogue cannot hide behind the other's fallback.
func TestMatterWarningsRenderEveryFindingInTheDaemonLocale(t *testing.T) {
	t.Parallel()
	catalogs, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("catalogs: %v", err)
	}
	since := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	src := staticFindings{
		{Code: matteradapter.FindingDeviceRefused, Key: "k1", Name: "Kellerlicht", ChannelAddress: "0001:1", DeviceAddress: "0001", Err: "constraint", Since: since},
		{Code: matteradapter.FindingDeviceWithheld, Key: "k2", DeviceAddress: "0002", Since: since},
	}
	for _, tc := range []struct {
		locale  string
		refused []string
		held    []string
	}{
		{"en", []string{"Kellerlicht (0001:1)", "refused", "constraint"}, []string{"0002 (0002)", "north.matter.spec_level"}},
		{"de", []string{"Kellerlicht (0001:1)", "abgelehnt", "constraint"}, []string{"0002 (0002)", "Spezifikationsstufe"}},
	} {
		got := matterWarnings(src, catalogs, tc.locale)(context.Background())
		if len(got) != 2 {
			t.Fatalf("%s: %d warnings, want 2", tc.locale, len(got))
		}
		for i, want := range [][]string{tc.refused, tc.held} {
			w := got[i]
			if w.Code != string(src[i].Code) || !w.Since.Equal(since) {
				t.Errorf("%s: warning %d = %+v, want code %q since %v", tc.locale, i, w, src[i].Code, since)
			}
			for _, part := range want {
				if !strings.Contains(w.Message, part) {
					t.Errorf("%s: warning %d message %q lacks %q", tc.locale, i, w.Message, part)
				}
			}
		}
	}
}

// TestMatterWarningsWithoutFindingsAreEmpty: no finding, no warning — the
// page shows nothing rather than an empty block of noise.
func TestMatterWarningsWithoutFindingsAreEmpty(t *testing.T) {
	t.Parallel()
	if got := matterWarnings(staticFindings(nil), nil, "en")(context.Background()); len(got) != 0 {
		t.Fatalf("warnings = %+v, want none", got)
	}
}
