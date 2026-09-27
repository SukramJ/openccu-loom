// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestFeatureUnavailableCommandError pins the WS answer for an operation
// the target central does not offer: error code feature_unavailable with
// details naming the feature — both when a handler returns the refusal
// as it is and when it builds its error through commandErr.
func TestFeatureUnavailableCommandError(t *testing.T) {
	t.Parallel()
	fe := &hmerr.FeatureUnavailableError{
		Central: "box", Feature: hmenum.FeatureHubSysvars, Reason: hmenum.FeatureReasonNotSupported,
	}
	router := NewRouter()
	router.Register("plain", func(context.Context, json.RawMessage) (any, error) {
		return nil, fmt.Errorf("list: %w", fe)
	})
	router.Register("built", func(context.Context, json.RawMessage) (any, error) {
		return nil, commandErr(CommandErrorInternal, "list_sysvars: ", fmt.Errorf("hub: %w", fe))
	})
	for _, cmd := range []string{"plain", "built"} {
		res := router.Dispatch(context.Background(), cmd, nil)
		if res.Error == nil || res.Error.Code != CommandErrorFeatureUnavailable {
			t.Errorf("%s: error = %+v, want code feature_unavailable", cmd, res.Error)
			continue
		}
		want := FeatureDetails{Central: "box", Key: "hub.sysvars", Reason: "not_supported_by_system"}
		if res.Error.Details == nil || *res.Error.Details != want {
			t.Errorf("%s: details = %+v, want %+v", cmd, res.Error.Details, want)
		}
	}
}
