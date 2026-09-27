// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// The system API, /api/system/v1, as far as Loom uses it. Answers keep
// their whole document in Raw next to the typed members (see the package
// doc for why only some members are typed).

const systemBase = "/api/system/v1"

// Health is the open GET /health.
type Health struct {
	OK      bool       `json:"ok"`
	Version string     `json:"version"`
	Release string     `json:"release"`
	Base    string     `json:"base"`
	UptimeS int64      `json:"uptime_s"`
	Meta    HealthMeta `json:"meta"`
}

// HealthMeta is the metadata store part of the health answer.
type HealthMeta struct {
	Revision  int  `json:"revision"`
	Recovered bool `json:"recovered"`
}

// Status is GET /status (system:read).
type Status struct {
	Hostname         string          `json:"hostname"`
	Timezone         string          `json:"timezone"`
	OcculitedVersion string          `json:"occulited_version"`
	Raw              json.RawMessage `json:"-"`
}

// Time is GET /time (system:read).
type Time struct {
	TZ         string          `json:"tz"`
	Zone       string          `json:"zone"`
	NTPServers []string        `json:"ntp_servers"`
	HasNTP     bool            `json:"has_ntp"`
	Now        string          `json:"now"`
	Raw        json.RawMessage `json:"-"`
}

// SystemUpdate is GET /system-update (system:read). Staged and Feed are
// nil when the box reports null.
type SystemUpdate struct {
	Running   RunningVersion  `json:"running"`
	Staged    *StagedUpdate   `json:"staged"`
	Feed      *UpdateFeed     `json:"feed"`
	Container string          `json:"container"`
	Raw       json.RawMessage `json:"-"`
}

// RunningVersion is the running /VERSION record; Lite is the openccu-lite
// version.
type RunningVersion struct {
	Version string `json:"version"`
	Lite    string `json:"lite"`
}

// StagedUpdate is a downloaded, verified update waiting for install.
type StagedUpdate struct {
	File    string `json:"file"`
	Size    int64  `json:"size"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
}

// UpdateFeed is the release feed state; Available is nil when the feed
// offers nothing.
type UpdateFeed struct {
	Enabled     bool             `json:"enabled"`
	Downloading bool             `json:"downloading"`
	Available   *AvailableUpdate `json:"available"`
}

// AvailableUpdate is the release the feed offers; Newer is true when it
// is newer than the running one.
type AvailableUpdate struct {
	Version string `json:"version"`
	Newer   bool   `json:"newer"`
}

// Health reads GET /health (open).
func (c *Client) Health(ctx context.Context) (Health, error) {
	return get[Health](ctx, c, systemBase+"/health", nil)
}

// Status reads GET /status.
func (c *Client) Status(ctx context.Context) (Status, error) {
	return getWithRaw(ctx, c, systemBase+"/status", func(s *Status, raw json.RawMessage) { s.Raw = raw })
}

// Time reads GET /time.
func (c *Client) Time(ctx context.Context) (Time, error) {
	return getWithRaw(ctx, c, systemBase+"/time", func(t *Time, raw json.RawMessage) { t.Raw = raw })
}

// SystemUpdate reads GET /system-update.
func (c *Client) SystemUpdate(ctx context.Context) (SystemUpdate, error) {
	return getWithRaw(ctx, c, systemBase+"/system-update", func(u *SystemUpdate, raw json.RawMessage) { u.Raw = raw })
}

// CheckSystemUpdate asks the release feed now (POST /system-update/check,
// scope power) and returns the refreshed state.
func (c *Client) CheckSystemUpdate(ctx context.Context) (SystemUpdate, error) {
	raw, err := call[json.RawMessage](ctx, c, request{method: http.MethodPost, path: systemBase + "/system-update/check"})
	if err != nil {
		return SystemUpdate{}, err
	}
	u, err := withRaw[SystemUpdate](raw)
	u.Raw = raw
	return u, err
}

// DownloadSystemUpdate downloads, verifies and stages the offered
// release (POST /system-update/download, scope power). The call is long
// running: it uses the stream client, bounded only by ctx.
func (c *Client) DownloadSystemUpdate(ctx context.Context) (StagedUpdate, error) {
	r := request{method: http.MethodPost, path: systemBase + "/system-update/download"}
	resp, err := c.send(ctx, c.stream, r)
	if err != nil {
		return StagedUpdate{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeBody[StagedUpdate](resp, r)
}

// InstallSystemUpdate arms recovery and reboots into the staged update
// (POST /system-update/install, scope power); 409 when nothing is staged.
func (c *Client) InstallSystemUpdate(ctx context.Context) (PowerAnswer, error) {
	return call[PowerAnswer](ctx, c, request{method: http.MethodPost, path: systemBase + "/system-update/install"})
}

// PowerAnswer is the answer of the power actions.
type PowerAnswer struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

var confirmBody = []byte(`{"confirm":true}`)

// Reboot reboots the box (POST /reboot {"confirm":true}, scope power).
func (c *Client) Reboot(ctx context.Context) (PowerAnswer, error) {
	return c.power(ctx, "/reboot")
}

// Halt powers the box off (POST /halt {"confirm":true}, scope power).
func (c *Client) Halt(ctx context.Context) (PowerAnswer, error) {
	return c.power(ctx, "/halt")
}

// RebootRecovery reboots into recovery (POST /reboot/recovery
// {"confirm":true}, scope power).
func (c *Client) RebootRecovery(ctx context.Context) (PowerAnswer, error) {
	return c.power(ctx, "/reboot/recovery")
}

func (c *Client) power(ctx context.Context, p string) (PowerAnswer, error) {
	return call[PowerAnswer](ctx, c, request{method: http.MethodPost, path: systemBase + p, body: confirmBody})
}

// Backup is a backup archive being streamed. The caller reads and closes
// Body. FileName is the Content-Disposition name reduced to a base name
// (".sbk", or ".sbk.age" when the box encrypts backups); Size is -1 when
// the box sends no length (an encrypted archive).
type Backup struct {
	Body     io.ReadCloser
	FileName string
	Size     int64
}

// DownloadBackup streams GET /backup (scope backup). The archive is
// taken as served; the box owner's encryption choice is kept (no
// ?encrypted=false). Bounded only by ctx.
func (c *Client) DownloadBackup(ctx context.Context) (Backup, error) {
	resp, err := c.send(ctx, c.stream, request{ //nolint:bodyclose // the caller closes Backup.Body
		method: http.MethodGet, path: systemBase + "/backup",
		header: http.Header{"Accept": {"application/octet-stream"}},
	})
	if err != nil {
		return Backup{}, err
	}
	return Backup{Body: resp.Body, FileName: dispositionFileName(resp.Header.Get("Content-Disposition")), Size: resp.ContentLength}, nil
}

// dispositionFileName extracts a safe base file name from a
// Content-Disposition header ("" when there is none).
func dispositionFileName(h string) string {
	if h == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(h)
	if err != nil {
		return ""
	}
	name := strings.ReplaceAll(params["filename"], "\\", "/")
	name = path.Base(name)
	if name == "." || name == "/" || name == ".." {
		return ""
	}
	return name
}

// BackupRun is POST /backup/run's 202 answer.
type BackupRun struct {
	Started  bool   `json:"started"`
	Instance string `json:"instance"`
}

type backupRunBody struct {
	Target string `json:"target,omitempty"`
}

// RunBackup starts an on-box backup run (scope backup); target "" runs
// the default targets. 409 busy while one runs.
func (c *Client) RunBackup(ctx context.Context, target string) (BackupRun, error) {
	raw, err := marshal(backupRunBody{Target: target})
	if err != nil {
		return BackupRun{}, err
	}
	return call[BackupRun](ctx, c, request{method: http.MethodPost, path: systemBase + "/backup/run", body: raw})
}

// BackupTargets is GET /backup/targets (system:read).
type BackupTargets struct {
	Targets []BackupTarget  `json:"targets"`
	Raw     json.RawMessage `json:"-"`
}

// BackupTarget is one backup target with its state and last run.
type BackupTarget struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Enabled    bool              `json:"enabled"`
	State      BackupTargetState `json:"state"`
	LastBackup *LastBackup       `json:"last_backup"`
}

// BackupTargetState is a target's state; State is "running" while a run
// for it is active.
type BackupTargetState struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// LastBackup is a target's most recent run.
type LastBackup struct {
	At    string `json:"at"`
	OK    bool   `json:"ok"`
	State string `json:"state"`
	Error string `json:"error"`
	Name  string `json:"name"`
}

// BackupTargets reads GET /backup/targets.
func (c *Client) BackupTargets(ctx context.Context) (BackupTargets, error) {
	return getWithRaw(ctx, c, systemBase+"/backup/targets", func(b *BackupTargets, raw json.RawMessage) { b.Raw = raw })
}

// RestoreCheck is POST /restore/check's answer.
type RestoreCheck struct {
	File       string            `json:"file"`
	Check      RestoreCheckState `json:"check"`
	Encryption RestoreEncryption `json:"encryption"`
}

// RestoreCheckState is the archive check.
type RestoreCheckState struct {
	OK             bool   `json:"ok"`
	Output         string `json:"output"`
	BackupVersion  string `json:"backup_version"`
	RunningVersion string `json:"running_version"`
	NeedsKey       bool   `json:"needs_key"`
	HasRega        bool   `json:"has_rega"`
}

// RestoreEncryption describes the archive's encryption.
type RestoreEncryption struct {
	Encrypted        bool `json:"encrypted"`
	NeedsRecoveryKey bool `json:"needs_recovery_key"`
	CreatedHere      bool `json:"created_here"`
}

// CheckRestore uploads an archive as the multipart field "file" (POST
// /restore/check, scope power). The upload is buffered to give it a
// length. Bounded only by ctx.
func (c *Client) CheckRestore(ctx context.Context, fileName string, archive io.Reader) (RestoreCheck, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", path.Base(fileName))
	if err != nil {
		return RestoreCheck{}, fmt.Errorf("occulited: restore upload: %w", err)
	}
	if _, err := io.Copy(part, archive); err != nil {
		return RestoreCheck{}, fmt.Errorf("occulited: restore upload: %w", err)
	}
	if err := mw.Close(); err != nil {
		return RestoreCheck{}, fmt.Errorf("occulited: restore upload: %w", err)
	}
	r := request{
		method: http.MethodPost, path: systemBase + "/restore/check", body: buf.Bytes(),
		header: http.Header{"Content-Type": {mw.FormDataContentType()}},
	}
	resp, err := c.send(ctx, c.stream, r)
	if err != nil {
		return RestoreCheck{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeBody[RestoreCheck](resp, r)
}

// RestoreApplyRequest is POST /restore/apply's body; File is the name
// the check answered.
type RestoreApplyRequest struct {
	File  string `json:"file"`
	Key   string `json:"key,omitempty"`
	Force bool   `json:"force,omitempty"`
}

// RestoreApply is POST /restore/apply's answer; Rebooting false with a
// Message means the reboot did not start.
type RestoreApply struct {
	OK        bool   `json:"ok"`
	Output    string `json:"output"`
	Rebooting bool   `json:"rebooting"`
	Message   string `json:"message"`
}

// ApplyRestore restores a checked archive (scope power); the box
// reboots.
func (c *Client) ApplyRestore(ctx context.Context, in RestoreApplyRequest) (RestoreApply, error) {
	raw, err := marshal(in)
	if err != nil {
		return RestoreApply{}, err
	}
	r := request{method: http.MethodPost, path: systemBase + "/restore/apply", body: raw}
	resp, err := c.send(ctx, c.stream, r)
	if err != nil {
		return RestoreApply{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeBody[RestoreApply](resp, r)
}

// ServiceMessages is GET /service-messages (system:read).
type ServiceMessages struct {
	Count    int              `json:"count"`
	Messages []ServiceMessage `json:"messages"`
}

// ServiceMessage is a service-flagged datapoint active on channel 0 of
// a device (Address is the device, Channel "0").
type ServiceMessage struct {
	Interface string          `json:"interface"`
	Address   string          `json:"address"`
	Channel   string          `json:"channel"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Since     time.Time       `json:"since"`
	Seen      string          `json:"seen"`
	Type      string          `json:"type"`
}

// ServiceMessages reads GET /service-messages.
func (c *Client) ServiceMessages(ctx context.Context) (ServiceMessages, error) {
	return get[ServiceMessages](ctx, c, systemBase+"/service-messages", nil)
}

// RadioHealth is GET /radio/health (system:read).
type RadioHealth struct {
	Interfaces    []RadioInterface `json:"interfaces"`
	Busy          bool             `json:"busy"`
	BusyInterface string           `json:"busy_interface"`
	Raw           json.RawMessage  `json:"-"`
}

// RadioInterface is one radio module.
type RadioInterface struct {
	Interface string  `json:"interface"`
	Address   string  `json:"address"`
	Type      string  `json:"type"`
	Connected bool    `json:"connected"`
	Default   bool    `json:"default"`
	Firmware  string  `json:"firmware"`
	DutyCycle float64 `json:"duty_cycle"`
}

// RadioHealth reads GET /radio/health.
func (c *Client) RadioHealth(ctx context.Context) (RadioHealth, error) {
	return getWithRaw(ctx, c, systemBase+"/radio/health", func(r *RadioHealth, raw json.RawMessage) { r.Raw = raw })
}

// ----------------------------------------------------------------------
// Heating groups
// ----------------------------------------------------------------------

// Groups is GET /groups (system:read).
type Groups struct {
	Groups             []Group             `json:"groups"`
	DevicesToConfigure []DeviceToConfigure `json:"devices_to_configure"`
}

// Group is one group: a virtual device on VirtualDevices.
type Group struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	TypeLabel string `json:"type_label"`
	Device    string `json:"device"`
	Ref       string `json:"ref"`
}

// DeviceToConfigure is a member device that still needs configuring.
type DeviceToConfigure struct {
	ID     string `json:"id"`
	Serial string `json:"serial"`
	Type   string `json:"type"`
}

// GroupTypes is GET /groups/types. The contract does not spell out the
// member shape, so members stay raw.
type GroupTypes struct {
	Types []GroupType `json:"types"`
}

// GroupType is one group type with its candidate members.
type GroupType struct {
	ID         string            `json:"id"`
	Label      string            `json:"label"`
	Assignable []json.RawMessage `json:"assignable"`
	Leftover   []json.RawMessage `json:"leftover"`
}

// GroupDetail is GET /groups/{id} (and the PUT answer). Members,
// Assignable, Leftover and Types stay raw (shape not in the contract).
type GroupDetail struct {
	Group
	DeviceName            string          `json:"device_name"`
	ForbidSingleOperation bool            `json:"forbid_single_operation"`
	Members               json.RawMessage `json:"members"`
	Assignable            json.RawMessage `json:"assignable"`
	Leftover              json.RawMessage `json:"leftover"`
	Types                 json.RawMessage `json:"types"`
}

// GroupCreate is the POST /groups body; Members are member ids.
type GroupCreate struct {
	Name                  string   `json:"name"`
	Type                  string   `json:"type"`
	Members               []string `json:"members"`
	ForbidSingleOperation *bool    `json:"forbid_single_operation,omitempty"`
}

// GroupUpdate is the PUT /groups/{id} body; Members replaces the whole
// list.
type GroupUpdate struct {
	Name                  *string   `json:"name,omitempty"`
	Members               *[]string `json:"members,omitempty"`
	ForbidSingleOperation *bool     `json:"forbid_single_operation,omitempty"`
}

// GroupCreated is the POST /groups answer.
type GroupCreated struct {
	Group
	DevicesToConfigure []DeviceToConfigure `json:"devices_to_configure"`
}

// GroupDeleted is the DELETE /groups/{id} answer.
type GroupDeleted struct {
	Deleted       bool            `json:"deleted"`
	FormerMembers json.RawMessage `json:"former_members"`
}

// Groups lists the groups.
func (c *Client) Groups(ctx context.Context) (Groups, error) {
	return get[Groups](ctx, c, systemBase+"/groups", nil)
}

// GroupTypes lists the group types.
func (c *Client) GroupTypes(ctx context.Context) (GroupTypes, error) {
	return get[GroupTypes](ctx, c, systemBase+"/groups/types", nil)
}

// Group reads one group; unknown-group (404) as an *APIError.
func (c *Client) Group(ctx context.Context, id string) (GroupDetail, error) {
	return get[GroupDetail](ctx, c, systemBase+"/groups/"+url.PathEscape(id), nil)
}

// CreateGroup creates a group (system:write).
func (c *Client) CreateGroup(ctx context.Context, in GroupCreate) (GroupCreated, error) {
	if in.Members == nil {
		in.Members = []string{}
	}
	raw, err := marshal(in)
	if err != nil {
		return GroupCreated{}, err
	}
	return call[GroupCreated](ctx, c, request{method: http.MethodPost, path: systemBase + "/groups", body: raw})
}

// UpdateGroup changes a group (system:write).
func (c *Client) UpdateGroup(ctx context.Context, id string, in GroupUpdate) (GroupDetail, error) {
	raw, err := marshal(in)
	if err != nil {
		return GroupDetail{}, err
	}
	return call[GroupDetail](ctx, c, request{method: http.MethodPut, path: systemBase + "/groups/" + url.PathEscape(id), body: raw})
}

// DeleteGroup removes a group (system:write).
func (c *Client) DeleteGroup(ctx context.Context, id string) (GroupDeleted, error) {
	return call[GroupDeleted](ctx, c, request{method: http.MethodDelete, path: systemBase + "/groups/" + url.PathEscape(id)})
}

// getWithRaw reads a GET answer into T and hands its raw document to
// keep. T is the answer type.
func getWithRaw[T any](ctx context.Context, c *Client, p string, keep func(*T, json.RawMessage)) (T, error) {
	raw, err := get[json.RawMessage](ctx, c, p, nil)
	if err != nil {
		var zero T
		return zero, err
	}
	out, err := withRaw[T](raw)
	if err != nil {
		return out, err
	}
	keep(&out, raw)
	return out, nil
}
