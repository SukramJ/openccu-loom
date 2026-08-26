// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"net/http"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ListAlarmSensorCandidates enumerates the data points a zone can
// enrol as alarm sensors.
//
// Sensor enrollment was the one alarm surface without a candidate list:
// outputs and remote keys had one, sensors were unvalidated free text
// over (central, interface, channel address, parameter). A typo
// produced a sensor that silently never fired, and the raw
// smoke-detector status was as easy to pick as the derived boolean that
// actually belongs there.
//
// ?enrolled=false hides data points already taken by a zone.
func ListAlarmSensorCandidates(p AlarmPanel, labels ParameterLabeler) http.HandlerFunc {
	vl, _ := labels.(ChannelTypedValueLabeler)
	translate := func(channelType string, parameter hmenum.Parameter, values []string) []string {
		if vl == nil || len(values) == 0 {
			return nil
		}
		out := make([]string, len(values))
		for i, v := range values {
			out[i] = vl.ChannelTypedValueLabel(channelType, string(parameter), v)
		}
		return out
	}
	return func(w http.ResponseWriter, r *http.Request) {
		hideEnrolled := r.URL.Query().Get("enrolled") == "false"
		rows := p.SensorCandidates(r.Context())
		out := make([]hmapi.AlarmSensorCandidate, 0, len(rows))
		for i := range rows {
			c := &rows[i]
			if hideEnrolled && c.Enrolled {
				continue
			}
			out = append(out, hmapi.AlarmSensorCandidate{
				Central:        c.Central,
				InterfaceID:    c.InterfaceID,
				DeviceAddress:  c.DeviceAddress,
				DeviceName:     c.DeviceName,
				Model:          c.Model,
				ChannelAddress: c.ChannelAddress,
				ChannelNo:      c.ChannelNo,
				ChannelName:    c.ChannelName,
				ChannelType:    c.ChannelType,
				Parameter:      c.Parameter,
				Rooms:          c.Rooms,
				Functions:      c.Functions,
				SensorType:     string(c.SensorType),
				SecurityClass:  string(c.SecurityClass),
				ValueList:      c.ValueList,
				ValueLabels:    translate(c.ChannelType, hmenum.Parameter(c.Parameter), c.ValueList),
				ActiveValues:   c.ActiveValues,
				Recommended:    c.Recommended,
				Deprioritised:  c.Deprioritised,
				Reason:         c.Reason,
				Enrolled:       c.Enrolled,
				ZoneID:         c.ZoneID,
			})
		}
		JSON(w, http.StatusOK, out)
	}
}
