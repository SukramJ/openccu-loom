// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// levelDescriptor is the MASTER descriptor the device-level write is coerced
// against — a bare device address is not in the model, so that path validates
// against the backend's descriptor before the wire call.
func levelDescriptor() map[string]hmproto.ParameterData {
	return map[string]hmproto.ParameterData{
		string(hmenum.ParameterLevel): {
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage("1.0"),
		},
	}
}

// TestParamsetWriteAuditReportsTheDeviceLevelAsSuch drives a device-level
// MASTER write through the domain and asserts the change-log row does not
// name channel 0.
//
// A bare device address carries no channel, and channel 0 is a real,
// unrelated channel on every Homematic device (MAINTENANCE). Reporting the
// device level as 0 makes the row point at the wrong object, which is the
// first thing an unexplained-configuration-change investigation reads.
func TestParamsetWriteAuditReportsTheDeviceLevelAsSuch(t *testing.T) {
	t.Parallel()

	p, _, fakeOps := buildParamsetFixture(t)
	fakeOps.getParamsetDescriptionFn = func(context.Context, string, hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
		return levelDescriptor(), nil
	}
	rec := &recordingAuditRecorder{}
	p.SetAuditRecorder(rec)

	if err := p.PutParamset(context.Background(), "0001ABCD", hmenum.ParamsetKeyMaster,
		map[string]any{string(hmenum.ParameterLevel): 0.4}); err != nil {
		t.Fatalf("PutParamset MASTER (device level): %v", err)
	}

	if len(rec.entries) != 1 {
		t.Fatalf("expected exactly one audit entry, got %d", len(rec.entries))
	}
	if got := rec.entries[0].ChannelNo; got != device.ChannelNumberDevice {
		t.Errorf("channel_no = %d, want the device level %d", got, device.ChannelNumberDevice)
	}
}

// The channel case must keep naming the channel: a parser that answered the
// device sentinel everywhere would satisfy the test above and be just as
// wrong.
func TestParamsetWriteAuditReportsTheChannelNumber(t *testing.T) {
	t.Parallel()

	p, _, _ := buildParamsetFixture(t)
	rec := &recordingAuditRecorder{}
	p.SetAuditRecorder(rec)

	if err := p.PutParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyMaster,
		map[string]any{string(hmenum.ParameterLevel): 0.4}); err != nil {
		t.Fatalf("PutParamset MASTER (channel): %v", err)
	}

	if len(rec.entries) != 1 {
		t.Fatalf("expected exactly one audit entry, got %d", len(rec.entries))
	}
	if got := rec.entries[0].ChannelNo; got != 1 {
		t.Errorf("channel_no = %d, want 1", got)
	}
}
