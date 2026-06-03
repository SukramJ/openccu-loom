// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"testing"
	"time"
)

func TestPublishDataPointValueChangedKind_DefaultsViaWrapper(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.PublishDataPointValueChanged("home", "HmIP-RF", "VCU123", 1, "LEVEL", "VALUES", 1.0, 0.0, time.Now())
	res := h.Replay(0, nil)
	if len(res.Events) != 1 || res.Events[0].Kind != KindChange {
		t.Fatalf("default-wrapper Kind = %q, want change", res.Events[0].Kind)
	}
}

func TestPublishDataPointValueChangedKind_InitialPropagates(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.PublishDataPointValueChangedKind(KindInitial, "home", "HmIP-RF", "VCU123", 1, "LEVEL", "VALUES", 1.0, nil, time.Now(), "", "", "")
	res := h.Replay(0, nil)
	if len(res.Events) != 1 || res.Events[0].Kind != KindInitial {
		t.Fatalf("Kind = %q, want initial", res.Events[0].Kind)
	}
	if res.Events[0].Type != "datapoint.value_changed" {
		t.Fatalf("Type = %q", res.Events[0].Type)
	}
}

func TestPublishCustomDataPointStateChangedKind_RefreshPropagates(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.PublishCustomDataPointStateChangedKind(KindRefresh, "home", "VCU123", 1,
		"main", "light", map[string]any{"on": true}, time.Now(), "")
	res := h.Replay(0, nil)
	if len(res.Events) != 1 || res.Events[0].Kind != KindRefresh {
		t.Fatalf("Kind = %q, want refresh", res.Events[0].Kind)
	}
}

func TestPublishCustomDataPointStateChangedKind_DefaultViaWrapper(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.PublishCustomDataPointStateChanged("home", "VCU123", 1, "main", "light",
		map[string]any{"on": true}, time.Now())
	res := h.Replay(0, nil)
	if len(res.Events) != 1 || res.Events[0].Kind != KindChange {
		t.Fatalf("default Kind = %q, want change", res.Events[0].Kind)
	}
}
