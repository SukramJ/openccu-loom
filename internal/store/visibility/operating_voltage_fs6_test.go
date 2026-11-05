// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestOperatingVoltageIgnoredForFlushMountSwitchWithoutInput pins the entry for
// HmIP-FS6, whose channel 0 is MAINTENANCE, not MAINTENANCE_BAT, and carries
// OPERATING_VOLTAGE (eQ-3 device documentation, crRFD 1.60.1, section 105). It
// suppresses the parameter like HmIP-FSM, even though OPERATING_VOLTAGE is on
// the required whitelist. Neither table entry is a prefix of HmIP-FSI6, which
// keeps the parameter.
func TestOperatingVoltageIgnoredForFlushMountSwitchWithoutInput(t *testing.T) {
	t.Parallel()

	d := NewParameterDecider(nil)
	d.SetRequiredParameters([]hmenum.Parameter{hmenum.ParameterOperatingVoltage})

	tests := []struct {
		model   string
		ignored bool
	}{
		{model: "HmIP-FS6", ignored: true},
		{model: "HmIP-FSM", ignored: true},
		{model: "HmIP-FSI6", ignored: false},
	}
	for _, tt := range tests {
		got := d.IsParameterIgnored(tt.model, "MAINTENANCE", 0, hmenum.ParamsetKeyValues, hmenum.ParameterOperatingVoltage)
		if got != tt.ignored {
			t.Errorf("%s OPERATING_VOLTAGE ignored = %v, want %v", tt.model, got, tt.ignored)
		}
	}
}
