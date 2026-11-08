// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// hub_wiring_messages_test.go covers messageDisplayName and the DisplayName
// population in loadAlarmMessages / loadServiceMessages.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/i18n"
	hubmodel "github.com/SukramJ/openccu-loom/internal/model/hub"
)

// ============================================================
// messageDisplayName — code extraction + i18n lookup
// ============================================================

func TestMessageDisplayNameCodeExtraction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		rawName string
		want    string
	}{
		{"AL-00021BE9:1.LOW_BAT", "LOW_BAT"},
		{"SM-0004A6:0.UNREACH", "UNREACH"},
		{"NODOTSHERE", "NODOTSHERE"},
		{"", ""},
		// The CCU appends the value of an enum alarm; without catalogues
		// the code keeps it in parentheses so nothing is lost.
		{"AL-NEQ0632439:1.ERROR-7", "ERROR (7)"},
	}
	for _, tc := range cases {
		got := messageDisplayName(nil, "en", tc.rawName, "")
		if got != tc.want {
			t.Errorf("messageDisplayName(nil, %q) = %q, want %q", tc.rawName, got, tc.want)
		}
	}
}

func TestMessageDisplayNameI18nHit(t *testing.T) {
	t.Parallel()
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	got := messageDisplayName(cats, "en", "AL-00021BE9:1.LOW_BAT", "")
	if got != "Low Battery" {
		t.Errorf("got %q, want %q", got, "Low Battery")
	}
}

// TestMessageDisplayNameEnumValue pins the reported case: an HM-Sec-SCo sabotage is the alarm "AL-NEQ0632439:1.ERROR-7", and the CCU
// resolves value 7 of ERROR to the list entry SABOTAGE. The operator must
// read "Sabotage", as in the CCU WebUI — not the code with its value.
func TestMessageDisplayNameEnumValue(t *testing.T) {
	t.Parallel()
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	cases := []struct {
		name      string
		rawName   string
		valueText string
		locale    string
		want      string
	}{
		{"value text translated", "AL-NEQ0632439:1.ERROR-7", "SABOTAGE", "en", "Sabotage"},
		{"value text translated (de)", "AL-NEQ0632439:1.ERROR-7", "SABOTAGE", "de", "Sabotage"},
		{"value text without catalogue entry", "AL-NEQ0632439:1.ERROR-7", "SOMETHING_NEW", "en", "Error: SOMETHING_NEW"},
		{"value without list entry", "AL-NEQ0632439:1.ERROR-3", "", "en", "Error (3)"},
		{"code without value", "AL-NEQ0632439:1.ERROR", "", "en", "Error"},
		{"unknown code with value", "AL-ABC:1.UNKNOWN_CUSTOM_CODE-2", "", "en", "UNKNOWN_CUSTOM_CODE (2)"},
		{"non-numeric suffix stays in the code", "AL-ABC:1.ERROR-X", "", "en", "ERROR-X"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := messageDisplayName(cats, tc.locale, tc.rawName, tc.valueText); got != tc.want {
				t.Errorf("messageDisplayName(%q, %q) = %q, want %q", tc.rawName, tc.valueText, got, tc.want)
			}
		})
	}
}

func TestMessageDisplayNameI18nFallback(t *testing.T) {
	t.Parallel()
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	got := messageDisplayName(cats, "en", "AL-00021BE9:1.UNKNOWN_CUSTOM_CODE", "")
	if got != "UNKNOWN_CUSTOM_CODE" {
		t.Errorf("got %q, want %q", got, "UNKNOWN_CUSTOM_CODE")
	}
}

func TestMessageDisplayNameNilCatalogs(t *testing.T) {
	t.Parallel()
	got := messageDisplayName(nil, "en", "AL-ADDR:1.LOW_BAT", "")
	if got != "LOW_BAT" {
		t.Errorf("got %q, want %q", got, "LOW_BAT")
	}
}

// ============================================================
// DisplayName in AdditionalInformation output
// ============================================================

func TestAlarmMessagesAdditionalInformationDisplayName(t *testing.T) {
	t.Parallel()
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	r := newRegaRunnerFor(t, `[{"id":"a1","name":"AL-ABC:1.LOW_BAT","timestamp":1700000000}]`)
	h := hubmodel.NewHub("test-central")
	if err := loadAlarmMessages(context.Background(), r, h, cats, "en"); err != nil {
		t.Fatalf("loadAlarmMessages: %v", err)
	}
	info := h.Messages.AdditionalInformation()
	if len(info) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(info))
	}
	dn, ok := info[0]["display_name"]
	if !ok {
		t.Fatal("display_name key missing from AdditionalInformation")
	}
	if dn != "Low Battery" {
		t.Errorf("display_name = %q, want %q", dn, "Low Battery")
	}
}

func TestServiceMessagesAdditionalInformationDisplayName(t *testing.T) {
	t.Parallel()
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	r := newRegaRunnerFor(t, `[{"id":"sm1","name":"SM-ABC:0.UNREACH","address":"ABC:0","device_name":"Switch"}]`)
	unit := newMinimalUnit(t, "HmIP-RF")
	if err := loadServiceMessages(context.Background(), r, unit, cats, "en"); err != nil {
		t.Fatalf("loadServiceMessages: %v", err)
	}
	msgs := unit.HubModel.ServiceMessages.List()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	info := unit.HubModel.ServiceMessages.AdditionalInformation()
	dn, ok := info[0]["display_name"]
	if !ok {
		t.Fatal("display_name key missing from AdditionalInformation")
	}
	if dn != "Unreachable" {
		t.Errorf("display_name = %q, want %q", dn, "Unreachable")
	}
}

// TestLoadServiceMessagesEnumValue drives the reported alarm through the
// loader: the CCU script reports value "7" and value_text "SABOTAGE" for
// "AL-NEQ0632439:1.ERROR-7" (measured on a CCU3). The model carries
// both, the parameter is ERROR (what the suppressor needs), and the
// display name reads like the CCU WebUI — in AdditionalInformation too,
// which is what the HA sensor and MQTT publish.
func TestLoadServiceMessagesEnumValue(t *testing.T) {
	t.Parallel()
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	r := newRegaRunnerFor(t, `[{"id":"7249","name":"AL%2DNEQ0632439%3A1%2EERROR%2D7","timestamp":1794334767,`+
		`"type":0,"address":"NEQ0632439","device_name":"Buero%5FFenster%5FHof","last_timestamp":0,`+
		`"counter":1,"rooms":[],"functions":[],"quittable":true,"value":"7","value_text":"SABOTAGE"}]`)
	unit := newMinimalUnit(t, "HmIP-RF", "BidCos-RF")
	if err := loadServiceMessages(context.Background(), r, unit, cats, "en"); err != nil {
		t.Fatalf("loadServiceMessages: %v", err)
	}
	msgs := unit.HubModel.ServiceMessages.List()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if m.Name != "AL-NEQ0632439:1.ERROR-7" {
		t.Errorf("Name = %q", m.Name)
	}
	if m.Parameter != "ERROR" {
		t.Errorf("Parameter = %q, want ERROR", m.Parameter)
	}
	if m.Value != "7" || m.ValueText != "SABOTAGE" {
		t.Errorf("Value/ValueText = %q/%q, want 7/SABOTAGE", m.Value, m.ValueText)
	}
	if m.DisplayName != "Sabotage" {
		t.Errorf("DisplayName = %q, want Sabotage", m.DisplayName)
	}
	info := unit.HubModel.ServiceMessages.AdditionalInformation()
	if len(info) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(info))
	}
	if info[0]["display_name"] != "Sabotage" || info[0]["value"] != "7" || info[0]["value_text"] != "SABOTAGE" {
		t.Errorf("AdditionalInformation = %v", info[0])
	}
}
