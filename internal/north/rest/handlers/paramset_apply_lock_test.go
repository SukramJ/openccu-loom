// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// TestApplyParamsetToChannels_RefusesLockedTargets pins the target-lock
// filter: a target whose MASTER edit lock is held by an open edit session is
// refused in place — the service never sees it — while unlocked targets go
// through, and the response keeps the request order.
func TestApplyParamsetToChannels_RefusesLockedTargets(t *testing.T) {
	t.Parallel()
	locks := NewEditSessions()
	lock, ok := locks.Open("channel:SRC:1:MASTER", "test")
	if !ok {
		t.Fatal("open source lock failed")
	}
	if _, ok := locks.Open("channel:TGT:2:MASTER", "someone-else"); !ok {
		t.Fatal("open target lock failed")
	}
	stub := &stubParamsetApplyService{outcomes: []interfaces.ParamsetApplyOutcome{
		{Address: "TGT:1", Status: interfaces.ApplyApplied},
	}}
	body := `{"values":{"X":1},"targets":["TGT:1","TGT:2"]}`
	req := applyRequest(http.MethodPost, "MASTER", body)
	req.Header.Set(EditTokenHeader, lock.Token)
	w := httptest.NewRecorder()
	ApplyParamsetToChannels(stub, locks).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, body=%s", w.Code, w.Body.String())
	}
	if len(stub.gotTargets) != 1 || stub.gotTargets[0] != "TGT:1" {
		t.Fatalf("service targets = %v, want [TGT:1] only", stub.gotTargets)
	}
	var resp struct {
		Items []struct {
			Address string `json:"address"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 2 || resp.Items[0].Address != "TGT:1" || resp.Items[1].Address != "TGT:2" {
		t.Fatalf("items = %+v, want TGT:1 then TGT:2", resp.Items)
	}
	if resp.Items[0].Status != interfaces.ApplyApplied {
		t.Fatalf("TGT:1 status = %s, want applied", resp.Items[0].Status)
	}
	if resp.Items[1].Status != interfaces.ApplyRefused ||
		!strings.Contains(resp.Items[1].Reason, "open edit session") {
		t.Fatalf("TGT:2 = %+v, want refused with the open-edit-session reason", resp.Items[1])
	}
}
