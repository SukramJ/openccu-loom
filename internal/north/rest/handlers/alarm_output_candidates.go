// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ListAlarmOutputCandidates renders the channels that can back a
// device-backed alarm output class, optionally filtered via ?class=.
// Notification and sysvar-mirror outputs are not device-backed, so
// requesting them as a filter is a client error.
//
//	GET /alarm/output-candidates?class=acoustic_siren
func ListAlarmOutputCandidates(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		class := hmenum.AlarmOutputClass(r.URL.Query().Get("class"))
		if class != "" && !alarm.DeviceBackedOutputClass(class) {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r,
					"Invalid class filter",
					"class must be a device-backed output class: "+string(class)))
			return
		}
		rows := p.OutputCandidates(class)
		out := make([]hmapi.AlarmOutputCandidate, 0, len(rows))
		for i := range rows {
			c := &rows[i]
			classes := make([]string, 0, len(c.Classes))
			for _, cl := range c.Classes {
				classes = append(classes, string(cl))
			}
			out = append(out, hmapi.AlarmOutputCandidate{
				Central:             c.Central,
				DeviceAddress:       c.DeviceAddress,
				DeviceName:          c.DeviceName,
				Model:               c.Model,
				ChannelAddress:      c.ChannelAddress,
				ChannelNo:           c.ChannelNo,
				ChannelName:         c.ChannelName,
				Classes:             classes,
				Kind:                c.Kind,
				AvailableTones:      c.AvailableTones,
				AvailableLights:     c.AvailableLights,
				AvailableSoundfiles: c.AvailableSoundfiles,
				Dimmable:            c.Dimmable,
			})
		}
		JSON(w, http.StatusOK, out)
	}
}

// ListAlarmRemoteKeyCandidates renders the physical remote/wall-button
// key channels a remote-key code binding can dispatch on.
//
//	GET /alarm/remote-key-candidates
func ListAlarmRemoteKeyCandidates(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		rows := p.RemoteKeyCandidates()
		out := make([]hmapi.AlarmRemoteKeyCandidate, 0, len(rows))
		for i := range rows {
			c := &rows[i]
			params := c.Parameters
			if params == nil {
				params = []string{}
			}
			out = append(out, hmapi.AlarmRemoteKeyCandidate{
				Central:        c.Central,
				DeviceAddress:  c.DeviceAddress,
				DeviceName:     c.DeviceName,
				Model:          c.Model,
				ChannelAddress: c.ChannelAddress,
				ChannelNo:      c.ChannelNo,
				ChannelName:    c.ChannelName,
				Parameters:     params,
			})
		}
		JSON(w, http.StatusOK, out)
	}
}
