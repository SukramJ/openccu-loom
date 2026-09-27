// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// System API subset.
//
// Each endpoint answers a fixed or knob-driven document and leaves the
// request in [Fake.Calls] (with its body), so a test asserts both what a
// client read and what it asked the box to do.

// System API scopes.
const (
	scopeSystemRead  = "system:read"
	scopeSystemWrite = "system:write"
	scopePower       = "power"
	scopeBackup      = "backup"
)

// backupBlob is the fixed archive GET /api/system/v1/backup streams.
// It has the member layout of a CCU system backup (the configuration
// archive, its signature, the firmware version), as the box serves one,
// so the daemon's archive inspection accepts it; the member contents are
// placeholders.
var backupBlob = sbkBlob()

func sbkBlob() []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, m := range []struct{ name, body string }{
		{"usr_local.tar.gz", "litefake configuration archive"},
		{"signature", "litefake signature"},
		{"firmware_version", "VERSION=" + fakeBase + "\nPRODUCT=openccu-lite\n"},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: m.name, Mode: 0o644, Size: int64(len(m.body))}); err != nil {
			panic(err) // invariant: writing to a bytes.Buffer cannot fail
		}
		if _, err := tw.Write([]byte(m.body)); err != nil {
			panic(err) // invariant: as above
		}
	}
	if err := tw.Close(); err != nil {
		panic(err) // invariant: as above
	}
	return buf.Bytes()
}

// BackupBlob returns the bytes GET /api/system/v1/backup streams.
func BackupBlob() []byte { return append([]byte(nil), backupBlob...) }

// BackupFileName is the Content-Disposition file name of the backup.
const BackupFileName = "openccu-lite-fake.sbk"

// ServiceMessage is one entry of /api/system/v1/service-messages: a
// service-flagged datapoint active on channel 0 of a device.
type ServiceMessage struct {
	Interface string          `json:"interface"`
	Address   string          `json:"address"`
	Channel   string          `json:"channel"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Since     string          `json:"since"`
	Seen      string          `json:"seen"`
	Type      string          `json:"type,omitempty"`
}

// StagedUpdate is the staged system update record.
type StagedUpdate struct {
	File          string `json:"file"`
	Size          int64  `json:"size"`
	Modified      string `json:"modified"`
	Kind          string `json:"kind"`
	Version       string `json:"version"`
	Board         string `json:"board"`
	Warning       string `json:"warning"`
	RecoveryArmed bool   `json:"recovery_armed"`
	WayBack       bool   `json:"way_back"`
}

// AvailableUpdate is the release the feed offers.
type AvailableUpdate struct {
	Version   string `json:"version"`
	Tag       string `json:"tag"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	SHA256URL string `json:"sha256_url"`
	Published string `json:"published"`
	NotesURL  string `json:"notes_url"`
	Newer     bool   `json:"newer"`
}

// systemState is the knob-driven part of the system API.
type systemState struct {
	mu              sync.Mutex
	serviceMessages []ServiceMessage
	available       *AvailableUpdate
	staged          *StagedUpdate
	uploads         map[string][]byte
	backupRuns      int
	groups          *groupStore
	// restoreNeedsKey makes every checked archive one only the box's
	// recovery key opens.
	restoreNeedsKey bool
	backupTargets   []BackupTarget
	applied         []string
}

// BackupTarget is one entry of /api/system/v1/backup/targets, reduced to
// the members the daemon reads.
type BackupTarget struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Enabled    bool              `json:"enabled"`
	State      BackupTargetState `json:"state"`
	LastBackup *LastBackup       `json:"last_backup,omitempty"`
}

// BackupTargetState is a target's current state ("idle", "running", …).
type BackupTargetState struct {
	State string `json:"state"`
}

// LastBackup is the outcome of a target's newest backup run.
type LastBackup struct {
	At    string `json:"at"`
	OK    bool   `json:"ok"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

func newSystemState() *systemState {
	return &systemState{uploads: map[string][]byte{}, groups: newGroupStore()}
}

func (f *Fake) systemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/system/v1/status", f.sysRead(f.handleStatus))
	mux.HandleFunc("GET /api/system/v1/time", f.sysRead(f.handleTime))
	mux.HandleFunc("GET /api/system/v1/system-update", f.sysRead(f.handleUpdateGet))
	mux.HandleFunc("POST /api/system/v1/system-update/check", f.sysScope(scopePower, f.handleUpdateGet))
	mux.HandleFunc("POST /api/system/v1/system-update/download", f.sysScope(scopePower, f.handleUpdateDownload))
	mux.HandleFunc("POST /api/system/v1/system-update/install", f.sysScope(scopePower, f.handleUpdateInstall))
	mux.HandleFunc("POST /api/system/v1/reboot", f.sysScope(scopePower, f.confirmed("rebooting")))
	mux.HandleFunc("POST /api/system/v1/halt", f.sysScope(scopePower, f.confirmed("halting")))
	mux.HandleFunc("POST /api/system/v1/reboot/recovery", f.sysScope(scopePower, f.confirmed("rebooting into recovery")))
	mux.HandleFunc("GET /api/system/v1/backup", f.sysScope(scopeBackup, f.handleBackup))
	mux.HandleFunc("POST /api/system/v1/backup/run", f.sysScope(scopeBackup, f.handleBackupRun))
	mux.HandleFunc("GET /api/system/v1/backup/targets", f.sysRead(f.handleBackupTargets))
	mux.HandleFunc("POST /api/system/v1/restore/check", f.sysScope(scopePower, f.handleRestoreCheck))
	mux.HandleFunc("POST /api/system/v1/restore/apply", f.sysScope(scopePower, f.handleRestoreApply))
	mux.HandleFunc("GET /api/system/v1/service-messages", f.sysRead(f.handleServiceMessages))
	mux.HandleFunc("GET /api/system/v1/radio/health", f.sysRead(f.handleRadioHealth))
	f.groupRoutes(mux)
}

// sysScope wraps a handler with its route scope.
func (f *Fake) sysScope(scope string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := f.authorize(w, r, scope, false); !ok {
			return
		}
		h(w, r)
	}
}

func (f *Fake) sysRead(h http.HandlerFunc) http.HandlerFunc { return f.sysScope(scopeSystemRead, h) }

// status is the part of /status whose members the contract names with
// a clear type.
type status struct {
	Hostname         string `json:"hostname"`
	OcculitedVersion string `json:"occulited_version"`
	Timezone         string `json:"timezone"`
	MetaRecovered    bool   `json:"meta_recovered"`
}

func (f *Fake) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, status{
		Hostname:         f.opts.Hostname,
		OcculitedVersion: fakeVersion,
		Timezone:         "Europe/Berlin",
	})
}

// timeAnswer is the /time answer.
type timeAnswer struct {
	TZ         string   `json:"tz"`
	Zone       string   `json:"zone"`
	NTPServers []string `json:"ntp_servers"`
	HasNTP     bool     `json:"has_ntp"`
	Now        string   `json:"now"`
}

func (f *Fake) handleTime(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, timeAnswer{
		TZ:         "CET-1CEST,M3.5.0,M10.5.0/3",
		Zone:       "Europe/Berlin",
		NTPServers: []string{"pool.ntp.org"},
		HasNTP:     true,
		Now:        time.Now().UTC().Format(time.RFC3339),
	})
}

// runningVersion is the running record; lite is the openccu-lite
// version.
type runningVersion struct {
	Version string `json:"version"`
	Base    string `json:"base"`
	Lite    string `json:"lite"`
}

type updateFeed struct {
	Enabled     bool             `json:"enabled"`
	FeedURL     string           `json:"feed_url"`
	Checked     string           `json:"checked"`
	Error       string           `json:"error"`
	Downloading bool             `json:"downloading"`
	Available   *AvailableUpdate `json:"available"`
}

type updateAnswer struct {
	Running   runningVersion `json:"running"`
	Staged    *StagedUpdate  `json:"staged"`
	Feed      *updateFeed    `json:"feed"`
	Container string         `json:"container"`
}

func (f *Fake) handleUpdateGet(w http.ResponseWriter, _ *http.Request) {
	s := f.system
	s.mu.Lock()
	ans := updateAnswer{
		Running: runningVersion{Version: fakeRelease, Base: fakeBase, Lite: fakeRelease},
		Staged:  s.staged,
	}
	if s.available != nil {
		a := *s.available
		ans.Feed = &updateFeed{Enabled: true, FeedURL: "https://example.invalid/feed", Available: &a}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, ans)
}

// handleUpdateDownload stages the available release and answers the
// staged record.
func (f *Fake) handleUpdateDownload(w http.ResponseWriter, _ *http.Request) {
	s := f.system
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.available == nil {
		writeError(w, http.StatusConflict, "nothing-available", "the feed offers no release")
		return
	}
	s.staged = &StagedUpdate{
		File:     "openccu-lite-" + s.available.Version + ".tar",
		Size:     s.available.Size,
		Modified: time.Now().UTC().Format(time.RFC3339),
		Kind:     "update",
		Version:  s.available.Version,
		WayBack:  true,
	}
	writeJSON(w, http.StatusOK, *s.staged)
}

// handleUpdateInstall arms recovery and reboots; 409 without a staged
// update.
func (f *Fake) handleUpdateInstall(w http.ResponseWriter, _ *http.Request) {
	s := f.system
	s.mu.Lock()
	staged := s.staged
	s.mu.Unlock()
	if staged == nil {
		writeError(w, http.StatusConflict, "nothing-staged", "no update is staged")
		return
	}
	writeJSON(w, http.StatusOK, rebootAnswer{OK: true, Message: "rebooting"})
}

// rebootAnswer is the answer of the power actions.
type rebootAnswer struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type confirmBody struct {
	Confirm bool `json:"confirm"`
}

// confirmed guards a power action behind a {"confirm":true} body.
func (f *Fake) confirmed(message string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body confirmBody
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || !body.Confirm {
			writeError(w, http.StatusBadRequest, "bad-request", `this action needs {"confirm":true}`)
			return
		}
		writeJSON(w, http.StatusOK, rebootAnswer{OK: true, Message: message})
	}
}

// handleBackup streams the fixed archive as an attachment.
func (f *Fake) handleBackup(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": BackupFileName}))
	w.Header().Set("Content-Length", strconv.Itoa(len(backupBlob)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(backupBlob)
}

type backupRunAnswer struct {
	Started  bool   `json:"started"`
	Instance string `json:"instance"`
}

func (f *Fake) handleBackupRun(w http.ResponseWriter, _ *http.Request) {
	s := f.system
	s.mu.Lock()
	s.backupRuns++
	n := s.backupRuns
	s.mu.Unlock()
	writeJSON(w, http.StatusAccepted, backupRunAnswer{Started: true, Instance: "run-" + strconv.Itoa(n)})
}

type backupTargetsAnswer struct {
	Targets []BackupTarget `json:"targets"`
}

func (f *Fake) handleBackupTargets(w http.ResponseWriter, _ *http.Request) {
	s := f.system
	s.mu.Lock()
	ans := backupTargetsAnswer{Targets: append([]BackupTarget{}, s.backupTargets...)}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, ans)
}

type restoreCheck struct {
	OK             bool   `json:"ok"`
	Output         string `json:"output"`
	BackupVersion  string `json:"backup_version"`
	RunningVersion string `json:"running_version"`
	NeedsKey       bool   `json:"needs_key"`
	HasRega        bool   `json:"has_rega"`
}

type restoreEncryption struct {
	Encrypted        bool `json:"encrypted"`
	NeedsRecoveryKey bool `json:"needs_recovery_key"`
	CreatedHere      bool `json:"created_here"`
}

type restoreCheckAnswer struct {
	File       string            `json:"file"`
	Check      restoreCheck      `json:"check"`
	Encryption restoreEncryption `json:"encryption"`
}

// handleRestoreCheck accepts an archive as a multipart "file" part or
// as the raw body. An empty archive is corrupt; the fake's own backup
// blob checks as created here.
func (f *Fake) handleRestoreCheck(w http.ResponseWriter, r *http.Request) {
	var data []byte
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "multipart/form-data" {
		file, _, err := r.FormFile("file")
		if err == nil {
			data, _ = io.ReadAll(file)
			_ = file.Close()
		}
	} else {
		data, _ = io.ReadAll(io.LimitReader(r.Body, 64<<20))
	}
	if len(data) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "corrupt", "the archive is empty or unreadable")
		return
	}
	s := f.system
	s.mu.Lock()
	name := "upload-" + strconv.Itoa(len(s.uploads)+1) + ".sbk"
	s.uploads[name] = data
	needsKey := s.restoreNeedsKey
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, restoreCheckAnswer{
		File: name,
		Check: restoreCheck{
			OK: true, BackupVersion: fakeBase, RunningVersion: fakeBase,
		},
		Encryption: restoreEncryption{
			Encrypted: needsKey, NeedsRecoveryKey: needsKey,
			CreatedHere: bytes.Equal(data, backupBlob),
		},
	})
}

type restoreApplyBody struct {
	File  string `json:"file"`
	Key   string `json:"key"`
	Force bool   `json:"force"`
}

type restoreApplyAnswer struct {
	OK        bool   `json:"ok"`
	Output    string `json:"output"`
	Rebooting bool   `json:"rebooting"`
}

// handleRestoreApply restores a checked upload and reboots.
func (f *Fake) handleRestoreApply(w http.ResponseWriter, r *http.Request) {
	var body restoreApplyBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad-request", "invalid body")
		return
	}
	s := f.system
	s.mu.Lock()
	_, ok := s.uploads[body.File]
	if ok {
		s.applied = append(s.applied, body.File)
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "restore-failed", "no checked upload "+body.File)
		return
	}
	writeJSON(w, http.StatusOK, restoreApplyAnswer{OK: true, Rebooting: true})
}

type serviceMessagesAnswer struct {
	Count    int              `json:"count"`
	Messages []ServiceMessage `json:"messages"`
}

func (f *Fake) handleServiceMessages(w http.ResponseWriter, _ *http.Request) {
	s := f.system
	s.mu.Lock()
	msgs := append([]ServiceMessage{}, s.serviceMessages...)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, serviceMessagesAnswer{Count: len(msgs), Messages: msgs})
}

type radioInterface struct {
	Interface string  `json:"interface"`
	Address   string  `json:"address"`
	Type      string  `json:"type"`
	Connected bool    `json:"connected"`
	Default   bool    `json:"default"`
	Firmware  string  `json:"firmware"`
	DutyCycle float64 `json:"duty_cycle"`
}

type radioHealth struct {
	Interfaces    []radioInterface `json:"interfaces"`
	Busy          bool             `json:"busy"`
	BusyInterface string           `json:"busy_interface"`
}

// handleRadioHealth reports one connected radio per RF interface.
func (f *Fake) handleRadioHealth(w http.ResponseWriter, _ *http.Request) {
	ans := radioHealth{Interfaces: []radioInterface{}}
	for _, n := range f.interfaceNames() {
		if n != "BidCos-RF" && n != "HmIP-RF" {
			continue
		}
		st, _ := f.ifaceSnapshot(n)
		ans.Interfaces = append(ans.Interfaces, radioInterface{
			Interface: n, Connected: !st.down, Default: true,
		})
	}
	writeJSON(w, http.StatusOK, ans)
}

// SetServiceMessages replaces what /service-messages reports.
func (f *Fake) SetServiceMessages(msgs []ServiceMessage) {
	f.system.mu.Lock()
	f.system.serviceMessages = append([]ServiceMessage{}, msgs...)
	f.system.mu.Unlock()
}

// SetUpdateAvailable makes the release feed offer an update; nil
// withdraws it. Downloading it stages it.
func (f *Fake) SetUpdateAvailable(a *AvailableUpdate) {
	f.system.mu.Lock()
	defer f.system.mu.Unlock()
	if a == nil {
		f.system.available = nil
		return
	}
	c := *a
	f.system.available = &c
}

// SetRestoreNeedsRecoveryKey makes every archive the restore check sees
// one that only the box's recovery key opens.
func (f *Fake) SetRestoreNeedsRecoveryKey(needs bool) {
	f.system.mu.Lock()
	f.system.restoreNeedsKey = needs
	f.system.mu.Unlock()
}

// SetBackupTargets replaces the backup targets /backup/targets reports.
func (f *Fake) SetBackupTargets(targets []BackupTarget) {
	f.system.mu.Lock()
	f.system.backupTargets = append([]BackupTarget(nil), targets...)
	f.system.mu.Unlock()
}

// Restores returns the file names of the archives restore/apply applied.
func (f *Fake) Restores() []string {
	f.system.mu.Lock()
	defer f.system.mu.Unlock()
	return append([]string(nil), f.system.applied...)
}
