// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp_test

import (
	"context"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// fakeParamsetApply records its calls and answers from fields.
type fakeParamsetApply struct {
	targets  []interfaces.ParamsetApplyTarget
	outcomes []interfaces.ParamsetApplyOutcome

	targetsSource string
	applySource   string
	applyTargets  []string
	applyDryRun   bool
	applyCalls    int
}

func (f *fakeParamsetApply) ApplyTargets(_ context.Context, source string) ([]interfaces.ParamsetApplyTarget, error) {
	f.targetsSource = source
	return f.targets, nil
}

func (f *fakeParamsetApply) ApplyToChannels(
	_ context.Context, source string, _ map[string]any, targets []string, dryRun bool,
) ([]interfaces.ParamsetApplyOutcome, error) {
	f.applyCalls++
	f.applySource, f.applyTargets, f.applyDryRun = source, targets, dryRun
	return f.outcomes, nil
}

func TestListParamsetApplyTargets(t *testing.T) {
	svc := &fakeParamsetApply{targets: []interfaces.ParamsetApplyTarget{
		{Address: "ADDR003:1", DeviceName: "Blind", DeviceModel: "HmIP-BROLL", InterfaceID: "HmIP-RF"},
	}}
	devs, _, _ := makeDeviceFixture()
	cs := connect(t, mcp.Deps{
		Centrals:      &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:       devs,
		ParamsetApply: svc,
	})
	defer cs.Close()

	res := callTool(t, cs, "list_paramset_apply_targets", map[string]any{
		"central_name": "ccu1", "source_channel": "ADDR001",
	})
	if res.IsError {
		t.Fatalf("list_paramset_apply_targets returned error: %v", res.Content)
	}
	var out struct {
		Targets []struct {
			Address     string `json:"address"`
			DeviceName  string `json:"device_name"`
			DeviceModel string `json:"device_model"`
			InterfaceID string `json:"interface_id"`
		} `json:"targets"`
	}
	unmarshalStructured(t, res, &out)
	if svc.targetsSource != "ADDR001" {
		t.Fatalf("service asked for %q, want ADDR001", svc.targetsSource)
	}
	if len(out.Targets) != 1 || out.Targets[0].Address != "ADDR003:1" || out.Targets[0].DeviceModel != "HmIP-BROLL" {
		t.Fatalf("targets = %+v", out.Targets)
	}

	// The named central must own the source channel.
	res = callTool(t, cs, "list_paramset_apply_targets", map[string]any{
		"central_name": "ccu2", "source_channel": "ADDR001",
	})
	if !res.IsError {
		t.Fatal("expected an error when central_name does not own the source channel")
	}
}

func TestApplyParamsetToChannels(t *testing.T) {
	svc := &fakeParamsetApply{outcomes: []interfaces.ParamsetApplyOutcome{
		{Address: "ADDR003:1", Status: interfaces.ApplyApplied, Result: &interfaces.ParamsetWriteReport{Written: []string{"LEVEL"}}},
		{Address: "ADDR004:1", Status: interfaces.ApplyRefused, Reason: "MASTER description differs"},
	}}
	locks := &fakeEditLocks{allow: true}
	devs, _, _ := makeDeviceFixture()
	cs := connect(t, mcp.Deps{
		Centrals:      &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:       devs,
		ParamsetApply: svc,
		EditLocks:     locks,
		AllowWrites:   true,
	})
	defer cs.Close()

	res := callTool(t, cs, "apply_paramset_to_channels", map[string]any{
		"central_name":   "ccu1",
		"source_channel": "ADDR001",
		"values":         map[string]any{"LEVEL": 0.5},
		"targets":        []string{"ADDR003:1", "ADDR004:1"},
		"dry_run":        true,
		"edit_token":     "tok",
	})
	if res.IsError {
		t.Fatalf("apply_paramset_to_channels returned error: %v", res.Content)
	}
	if locks.key != "channel:ADDR001:MASTER" || locks.token != "tok" {
		t.Fatalf("lock verified as (%q, %q), want the source channel's MASTER lock", locks.key, locks.token)
	}
	if svc.applySource != "ADDR001" || !svc.applyDryRun || !slices.Equal(svc.applyTargets, []string{"ADDR003:1", "ADDR004:1"}) {
		t.Fatalf("service got source=%q dryRun=%v targets=%v", svc.applySource, svc.applyDryRun, svc.applyTargets)
	}
	var out struct {
		Outcomes []struct {
			Address string `json:"address"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Result  *struct {
				Written             []string `json:"written"`
				ReadbackDivergences []any    `json:"readback_divergences"`
			} `json:"result"`
		} `json:"outcomes"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Outcomes) != 2 {
		t.Fatalf("outcomes = %+v, want 2", out.Outcomes)
	}
	if o := out.Outcomes[0]; o.Status != interfaces.ApplyApplied || o.Result == nil || !slices.Equal(o.Result.Written, []string{"LEVEL"}) {
		t.Fatalf("outcome 0 = %+v", o)
	}
	if o := out.Outcomes[1]; o.Status != interfaces.ApplyRefused || o.Reason == "" || o.Result != nil {
		t.Fatalf("outcome 1 = %+v", o)
	}

	// Without the source lock the service is never reached.
	locks.allow = false
	res = callTool(t, cs, "apply_paramset_to_channels", map[string]any{
		"central_name":   "ccu1",
		"source_channel": "ADDR001",
		"values":         map[string]any{"LEVEL": 0.5},
		"targets":        []string{"ADDR003:1"},
	})
	if !res.IsError {
		t.Fatal("expected an error without the source edit lock")
	}
	if svc.applyCalls != 1 {
		t.Fatalf("service calls = %d, want 1 (the locked call must not reach it)", svc.applyCalls)
	}
}
