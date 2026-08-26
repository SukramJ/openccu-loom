// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/httpx"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// Heating-group mutations run through the CCU's own HMServer jpages
// endpoints, authenticated with Loom's live JSON-RPC session — the group
// wiring matrix is computed by HMServer, never re-derived in Go. See
// docs/adr/0055-groups-jpages-proxy.md. The wire contract below was verified
// live against a real CCU (create → save → delete) and against the shipped
// GroupEditPage.ftl / GroupListPage.ftl page templates.

const (
	// jpagesInHeatingGroupDataID is the per-device metadata flag the WebUI
	// sets before a save (assigned members true, other assignable devices
	// false).
	jpagesInHeatingGroupDataID = "inHeatingGroup"
	// jpagesSessionExpiredCode is the errorCode HMServer returns when the
	// sid is missing / expired.
	jpagesSessionExpiredCode = "42"
	// jpagesSaveTimeout bounds the save POST. HMServer commits the group but
	// its HTTP response is slow / may not return; the adapter treats a
	// timeout as "fired" and polls getHeatingGroupList for completion.
	jpagesSaveTimeout = 60 * time.Second
	// jpagesReadTimeout bounds the prompt create / delete / suitable-members
	// calls.
	jpagesReadTimeout = 30 * time.Second
)

// jpagesResult is HMServer's JsonResponse wrapper for the group endpoints
// (create / save / delete). Data endpoints (suitableGroupMembers) return
// their payload bare and only fall back to this wrapper on a bad session.
type jpagesResult struct {
	IsSuccessful bool   `json:"isSuccessful"`
	ErrorCode    string `json:"errorCode"`
	Content      string `json:"content"`
}

// HeatingGroupType is one assignable group type offered by the create form.
type HeatingGroupType struct {
	ID       string
	LabelKey string
}

// HeatingGroupMember is one device/channel from a suitableGroupMembers reply.
type HeatingGroupMember struct {
	ID           string `json:"id"`
	SerialNumber string `json:"serialNumber"`
	Type         string `json:"type"`
}

// SuitableHeatingGroupMembers is the suitableGroupMembers reply: the devices
// assignable to a group of a given type, split into currently-assignable and
// leftover (not fitting) buckets.
type SuitableHeatingGroupMembers struct {
	Assignable []HeatingGroupMember `json:"assignableGroupMembers"`
	Leftover   []HeatingGroupMember `json:"leftoverGroupMembers"`
}

// HeatingGroupSaveInput is the body of a group/save call.
type HeatingGroupSaveInput struct {
	// GroupID is 0 for a new group (after CreateHeatingGroupDraft) and the
	// existing id for an edit.
	GroupID int
	Name    string
	TypeID  string
	// ForbidSingleOperation is the "operate only via group" flag.
	ForbidSingleOperation bool
	// MemberIDs are the assigned member channel/device addresses.
	MemberIDs []string
	IsNew     bool
}

var (
	reDraftGroupID = regexp.MustCompile(`self\.groupId\s*=\s*(\d+)`)
	reDraftSerial  = regexp.MustCompile(`createVirtualDeviceSerialNumber\((\d+)\)`)
	reGroupType    = regexp.MustCompile(`new GroupType\("([^"]+)",\s*translateKey\('([^']*)'\)\)`)
)

// groupsTransportReady reports whether the session-authenticated HTTP
// transport (base URL + session id) is wired; group mutations need it.
func (b *CcuBackend) groupsTransportReady() error {
	if b.baseURL == "" || b.sessionIDFn == nil {
		return fmt.Errorf("ccu groups: jpages transport not wired: %w", ErrUnsupported)
	}
	return nil
}

// jpagesURL builds a jpages endpoint URL carrying the live JSON-RPC session.
func (b *CcuBackend) jpagesURL(path string) string {
	return strings.TrimRight(b.baseURL, "/") + "/pages/jpages/group/" + path +
		"?sid=" + b.sessionIDFn()
}

func (b *CcuBackend) jpagesClient() *http.Client {
	if b.httpClient != nil {
		return b.httpClient
	}
	return httpx.NewClient(jpagesReadTimeout)
}

// jpagesDo issues a jpages request. method GET carries no body; POST sends
// the JSON body with the WebUI's form content-type (Prototype default). The
// raw response body is returned for the caller to decode.
func (b *CcuBackend) jpagesDo(ctx context.Context, method, path string, body any, timeout time.Duration) ([]byte, error) {
	if err := b.groupsTransportReady(); err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("ccu groups: marshal body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, b.jpagesURL(path), reader)
	if err != nil {
		return nil, fmt.Errorf("ccu groups: build request: %w", err)
	}
	if body != nil {
		// Mirror the WebUI's Prototype Ajax.Request default; the save handler
		// hangs on application/json.
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	}
	resp, err := b.jpagesClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("ccu groups: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("ccu groups: read %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ccu groups: %s %s: HTTP %d: %w", method, path, resp.StatusCode, ErrUnsupported)
	}
	return raw, nil
}

// decodeJpagesResult parses the {isSuccessful,errorCode,content} wrapper and
// maps a failure to an error (an expired session to ErrAuthFailure).
func decodeJpagesResult(raw []byte, op string) (jpagesResult, error) {
	var r jpagesResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, fmt.Errorf("ccu groups: decode %s reply: %w", op, err)
	}
	if !r.IsSuccessful {
		if r.ErrorCode == jpagesSessionExpiredCode {
			return r, fmt.Errorf("ccu groups: %s: session expired: %w", op, hmerr.ErrAuthFailure)
		}
		return r, fmt.Errorf("ccu groups: %s failed (errorCode %q): %w", op, r.ErrorCode, ErrUnsupported)
	}
	return r, nil
}

// CreateHeatingGroupDraft runs the first step of a new-group flow: GET
// group/create allocates the draft on HMServer and returns the edit page.
// The assignable group types are parsed from that page (there is no separate
// JSON type endpoint on the firmware). The draft id is a placeholder — the
// real id is assigned by the subsequent save and read back from the roster.
func (b *CcuBackend) CreateHeatingGroupDraft(ctx context.Context) (draftID int, types []HeatingGroupType, err error) {
	raw, err := b.jpagesDo(ctx, http.MethodGet, "create", nil, jpagesReadTimeout)
	if err != nil {
		return 0, nil, err
	}
	res, err := decodeJpagesResult(raw, "create")
	if err != nil {
		return 0, nil, err
	}
	if m := reDraftGroupID.FindStringSubmatch(res.Content); m != nil {
		draftID = atoiSafe(m[1])
	} else if m := reDraftSerial.FindStringSubmatch(res.Content); m != nil {
		draftID = atoiSafe(m[1])
	}
	for _, m := range reGroupType.FindAllStringSubmatch(res.Content, -1) {
		types = append(types, HeatingGroupType{ID: m[1], LabelKey: m[2]})
	}
	return draftID, types, nil
}

// SaveHeatingGroup commits a group (create after CreateHeatingGroupDraft, or
// edit). The reply is best-effort: HMServer commits the group but its HTTP
// response is slow / may time out — the caller confirms completion by
// re-reading getHeatingGroupList. A returned error that is a context timeout
// therefore does NOT mean the save failed.
func (b *CcuBackend) SaveHeatingGroup(ctx context.Context, in HeatingGroupSaveInput) error {
	memberIDs := in.MemberIDs
	if memberIDs == nil {
		memberIDs = []string{}
	}
	// assignedDevicesIds must be a JSON-encoded STRING, not a native array —
	// HMServer's save handler re-parses the field and silently drops a native
	// array, committing an EMPTY group. The CCU WebUI sends it stringified too
	// (captured from GroupEditPage save()); live-confirmed both here: a native
	// array yields 0 members, the stringified form assigns them.
	assigned, err := json.Marshal(memberIDs)
	if err != nil {
		return fmt.Errorf("ccu groups: marshal member ids: %w", err)
	}
	// groupDeviceName is the bare group name (no "INT<serial>" suffix). At save
	// time the real group id is unknown — HMServer only assigns it on commit —
	// so any serial we could build here (from the always-zero draft id) would be
	// INT0000000, a wrong label that HMServer stores verbatim. Sending the bare
	// name lets the CCU derive the virtual device's channel names as
	// "<name>:<n>", which is both correct and cleaner. Live-confirmed: the
	// roster's GROUP_DEVICE_NAME then equals the group name exactly.
	body := map[string]any{
		"groupId":               in.GroupID,
		"groupName":             jsEscape(in.Name),
		"groupTypeId":           in.TypeID,
		"forbidSingleOperation": in.ForbidSingleOperation,
		"assignedDevicesIds":    string(assigned),
		"isNewGroup":            in.IsNew,
		"groupDeviceName":       jsEscape(in.Name),
	}
	raw, err := b.jpagesDo(ctx, http.MethodPost, "save", body, jpagesSaveTimeout)
	if err != nil {
		return err
	}
	_, err = decodeJpagesResult(raw, "save")
	return err
}

// DeleteHeatingGroup removes a group by id. Unlike save, delete returns
// promptly.
func (b *CcuBackend) DeleteHeatingGroup(ctx context.Context, groupID int) error {
	raw, err := b.jpagesDo(ctx, http.MethodPost, "delete", map[string]any{"groupId": groupID}, jpagesReadTimeout)
	if err != nil {
		return err
	}
	_, err = decodeJpagesResult(raw, "delete")
	return err
}

// SuitableHeatingGroupMembers returns the devices assignable to a group of
// the given type. On a valid session HMServer returns the payload bare; only
// a bad session yields the {isSuccessful:false,...} wrapper, which is mapped
// to an error.
func (b *CcuBackend) SuitableHeatingGroupMembers(ctx context.Context, typeID string) (SuitableHeatingGroupMembers, error) {
	raw, err := b.jpagesDo(ctx, http.MethodPost, "suitableGroupMembers",
		map[string]any{"groupTypeId": typeID}, jpagesReadTimeout)
	if err != nil {
		return SuitableHeatingGroupMembers{}, err
	}
	// Detect the session-invalid wrapper before treating the body as data.
	var probe struct {
		IsSuccessful *bool  `json:"isSuccessful"`
		ErrorCode    string `json:"errorCode"`
	}
	if json.Unmarshal(raw, &probe) == nil && probe.IsSuccessful != nil && !*probe.IsSuccessful {
		if probe.ErrorCode == jpagesSessionExpiredCode {
			return SuitableHeatingGroupMembers{}, fmt.Errorf("ccu groups: suitableGroupMembers: session expired: %w", hmerr.ErrAuthFailure)
		}
		return SuitableHeatingGroupMembers{}, fmt.Errorf("ccu groups: suitableGroupMembers failed (errorCode %q): %w", probe.ErrorCode, ErrUnsupported)
	}
	var out SuitableHeatingGroupMembers
	if err := json.Unmarshal(raw, &out); err != nil {
		return SuitableHeatingGroupMembers{}, fmt.Errorf("ccu groups: decode suitableGroupMembers: %w", err)
	}
	return out, nil
}

// SetInHeatingGroupMetadata sets the per-device inHeatingGroup flag via
// JSON-RPC Interface.setMetadata — the preamble the WebUI runs before a save
// (assigned members true, other assignable devices false).
func (b *CcuBackend) SetInHeatingGroupMetadata(ctx context.Context, deviceAddress string, inGroup bool) error {
	if b.json == nil {
		return ErrUnsupported
	}
	_, err := b.json.Call(ctx, "Interface.setMetadata", map[string]any{
		"objectId": deviceAddress,
		"dataId":   jpagesInHeatingGroupDataID,
		"value":    strconvBool(inGroup),
	})
	return err
}

// DeviceRegaID resolves a device/serial address to its ReGa id
// (JSON-RPC Device.getReGaIDByAddress). A CCU "noDeviceFound" (or empty)
// result yields "" with no error — the caller treats the device as not yet
// visible. Used to name a group's virtual device (GR03) and to toggle a
// member's operate-only flag (GR04).
func (b *CcuBackend) DeviceRegaID(ctx context.Context, address string) (string, error) {
	if b.json == nil {
		return "", ErrUnsupported
	}
	raw, err := b.json.Call(ctx, "Device.getReGaIDByAddress", map[string]any{"address": address})
	if err != nil {
		return "", err
	}
	id, _ := raw.(string)
	if id == "" || id == "noDeviceFound" {
		return "", nil
	}
	return id, nil
}

// SetOperateGroupOnly sets a device's "operate only via group" flag by ReGa
// id (JSON-RPC Device.setOperateGroupOnly). The CCU reports the flag back as
// the string "true"/"false".
func (b *CcuBackend) SetOperateGroupOnly(ctx context.Context, regaID string, mode bool) error {
	if b.json == nil {
		return ErrUnsupported
	}
	_, err := b.json.Call(ctx, "Device.setOperateGroupOnly", map[string]any{"id": regaID, "mode": mode})
	return err
}

// --- small helpers ----------------------------------------------------------

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func strconvBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// jsEscape mirrors JavaScript escape(): unreserved ASCII stays literal, other
// code points up to 0xFF become %XX (Latin-1), higher ones %uXXXX. HMServer
// decodes the group name as ISO-8859-1, so this is what keeps umlauts intact
// (e.g. "Süd" -> "S%FCd").
func jsEscape(s string) string {
	const keep = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789@*_+-./"
	var out strings.Builder
	for _, r := range s {
		switch {
		case r < 128 && strings.ContainsRune(keep, r):
			out.WriteRune(r)
		case r <= 0xFF:
			fmt.Fprintf(&out, "%%%02X", r)
		default:
			fmt.Fprintf(&out, "%%u%04X", r)
		}
	}
	return out.String()
}
