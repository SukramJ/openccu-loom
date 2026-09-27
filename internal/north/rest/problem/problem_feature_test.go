// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package problem

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestWriteFeatureUnavailable pins the feature_unavailable answer: 422,
// the problem code in the header and the body, and the feature member
// naming the central, the key, the reason and the missing scope — also
// when the refusal arrives wrapped. Any other error is left to the caller.
func TestWriteFeatureUnavailable(t *testing.T) {
	t.Parallel()
	fe := &hmerr.FeatureUnavailableError{
		Central: "box", Feature: hmenum.FeatureSystemReboot,
		Reason: hmenum.FeatureReasonMissingScope, Scope: "power", Legacy: errors.New("legacy"),
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/ccu/reboot", http.NoBody)
	if !WriteFeatureUnavailable(rr, req, fmt.Errorf("reboot: %w", fe)) {
		t.Fatal("a wrapped FeatureUnavailableError was not written")
	}
	if rr.Code != http.StatusUnprocessableEntity || rr.Header().Get("X-Problem-Code") != "feature_unavailable" {
		t.Errorf("status %d, code %q", rr.Code, rr.Header().Get("X-Problem-Code"))
	}
	var d Details
	if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
		t.Fatalf("body: %v", err)
	}
	want := FeatureRef{Central: "box", Key: "system.reboot", Reason: "missing_scope", Scope: "power"}
	if d.Feature == nil || *d.Feature != want || d.Code != "feature_unavailable" {
		t.Errorf("problem = %+v, feature %+v; want %+v", d, d.Feature, want)
	}

	other := httptest.NewRecorder()
	if WriteFeatureUnavailable(other, req, errors.New("boom")) || other.Body.Len() != 0 {
		t.Error("an unrelated error was written as feature_unavailable")
	}
}
