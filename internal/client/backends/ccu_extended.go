// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Extended Operations implementations for CcuBackend — install mode,
// service/alarm messages, rooms, functions, renaming, inbox, programs,
// system variables, bulk device data, backup, and firmware trigger.

package backends

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// maxDownloadResponseSize bounds how much of a firmware image or backup
// archive response body downloadBackup/DownloadFirmware will buffer.
// Without a limit, a misbehaving proxy or compromised CCU could stream an
// unbounded response and exhaust process memory. 512 MiB is generous for
// both a firmware image and a full CCU backup (real-world archives run to
// a few tens of MB) while still bounding worst-case memory use. Declared
// as a var rather than a const so tests can lower it to exercise the
// overflow path without generating a multi-hundred-MB fixture.
var maxDownloadResponseSize int64 = 512 * 1024 * 1024

// readLimitedResponse reads r up to limit+1 bytes and returns an error if
// the body exceeds limit, instead of buffering an unbounded amount of
// memory for a response of unknown size.
func readLimitedResponse(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d byte limit", limit)
	}
	return data, nil
}

// --- install mode -------------------------------------------------------

// GetInstallMode implements Operations. Returns the remaining seconds
// the CCU is in install (pairing) mode via JSON-RPC.
func (b *CcuBackend) GetInstallMode(ctx context.Context) (int, error) {
	if b.json == nil {
		return 0, ErrUnsupported
	}
	raw, err := b.json.Call(ctx, "Interface.getInstallMode")
	if err != nil {
		return 0, err
	}
	switch v := raw.(type) {
	case int:
		return v, nil
	case float64:
		return int(v), nil
	}
	return 0, nil
}

// SetInstallMode implements Operations. Enables or disables CCU pairing mode.
//
// HmIP-RF uses Interface.setInstallModeHMIP via JSON-RPC (named params).
// BidCos-RF and BidCos-Wired use setInstallMode via XML-RPC with positional
// arguments: (on, time, deviceAddress) when a device address is given, or
// (on, time, mode) otherwise.
func (b *CcuBackend) SetInstallMode(ctx context.Context, on bool, duration, mode int, deviceAddress string) error {
	if b.ifaceType == hmenum.InterfaceHmIPRF {
		if b.json == nil {
			return ErrUnsupported
		}
		onStr := "false"
		if on {
			onStr = "true"
		}
		params := map[string]any{
			"interface":   string(b.ifaceType),
			"on":          onStr,
			"time":        duration,
			"installMode": "ALL",
			"address":     deviceAddress,
			"key":         "",
			"keymode":     "",
		}
		_, err := b.json.Call(ctx, "Interface.setInstallModeHMIP", params)
		return err
	}
	if b.xml == nil {
		return ErrUnsupported
	}
	if deviceAddress != "" {
		_, err := b.xml.Call(ctx, "setInstallMode", on, duration, deviceAddress)
		return err
	}
	_, err := b.xml.Call(ctx, "setInstallMode", on, duration, mode)
	return err
}

// --- service / alarm messages -------------------------------------------

// GetServiceMessages implements Operations. Returns all active service
// messages from the CCU via the get_service_messages ReGa script.
//
// When a ScriptRunner is wired in (via [CcuBackend.SetScriptRunner]), the
// call goes through the ReGa engine. Without it the backend falls back to
// a direct JSON-RPC call, which may not be supported on all CCU firmware
// versions. The messageType filter is applied client-side on the ReGa path
// because the script returns all messages unconditionally.
func (b *CcuBackend) GetServiceMessages(ctx context.Context, messageType string) ([]map[string]any, error) {
	if b.rega != nil {
		type svcMsg struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Timestamp     string `json:"timestamp"`
			Type          int    `json:"type"`
			Address       string `json:"address"`
			DeviceName    string `json:"device_name"`
			LastTimestamp string `json:"last_timestamp"`
			Counter       int    `json:"counter"`
			Rooms         string `json:"rooms"`
			Functions     string `json:"functions"`
			Quittable     bool   `json:"quittable"`
		}
		var msgs []svcMsg
		if err := b.rega.RunJSON(ctx, hmenum.RegaScriptGetServiceMessages, nil, &msgs); err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(msgs))
		for i := range msgs {
			m := &msgs[i]
			if messageType != "" && strconv.Itoa(m.Type) != messageType {
				continue
			}
			out = append(out, map[string]any{
				"id":             m.ID,
				"name":           m.Name,
				"timestamp":      m.Timestamp,
				"type":           m.Type,
				"address":        m.Address,
				"device_name":    m.DeviceName,
				"last_timestamp": m.LastTimestamp,
				"counter":        m.Counter,
				"rooms":          m.Rooms,
				"functions":      m.Functions,
				"quittable":      m.Quittable,
			})
		}
		return out, nil
	}
	if b.json == nil {
		return nil, ErrUnsupported
	}
	// wire:inline reason=rega-preferred-fallback: "Message.getAll" is used only
	// when no ScriptRunner is wired; the canonical path is the ReGa script above.
	var raw any
	var err error
	if messageType != "" {
		raw, err = b.json.Call(ctx, "Message.getAll", map[string]any{"type": messageType})
	} else {
		raw, err = b.json.Call(ctx, "Message.getAll")
	}
	if err != nil {
		return nil, err
	}
	return toSliceOfMaps(raw, "GetServiceMessages")
}

// SuppressServiceMessage implements Operations. Suppresses or
// unsuppresses a service message for a channel via JSON-RPC.
//
// Wire: Interface.suppressServiceMessages, params: {interface, channelAddress,
// parameterId, suppress}. Mirrors json_rpc.py suppress_service_message.
func (b *CcuBackend) SuppressServiceMessage(ctx context.Context, channelAddress, parameterID string, suppress bool) error {
	if b.json == nil {
		return ErrUnsupported
	}
	_, err := b.json.Call(ctx, "Interface.suppressServiceMessages", map[string]any{
		"interface":      string(b.ifaceType),
		"channelAddress": channelAddress,
		"parameterId":    parameterID,
		"suppress":       suppress,
	})
	return err
}

// GetAlarmMessages implements Operations. Returns all active alarm messages
// from the CCU via the get_alarm_messages ReGa script — the reference stack
// reads alarms the same way; there is no JSON-RPC alarm method on the CCU.
// Without a ScriptRunner alarm messages are unavailable.
func (b *CcuBackend) GetAlarmMessages(ctx context.Context) ([]map[string]any, error) {
	if b.rega == nil {
		return nil, ErrUnsupported
	}
	type alarmMsg struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		DeviceName    string `json:"device_name"`
		Timestamp     string `json:"timestamp"`
		LastTimestamp string `json:"last_timestamp"`
		Counter       int    `json:"counter"`
		LastTrigger   string `json:"last_trigger"`
		Rooms         string `json:"rooms"`
	}
	var msgs []alarmMsg
	if err := b.rega.RunJSON(ctx, hmenum.RegaScriptGetAlarmMessages, nil, &msgs); err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(msgs))
	for i := range msgs {
		m := &msgs[i]
		out = append(out, map[string]any{
			"id":             m.ID,
			"name":           m.Name,
			"description":    m.Description,
			"device_name":    m.DeviceName,
			"timestamp":      m.Timestamp,
			"last_timestamp": m.LastTimestamp,
			"counter":        m.Counter,
			"last_trigger":   m.LastTrigger,
			"rooms":          m.Rooms,
		})
	}
	return out, nil
}

// --- BidCos interfaces --------------------------------------------------

// ListBidcosInterfaces returns the BidCos gateways attached to the named
// interface together with their radio-utilisation state. It is a
// read-only JSON-RPC query and generates no radio traffic. Each returned
// map carries the CCU-defined keys "address", "description", "dutyCycle",
// "isConnected", "isDefault", "fwVersion", and "type".
//
// Wire: Interface.listBidcosInterfaces, params: {interface: iface}.
func (b *CcuBackend) ListBidcosInterfaces(ctx context.Context, iface string) ([]map[string]any, error) {
	if b.json == nil {
		return nil, ErrUnsupported
	}
	raw, err := b.json.Call(ctx, "Interface.listBidcosInterfaces", map[string]any{"interface": iface})
	if err != nil {
		return nil, err
	}
	return toSliceOfMaps(raw, "ListBidcosInterfaces")
}

// --- rooms / functions --------------------------------------------------

// GetAllRooms implements Operations. Returns a map of roomName →
// []channelAddress via JSON-RPC.
func (b *CcuBackend) GetAllRooms(ctx context.Context) (map[string][]string, error) {
	if b.json == nil {
		return nil, ErrUnsupported
	}
	raw, err := b.json.Call(ctx, "Room.getAll")
	if err != nil {
		return nil, err
	}
	return extractGroupMap(raw, "GetAllRooms")
}

// GetAllFunctions implements Operations. Returns a map of functionName →
// []channelAddress via JSON-RPC.
func (b *CcuBackend) GetAllFunctions(ctx context.Context) (map[string][]string, error) {
	if b.json == nil {
		return nil, ErrUnsupported
	}
	raw, err := b.json.Call(ctx, "Subsection.getAll")
	if err != nil {
		return nil, err
	}
	return extractGroupMap(raw, "GetAllFunctions")
}

// extractGroupMap converts the CCU's Room/Subsection response into a
// map of name → channelAddresses.
func extractGroupMap(raw any, method string) (map[string][]string, error) {
	list, err := toSliceOfMaps(raw, method)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(list))
	for _, item := range list {
		name, _ := item["name"].(string)
		if name == "" {
			continue
		}
		if channels, ok := item["channels"].([]any); ok {
			for _, ch := range channels {
				if addr, ok := ch.(string); ok && addr != "" {
					out[name] = append(out[name], addr)
				}
			}
		}
	}
	return out, nil
}

// --- renaming -----------------------------------------------------------

// RenameDevice implements Operations. Renames a CCU device identified
// by ISE-ID via JSON-RPC.
func (b *CcuBackend) RenameDevice(ctx context.Context, iseID int, newName string) (bool, error) {
	if b.json == nil {
		return false, ErrUnsupported
	}
	_, err := b.json.Call(ctx, "Device.setName", map[string]any{
		"id":   strconv.Itoa(iseID),
		"name": newName,
	})
	return err == nil, err
}

// RenameChannel implements Operations. Renames a CCU channel identified
// by ISE-ID via JSON-RPC.
func (b *CcuBackend) RenameChannel(ctx context.Context, iseID int, newName string) (bool, error) {
	if b.json == nil {
		return false, ErrUnsupported
	}
	_, err := b.json.Call(ctx, "Channel.setName", map[string]any{
		"id":   strconv.Itoa(iseID),
		"name": newName,
	})
	return err == nil, err
}

// --- inbox --------------------------------------------------------------

// AcceptDeviceInInbox implements Operations. Accepts a discovered device
// from the CCU pairing inbox.
//
// When a ScriptRunner is wired in (via [CcuBackend.SetScriptRunner]), the
// operation runs the accept_device_in_inbox ReGa script. Without a
// ScriptRunner the backend falls back to a direct JSON-RPC call.
func (b *CcuBackend) AcceptDeviceInInbox(ctx context.Context, deviceAddress string) (bool, error) {
	if b.rega != nil {
		var resp struct {
			Success bool   `json:"success"`
			Error   string `json:"error"`
		}
		if err := b.rega.RunJSON(ctx, hmenum.RegaScriptAcceptDeviceInInbox,
			map[string]string{"device_address": deviceAddress}, &resp); err != nil {
			return false, err
		}
		if !resp.Success && resp.Error != "" {
			return false, fmt.Errorf("ccu.AcceptDeviceInInbox(%s): %s", deviceAddress, resp.Error)
		}
		return resp.Success, nil
	}
	if b.json == nil {
		return false, ErrUnsupported
	}
	// wire:inline reason=rega-preferred-fallback: "Interface.acceptDevice" is used only
	// when no ScriptRunner is wired; the canonical path is the ReGa script above.
	_, err := b.json.Call(ctx, "Interface.acceptDevice", map[string]any{
		"address": deviceAddress,
	})
	return err == nil, err
}

// --- programs -----------------------------------------------------------

// ExecuteProgram implements Operations. Triggers a CCU program by ISE-ID
// via JSON-RPC.
func (b *CcuBackend) ExecuteProgram(ctx context.Context, iseID string) (bool, error) {
	if b.json == nil {
		return false, ErrUnsupported
	}
	_, err := b.json.Call(ctx, "Program.execute", map[string]any{
		"id": iseID,
	})
	return err == nil, err
}

// --- system variables ---------------------------------------------------

// GetSystemVariable implements Operations. Returns the value of a
// single system variable by name via JSON-RPC.
func (b *CcuBackend) GetSystemVariable(ctx context.Context, name string) (any, error) {
	if b.json == nil {
		return nil, ErrUnsupported
	}
	raw, err := b.json.Call(ctx, "SysVar.getValueByName", map[string]any{
		"name": name,
	})
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// GetAllSystemVariables implements Operations. Returns all CCU system
// variables as raw maps via JSON-RPC.
func (b *CcuBackend) GetAllSystemVariables(ctx context.Context) ([]map[string]any, error) {
	if b.json == nil {
		return nil, ErrUnsupported
	}
	raw, err := b.json.Call(ctx, "SysVar.getAll")
	if err != nil {
		return nil, err
	}
	return toSliceOfMaps(raw, "GetAllSystemVariables")
}

// --- bulk device data ---------------------------------------------------

// GetAllDeviceData implements Operations. Fetches all current data-point
// values for all devices on the interface. Returns a map of
// dpName → value.
//
// When a ScriptRunner is wired in (via [CcuBackend.SetScriptRunner]), the
// operation runs the fetch_all_device_data ReGa script, which requires the
// interface name as a parameter. Without a ScriptRunner the backend falls
// back to a direct JSON-RPC call.
func (b *CcuBackend) GetAllDeviceData(ctx context.Context) (map[string]map[string]any, error) {
	if b.rega != nil {
		// The ReGa script returns a flat {"DPName": value, ...} map where
		// the keys are dot-separated DP names (e.g. "HmIP-RF.ADDR:1.LEVEL").
		// We re-wrap each entry as map[string]any so callers get a uniform
		// two-level structure keyed by the full DP name.
		var flat map[string]any
		if err := b.rega.RunJSON(ctx, hmenum.RegaScriptFetchAllDeviceData,
			map[string]string{"interface": string(b.ifaceType)}, &flat); err != nil {
			return nil, err
		}
		out := make(map[string]map[string]any, len(flat))
		for k, v := range flat {
			out[k] = map[string]any{"value": v}
		}
		return out, nil
	}
	if b.json == nil {
		return nil, ErrUnsupported
	}
	// wire:inline reason=rega-preferred-fallback: "Interface.getAllDeviceData" is used only
	// when no ScriptRunner is wired; the canonical path is the ReGa script above.
	raw, err := b.json.Call(ctx, "Interface.getAllDeviceData")
	if err != nil {
		return nil, err
	}
	outer, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ccu.GetAllDeviceData: unexpected type %T", raw)
	}
	out := make(map[string]map[string]any, len(outer))
	for addr, inner := range outer {
		if m, ok := inner.(map[string]any); ok {
			out[addr] = m
		}
	}
	return out, nil
}

// GetDeviceDetails implements Operations. Returns name / ISE-ID /
// interface details for all devices via JSON-RPC. addresses is ignored
// for the CCU backend (CCU returns all at once).
func (b *CcuBackend) GetDeviceDetails(ctx context.Context, _ []string) ([]map[string]any, error) {
	if b.json == nil {
		return nil, ErrUnsupported
	}
	raw, err := b.json.Call(ctx, "Device.listAllDetail")
	if err != nil {
		return nil, err
	}
	return toSliceOfMaps(raw, "GetDeviceDetails")
}

// GetDeviceDescription implements Operations. Returns the raw device
// description for a single address via XML-RPC.
func (b *CcuBackend) GetDeviceDescription(ctx context.Context, address string) (map[string]any, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	raw, err := b.xml.Call(ctx, "getDeviceDescription", address)
	if err != nil {
		return nil, err
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ccu.GetDeviceDescription: unexpected type %T", raw)
	}
	return m, nil
}

// --- backup -------------------------------------------------------------

// backupStatus mirrors the JSON object returned by the
// create_backup_status ReGa script.
type backupStatus struct {
	Status   string `json:"status"`
	File     string `json:"file"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

// backupStart mirrors the JSON object returned by the create_backup_start
// ReGa script.
type backupStart struct {
	Success bool   `json:"success"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

const (
	backupStatusRunning   = "running"
	backupStatusCompleted = "completed"
	backupStatusFailed    = "failed"
	backupStatusIdle      = "idle"
)

// CreateBackupAndDownload implements Operations. It starts a CCU config
// backup in the background via the create_backup_start ReGa script, polls
// create_backup_status until the archive is ready, then downloads the
// `.sbk` archive over HTTP from the CCU's cp_security.cgi maintenance
// endpoint. Returns the raw archive bytes. maxWaitTime and pollInterval
// are in seconds; zero values fall back to 300 s / 5 s.
//
// Requires both a ReGa ScriptRunner (SetScriptRunner) for the start/status
// scripts and the HTTP download transport (SetDownloadFirmwareTransport)
// for the archive fetch. Mirrors the reference create_backup_and_download
// flow (start → poll status → download_backup via cp_security.cgi).
func (b *CcuBackend) CreateBackupAndDownload(ctx context.Context, maxWaitTime, pollInterval float64) ([]byte, error) {
	if b.rega == nil {
		return nil, ErrUnsupported
	}
	if b.baseURL == "" || b.sessionIDFn == nil {
		return nil, fmt.Errorf("ccu.CreateBackupAndDownload: HTTP download transport not wired: %w", ErrUnsupported)
	}
	if maxWaitTime <= 0 {
		maxWaitTime = 300
	}
	if pollInterval <= 0 {
		pollInterval = 5
	}

	// 1. Start the backup process in the background.
	var start backupStart
	if err := b.rega.RunJSON(ctx, hmenum.RegaScriptCreateBackupStart, nil, &start); err != nil {
		return nil, fmt.Errorf("ccu.CreateBackupAndDownload: start: %w", err)
	}
	if !start.Success {
		msg := start.Message
		if msg == "" {
			msg = start.Status
		}
		return nil, fmt.Errorf("ccu.CreateBackupAndDownload: start failed: %s", msg)
	}

	// 2. Poll create_backup_status until completion, failure, or timeout.
	deadline := time.Now().Add(time.Duration(maxWaitTime * float64(time.Second)))
	ticker := time.NewTicker(time.Duration(pollInterval * float64(time.Second)))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}

		var status backupStatus
		if err := b.rega.RunJSON(ctx, hmenum.RegaScriptCreateBackupStatus, nil, &status); err != nil {
			return nil, fmt.Errorf("ccu.CreateBackupAndDownload: status: %w", err)
		}

		switch status.Status {
		case backupStatusCompleted:
			return b.downloadBackup(ctx)
		case backupStatusFailed:
			return nil, errors.New("ccu.CreateBackupAndDownload: backup failed on CCU")
		case backupStatusIdle:
			return nil, errors.New("ccu.CreateBackupAndDownload: unexpected idle status (backup not running)")
		case backupStatusRunning:
			// keep polling
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("ccu.CreateBackupAndDownload: timeout after %.0fs", maxWaitTime)
		}
	}
}

// downloadBackup fetches a freshly created backup archive from the CCU's
// cp_security.cgi maintenance endpoint. The CGI both creates and streams the
// archive in one GET. It authenticates by session id wrapped in `@` symbols
// (sid=@SESSION_ID@).
//
// Critically, the CGI answers an unauthenticated request with HTTP 200 + an
// HTML login page (not a 401), so a stale session would otherwise be saved as
// a bogus "backup". To avoid that we force a fresh login first and reject any
// non-archive response.
func (b *CcuBackend) downloadBackup(ctx context.Context) ([]byte, error) {
	sid, err := b.backupSessionID(ctx)
	if err != nil {
		return nil, err
	}
	if sid == "" {
		return nil, fmt.Errorf("ccu.CreateBackupAndDownload: no active JSON-RPC session: %w", ErrUnsupported)
	}

	// The session delimiter @ stays literal (the CGI matches sid=@SID@
	// verbatim); url.Values.Encode would percent-encode it.
	downloadURL := strings.TrimRight(b.baseURL, "/") +
		"/config/cp_security.cgi?sid=@" + sid + "@&action=create_backup"

	hc := b.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: firmwareDownloadTimeout}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("ccu.CreateBackupAndDownload: build download request: %w", err)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ccu.CreateBackupAndDownload: download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("ccu.CreateBackupAndDownload: CCU returned HTTP %d", resp.StatusCode)
	}

	content, err := readLimitedResponse(resp.Body, maxDownloadResponseSize)
	if err != nil {
		return nil, fmt.Errorf("ccu.CreateBackupAndDownload: read archive: %w", err)
	}
	// An empty body or an HTML page means the CGI rejected the session (it
	// returns the login page under HTTP 200 instead of a 401). Treat both as
	// a failure rather than persisting a worthless 0-byte / HTML "backup".
	if len(content) == 0 {
		return nil, errors.New("ccu.CreateBackupAndDownload: empty archive (session rejected by cp_security.cgi)")
	}
	if looksLikeHTML(content) {
		return nil, errors.New("ccu.CreateBackupAndDownload: CCU returned an HTML page, not an archive (session rejected)")
	}
	return content, nil
}

// backupSessionID returns a session id for the backup download, preferring a
// freshly renewed session (sessionRenewFn) over the current one (sessionIDFn).
func (b *CcuBackend) backupSessionID(ctx context.Context) (string, error) {
	if b.sessionRenewFn != nil {
		sid, err := b.sessionRenewFn(ctx)
		if err != nil {
			return "", fmt.Errorf("ccu.CreateBackupAndDownload: renew session: %w", err)
		}
		return sid, nil
	}
	if b.sessionIDFn != nil {
		return b.sessionIDFn(), nil
	}
	return "", nil
}

// looksLikeHTML reports whether data begins with an HTML/XML document marker,
// after skipping leading whitespace. Used to detect the CCU login page the
// maintenance CGI serves under HTTP 200 when the session is invalid.
func looksLikeHTML(data []byte) bool {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	for _, prefix := range [][]byte{[]byte("<!DOCTYPE"), []byte("<!doctype"), []byte("<html"), []byte("<HTML"), []byte("<?xml")} {
		if bytes.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// --- trigger firmware update -------------------------------------------

// TriggerFirmwareUpdate implements Operations. Triggers a CCU software
// update.
//
// When a ScriptRunner is wired in (via [CcuBackend.SetScriptRunner]), the
// operation runs the trigger_firmware_update ReGa script, which calls the
// CCU's checkFirmwareUpdate.sh with -a -r flags (apply + reboot).
// Without a ScriptRunner the backend falls back to a direct JSON-RPC call.
func (b *CcuBackend) TriggerFirmwareUpdate(ctx context.Context) (bool, error) {
	if b.rega != nil {
		var resp struct {
			Success         bool   `json:"success"`
			ScriptAvailable bool   `json:"script_available"`
			Message         string `json:"message"`
		}
		if err := b.rega.RunJSON(ctx, hmenum.RegaScriptTriggerFirmwareUpdate, nil, &resp); err != nil {
			return false, err
		}
		return resp.Success, nil
	}
	if b.json == nil {
		return false, ErrUnsupported
	}
	// wire:inline reason=rega-preferred-fallback: "System.runFirmwareUpdate" is used only
	// when no ScriptRunner is wired; the canonical path is the ReGa script above.
	_, err := b.json.Call(ctx, "System.runFirmwareUpdate")
	return err == nil, err
}

// --- reboot CCU --------------------------------------------------------

// RebootCCU reboots the CCU. It runs the reboot_ccu ReGa script, which
// persists runtime state (system.Save) and then triggers /sbin/reboot in
// the background. Requires a wired ScriptRunner (via
// [CcuBackend.SetScriptRunner]); without one it returns [ErrUnsupported]
// because no JSON-RPC method reboots the box.
//
// Modelled on [CcuBackend.TriggerFirmwareUpdate].
func (b *CcuBackend) RebootCCU(ctx context.Context) (bool, error) {
	if b.rega == nil {
		return false, ErrUnsupported
	}
	var resp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := b.rega.RunJSON(ctx, hmenum.RegaScriptRebootCCU, nil, &resp); err != nil {
		return false, err
	}
	return resp.Success, nil
}

// --- system variable deletion ------------------------------------------

// DeleteSystemVariable implements Operations. Deletes a CCU system variable
// by name via JSON-RPC.
//
// Wire: SysVar.deleteSysVarByName, params: {name}. Mirrors
// json_rpc.py delete_system_variable.
func (b *CcuBackend) DeleteSystemVariable(ctx context.Context, name string) (bool, error) {
	if b.json == nil {
		return false, ErrUnsupported
	}
	_, err := b.json.Call(ctx, "SysVar.deleteSysVarByName", map[string]any{"name": name})
	return err == nil, err
}

// --- ISE-ID lookup ----------------------------------------------------

// GetIseIDByAddress implements Operations. Resolves a device or channel
// address to its ReGa ISE-ID via JSON-RPC.
func (b *CcuBackend) GetIseIDByAddress(ctx context.Context, address string) (int, error) {
	if b.json == nil {
		return 0, ErrUnsupported
	}
	raw, err := b.json.Call(ctx, "Interface.getIseIDByAddress", map[string]any{
		"address": address,
	})
	if err != nil {
		return 0, err
	}
	switch v := raw.(type) {
	case int:
		return v, nil
	case float64:
		return int(v), nil
	}
	return 0, nil
}

// --- link info ---------------------------------------------------------

// GetLinkInfo implements Operations. Returns the name and description
// of the direct link between senderAddress and receiverAddress on iface
// via JSON-RPC.
//
// Wire: Interface.getLinkInfo, params: {interface, senderAddress,
// receiverAddress}. Mirrors json_rpc.py get_link_info.
func (b *CcuBackend) GetLinkInfo(ctx context.Context, iface, senderAddress, receiverAddress string) (map[string]any, error) {
	if b.json == nil {
		return nil, ErrUnsupported
	}
	raw, err := b.json.Call(ctx, "Interface.getLinkInfo", map[string]any{
		"interface":       iface,
		"senderAddress":   senderAddress,
		"receiverAddress": receiverAddress,
	})
	if err != nil {
		return nil, err
	}
	m, _ := raw.(map[string]any)
	return m, nil
}

// SetLinkInfo implements Operations. Sets the name and description of a
// direct link via JSON-RPC.
//
// Wire: Interface.setLinkInfo, params: {interface, senderAddress,
// receiverAddress, name, description}. Mirrors json_rpc.py set_link_info.
func (b *CcuBackend) SetLinkInfo(ctx context.Context, iface, senderAddress, receiverAddress, name, description string) (bool, error) {
	if b.json == nil {
		return false, ErrUnsupported
	}
	_, err := b.json.Call(ctx, "Interface.setLinkInfo", map[string]any{
		"interface":       iface,
		"senderAddress":   senderAddress,
		"receiverAddress": receiverAddress,
		"name":            name,
		"description":     description,
	})
	return err == nil, err
}

// --- suppressed service messages ---------------------------------------

// GetSuppressedServiceMessages implements Operations. Returns the list of
// currently suppressed service message parameter IDs for channelAddress on
// iface via JSON-RPC.
//
// Wire: Interface.getSuppressedServiceMessages, params: {interface,
// channelAddress}. Mirrors json_rpc.py get_suppressed_service_messages.
func (b *CcuBackend) GetSuppressedServiceMessages(ctx context.Context, iface, channelAddress string) ([]string, error) {
	if b.json == nil {
		return nil, ErrUnsupported
	}
	raw, err := b.json.Call(ctx, "Interface.getSuppressedServiceMessages", map[string]any{
		"interface":      iface,
		"channelAddress": channelAddress,
	})
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, nil
	}
	out := make([]string, 0, len(list))
	for _, entry := range list {
		if s, ok := entry.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// --- programs ----------------------------------------------------------

// HasProgramIDs implements Operations. Reports whether the CCU program
// identified by iseID exists via JSON-RPC.
func (b *CcuBackend) HasProgramIDs(ctx context.Context, iseID string) (bool, error) {
	if b.json == nil {
		return false, ErrUnsupported
	}
	raw, err := b.json.Call(ctx, "Program.getByID", map[string]any{"id": iseID})
	if err != nil {
		return false, nil //nolint:nilerr // not found → false, nil
	}
	return raw != nil, nil
}

// --- firmware download (direct HTTP POST) ---------------------------------

// DownloadFirmware implements Operations. Posts the firmware URL to the CCU's
// maintenance CGI (`/config/cp_maintenance.cgi`) so the CCU can fetch and
// stage the image for installation. Requires that
// [SetDownloadFirmwareTransport] has been called with a valid base URL and
// session-ID provider; returns [ErrUnsupported] otherwise.
//
// Only "http://" and "https://" scheme firmware URLs are accepted.
func (b *CcuBackend) DownloadFirmware(ctx context.Context, firmwareURL string) error {
	if b.baseURL == "" || b.sessionIDFn == nil {
		return ErrUnsupported
	}
	if !strings.HasPrefix(firmwareURL, "http://") && !strings.HasPrefix(firmwareURL, "https://") {
		return fmt.Errorf("ccu.DownloadFirmware: only http/https scheme allowed, got %q: %w", firmwareURL, ErrUnsupported)
	}

	sid := b.sessionIDFn()
	if sid == "" {
		return fmt.Errorf("ccu.DownloadFirmware: no active JSON-RPC session: %w", ErrUnsupported)
	}

	uploadURL := strings.TrimRight(b.baseURL, "/") + "/config/cp_maintenance.cgi"

	form := url.Values{
		"sid":    {sid},
		"action": {"download_firmware"},
		"url":    {firmwareURL},
	}

	hc := b.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: firmwareDownloadTimeout}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("ccu.DownloadFirmware: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("ccu.DownloadFirmware: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := readLimitedResponse(resp.Body, maxDownloadResponseSize); err != nil {
		return fmt.Errorf("ccu.DownloadFirmware: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ccu.DownloadFirmware: CCU returned HTTP %d", resp.StatusCode)
	}
	return nil
}
