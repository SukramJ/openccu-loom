// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client_test

import (
	"context"
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestPutParamsetWithOptionsRecordsEverySentValue pins that a paramset
// write reaches the command-tracker hook the way a single SetValue does —
// once per parameter, with the paramset key it was written under. The
// reference records a paramset write the same way (add_put_paramset);
// skipping it here left a paramset write with no tracker entry for the
// CCU's echo to clear.
func TestPutParamsetWithOptionsRecordsEverySentValue(t *testing.T) {
	t.Parallel()
	w := client.NewValueWriter()
	w.Register("ccu", "HmIP-RF", &paramsetDispatchBackend{orchBackend: &orchBackend{}})

	type rec struct {
		param hmenum.Parameter
		key   hmenum.ParamsetKey
		value any
	}
	var got []rec
	w.SetCommandTrackerFn(func(_, channelAddress string, parameter hmenum.Parameter, paramsetKey hmenum.ParamsetKey, value any) {
		if channelAddress != "DEV0001:3" {
			t.Errorf("channel = %q, want DEV0001:3", channelAddress)
		}
		got = append(got, rec{parameter, paramsetKey, value})
	})

	values := map[string]any{"LEVEL": 0.5, "RAMP_TIME": 2.0}
	if err := w.PutParamsetWithOptions(context.Background(), "ccu", "HmIP-RF", "DEV0001:3", hmenum.ParamsetKeyValues, values, client.WriteOptions{}); err != nil {
		t.Fatalf("PutParamsetWithOptions: %v", err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].param < got[j].param })
	if len(got) != 2 || got[0].param != "LEVEL" || got[0].key != hmenum.ParamsetKeyValues || got[0].value != 0.5 ||
		got[1].param != "RAMP_TIME" || got[1].value != 2.0 {
		t.Fatalf("tracker hook saw %+v, want one call per parameter under VALUES", got)
	}
}
