// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// stubParamsetApplyService records its calls and answers from fields.
type stubParamsetApplyService struct {
	targets    []interfaces.ParamsetApplyTarget
	targetsErr error
	outcomes   []interfaces.ParamsetApplyOutcome

	applyCalls  int
	gotSource   string
	gotTargets  []string
	gotDryRun   bool
	gotValues   map[string]any
	targetsCall string
}

func (s *stubParamsetApplyService) ApplyTargets(_ context.Context, source string) ([]interfaces.ParamsetApplyTarget, error) {
	s.targetsCall = source
	return s.targets, s.targetsErr
}

func (s *stubParamsetApplyService) ApplyToChannels(
	_ context.Context, source string, values map[string]any, targets []string, dryRun bool,
) ([]interfaces.ParamsetApplyOutcome, error) {
	s.applyCalls++
	s.gotSource, s.gotValues, s.gotTargets, s.gotDryRun = source, values, targets, dryRun
	return s.outcomes, nil
}

func applyRequest(method, key, body string) *http.Request {
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	return req.WithContext(chiContext(req, map[string]string{"addr": "SRC:1", "key": key}))
}

func TestGetParamsetApplyTargets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		svc      *stubParamsetApplyService
		nilSvc   bool
		key      string
		wantCode int
		wantBody string
	}{
		{
			name: "lists targets in the documented shape",
			svc: &stubParamsetApplyService{targets: []interfaces.ParamsetApplyTarget{
				{
					Address: "T1:1", Name: "Blind", DeviceAddress: "T1", DeviceName: "Kitchen",
					DeviceModel: "HmIP-BROLL", InterfaceID: "HmIP-RF",
				},
				{Address: "GONE:1", InterfaceID: "HmIP-RF"},
			}},
			key:      "MASTER",
			wantCode: http.StatusOK,
			wantBody: `{"items":[{"address":"T1:1","name":"Blind","device_address":"T1","device_name":"Kitchen",` +
				`"device_model":"HmIP-BROLL","interface_id":"HmIP-RF"},{"address":"GONE:1","interface_id":"HmIP-RF"}]}`,
		},
		{
			name:     "no targets is an empty list, not null",
			svc:      &stubParamsetApplyService{},
			key:      "MASTER",
			wantCode: http.StatusOK,
			wantBody: `{"items":[]}`,
		},
		{name: "VALUES key is rejected", svc: &stubParamsetApplyService{}, key: "VALUES", wantCode: http.StatusBadRequest},
		{name: "LINK key is rejected", svc: &stubParamsetApplyService{}, key: "LINK", wantCode: http.StatusBadRequest},
		{
			name:     "source without stored description is 404",
			svc:      &stubParamsetApplyService{targetsErr: fmt.Errorf("x: %w", hmerr.ErrDescriptionNotFound)},
			key:      "MASTER",
			wantCode: http.StatusNotFound,
		},
		{name: "unwired service is 503", nilSvc: true, key: "MASTER", wantCode: http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var svc ParamsetApplyService
			if !tc.nilSvc {
				svc = tc.svc
			}
			w := httptest.NewRecorder()
			GetParamsetApplyTargets(svc).ServeHTTP(w, applyRequest(http.MethodGet, tc.key, ""))
			if w.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d; body=%s", w.Code, tc.wantCode, w.Body.String())
			}
			if tc.wantBody != "" && strings.TrimSpace(w.Body.String()) != tc.wantBody {
				t.Fatalf("body =\n %s\nwant\n %s", strings.TrimSpace(w.Body.String()), tc.wantBody)
			}
			if tc.wantCode == http.StatusOK && tc.svc.targetsCall != "SRC:1" {
				t.Fatalf("service asked for %q, want SRC:1", tc.svc.targetsCall)
			}
		})
	}
}

func TestApplyParamsetToChannels(t *testing.T) {
	t.Parallel()
	outcomes := []interfaces.ParamsetApplyOutcome{
		{
			Address: "T1:1", Status: interfaces.ApplyApplied,
			Result: &interfaces.ParamsetWriteReport{Written: []string{"LEVEL"}},
		},
		{Address: "T2:1", Status: interfaces.ApplyRefused, Reason: "MASTER description differs"},
		{Address: "T3:1", Status: interfaces.ApplyFailed, Reason: "ccu unreachable"},
	}
	cases := []struct {
		name      string
		key       string
		body      string
		token     bool
		nilSvc    bool
		wantCode  int
		wantBody  string
		wantCalls int
	}{
		{
			name:      "outcomes in the documented shape",
			key:       "MASTER",
			body:      `{"values":{"LEVEL":0.5},"targets":["T1:1","T2:1","T3:1"],"dry_run":true}`,
			token:     true,
			wantCode:  http.StatusOK,
			wantCalls: 1,
			wantBody: `{"items":[` +
				`{"address":"T1:1","status":"applied","result":{"written":["LEVEL"],"readback_divergences":[]}},` +
				`{"address":"T2:1","status":"refused","reason":"MASTER description differs"},` +
				`{"address":"T3:1","status":"failed","reason":"ccu unreachable"}]}`,
		},
		{
			name:     "without the source edit lock is 423",
			key:      "MASTER",
			body:     `{"values":{"LEVEL":0.5},"targets":["T1:1"]}`,
			wantCode: http.StatusLocked,
		},
		{
			name:     "non-MASTER key is 400",
			key:      "VALUES",
			body:     `{"values":{"LEVEL":0.5},"targets":["T1:1"]}`,
			token:    true,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "empty targets is 400",
			key:      "MASTER",
			body:     `{"values":{"LEVEL":0.5},"targets":[]}`,
			token:    true,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "empty values is 400",
			key:      "MASTER",
			body:     `{"values":{},"targets":["T1:1"]}`,
			token:    true,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "malformed JSON is 400",
			key:      "MASTER",
			body:     `{"values":`,
			token:    true,
			wantCode: http.StatusBadRequest,
		},
		{name: "unwired service is 503", nilSvc: true, key: "MASTER", wantCode: http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			locks := NewEditSessions()
			lock, ok := locks.Open("channel:SRC:1:MASTER", "test")
			if !ok {
				t.Fatal("open failed")
			}
			stub := &stubParamsetApplyService{outcomes: outcomes}
			var svc ParamsetApplyService = stub
			if tc.nilSvc {
				svc = nil
			}
			req := applyRequest(http.MethodPost, tc.key, tc.body)
			if tc.token {
				req.Header.Set(EditTokenHeader, lock.Token)
			}
			w := httptest.NewRecorder()
			ApplyParamsetToChannels(svc, locks).ServeHTTP(w, req)
			if w.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d; body=%s", w.Code, tc.wantCode, w.Body.String())
			}
			if stub.applyCalls != tc.wantCalls {
				t.Fatalf("service calls = %d, want %d", stub.applyCalls, tc.wantCalls)
			}
			if tc.wantBody == "" {
				return
			}
			if got := strings.TrimSpace(w.Body.String()); got != tc.wantBody {
				t.Fatalf("body =\n %s\nwant\n %s", got, tc.wantBody)
			}
			if stub.gotSource != "SRC:1" || !stub.gotDryRun || len(stub.gotTargets) != 3 {
				t.Fatalf("service got source=%q dryRun=%v targets=%v", stub.gotSource, stub.gotDryRun, stub.gotTargets)
			}
			var decoded map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("response is not JSON: %v", err)
			}
		})
	}
}
