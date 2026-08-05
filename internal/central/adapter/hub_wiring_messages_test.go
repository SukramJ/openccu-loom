// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	}
	for _, tc := range cases {
		got := messageDisplayName(nil, "en", tc.rawName)
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
	got := messageDisplayName(cats, "en", "AL-00021BE9:1.LOW_BAT")
	if got != "Low Battery" {
		t.Errorf("got %q, want %q", got, "Low Battery")
	}
}

func TestMessageDisplayNameI18nFallback(t *testing.T) {
	t.Parallel()
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	got := messageDisplayName(cats, "en", "AL-00021BE9:1.UNKNOWN_CUSTOM_CODE")
	if got != "UNKNOWN_CUSTOM_CODE" {
		t.Errorf("got %q, want %q", got, "UNKNOWN_CUSTOM_CODE")
	}
}

func TestMessageDisplayNameNilCatalogs(t *testing.T) {
	t.Parallel()
	got := messageDisplayName(nil, "en", "AL-ADDR:1.LOW_BAT")
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
