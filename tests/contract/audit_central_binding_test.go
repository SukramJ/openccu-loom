// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/channelflags"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
)

// nopChannelFlags accepts every flag write; the test observes the change log.
type nopChannelFlags struct{}

func (nopChannelFlags) Set(context.Context, string, string, bool, bool, string) error { return nil }

// TestCentralBoundChangeIsRecordedOnThatCentral pins the change log's central
// through the production router, guard and adapter: a change made through a
// device route bound with `?central=` records that central on its row, and
// the bound history of the same address on the other CCU leaves the row out.
// Device addresses repeat across CCUs, so a row that took its central from
// the address alone showed up in both CCUs' histories. A legacy row recorded
// without a central stays in every bound history, attributed to the bound
// central as before.
func TestCentralBoundChangeIsRecordedOnThatCentral(t *testing.T) {
	const addr = "HmIP-RCV-1"
	reg := twoCentralsSharingAnAddress(t, addr)
	buf := audit.NewBuffer(16)
	buf.Replay(audit.Entry{
		Timestamp: time.Now().Add(-time.Hour), Action: audit.ActionChannelFlags,
		DeviceAddress: addr, Note: "legacy row without a central",
	})
	deps := fullyWiredRouterDeps()
	deps.Devices = adapter.NewDevicesAdapter(reg)
	deps.DeviceCentrals = reg
	deps.AuditRecorder = buf
	deps.Audit = buf
	deps.ChannelFlags = nopChannelFlags{}
	deps.ChannelFlagsOverlay = channelflags.New()
	router := rest.NewRouter(deps)

	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/devices/"+addr+"/channels/1/flags?central=ccu-02", strings.NewReader(`{"hidden":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("flag write: status %d body %.200s", rec.Code, rec.Body.String())
	}
	rows := buf.List(16)
	if len(rows) != 2 || rows[0].Central != "ccu-02" {
		t.Fatalf("newest row central = %q (rows %+v), want ccu-02", rows[0].Central, rows)
	}

	history := func(central string) map[string]string { // note → reported central
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/"+addr+"/audit?central="+central, http.NoBody)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("history ?central=%s: status %d body %.200s", central, rec.Code, rec.Body.String())
		}
		var got []struct {
			Central string `json:"central"`
			Note    string `json:"note"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := make(map[string]string, len(got))
		for _, e := range got {
			out[e.Note] = e.Central
		}
		return out
	}
	const legacy = "legacy row without a central"
	onTwo := history("ccu-02")
	if len(onTwo) != 2 || onTwo[legacy] != "ccu-02" {
		t.Errorf("ccu-02 history = %v, want its own row and the legacy row, both on ccu-02", onTwo)
	}
	onOne := history("ccu-01")
	if len(onOne) != 1 || onOne[legacy] != "ccu-01" {
		t.Errorf("ccu-01 history = %v, want only the legacy row: the change was made on ccu-02", onOne)
	}
}
