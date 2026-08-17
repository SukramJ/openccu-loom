// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rega

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// regaRunScriptMethod is the JSON-RPC method the CCU exposes for
// executing a HomeMatic Script.
const regaRunScriptMethod = "ReGa.runScript"

// placeholderPattern matches every ##NAME## token in a script body.
// Names start with a letter or underscore and continue with word chars.
var placeholderPattern = regexp.MustCompile(`##([A-Za-z_][A-Za-z0-9_]*)##`)

// Config configures a [Runner].
type Config struct {
	// Client is the JSON-RPC transport used to reach the CCU. Required.
	Client *jsonrpc.Client
	// Logger receives structured slog events. If nil, slog.Default().
	Logger *slog.Logger
}

// Runner dispatches ReGa scripts through a JSON-RPC client. Safe for
// concurrent use.
type Runner struct {
	client *jsonrpc.Client
	logger *slog.Logger
}

// NewRunner constructs a Runner.
func NewRunner(cfg Config) (*Runner, error) {
	if cfg.Client == nil {
		return nil, errors.New("rega: Config.Client is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{client: cfg.Client, logger: logger}, nil
}

// Client returns the underlying [jsonrpc.Client]. Adapters that need
// the active CCU session (e.g. the backup restorer's HTTP-multipart
// upload) borrow the client via this getter; the runner remains the
// owner.
func (r *Runner) Client() *jsonrpc.Client { return r.client }

// Run executes script with the given params and returns its raw stdout.
// Placeholder keys missing from params cause an error before any
// network activity — this catches typos fast.
//
// JSON-bearing scripts should use [Runner.RunJSON] instead: it applies
// the control-character sanitisation CCU output often needs.
func (r *Runner) Run(ctx context.Context, script hmenum.RegaScript, params map[string]string) (string, error) {
	body, err := loadScript(script)
	if err != nil {
		return "", err
	}
	substituted, err := substitute(body, params)
	if err != nil {
		return "", fmt.Errorf("rega: %s: %w", script, err)
	}

	r.logger.Debug(
		"rega runScript dispatch",
		slog.String("script", string(script)),
		slog.Int("bytes", len(substituted)),
	)

	var out string
	err = r.client.Call(ctx, regaRunScriptMethod, map[string]any{"script": substituted}, &out)
	if err != nil {
		return "", fmt.Errorf("rega: %s: %w", script, err)
	}
	return out, nil
}

// RunLists runs a script whose parameters include newline-joined lists. Each
// element of every list is validated for control characters individually (a
// single element may not contain any, including a newline that would forge an
// extra entry), then the elements are joined with LF and substituted as one
// value; the join newlines are the only control characters permitted for those
// keys. Single-value params in params keep the strict all-control-char
// rejection. This is how set_device_rooms / set_device_functions pass their
// \n-separated lists without either mis-encoding a legitimate multi-entry
// assignment or reopening the comment-break injection.
func (r *Runner) RunLists(
	ctx context.Context,
	script hmenum.RegaScript,
	params map[string]string,
	lists map[string][]string,
) (string, error) {
	merged := make(map[string]string, len(params)+len(lists))
	for k, v := range params {
		merged[k] = v
	}
	listKeys := make(map[string]bool, len(lists))
	for k, elems := range lists {
		for _, e := range elems {
			if bad, isBad := firstControlChar(e); isBad {
				return "", fmt.Errorf(
					"rega: %s: rejected control character in list param %s (U+%04X): %w",
					script, k, bad, hmerr.ErrValidation,
				)
			}
		}
		merged[k] = strings.Join(elems, "\n")
		listKeys[k] = true
	}
	body, err := loadScript(script)
	if err != nil {
		return "", err
	}
	substituted, err := substituteWithLists(body, merged, listKeys)
	if err != nil {
		return "", fmt.Errorf("rega: %s: %w", script, err)
	}
	var out string
	if err := r.client.Call(ctx, regaRunScriptMethod, map[string]any{"script": substituted}, &out); err != nil {
		return "", fmt.Errorf("rega: %s: %w", script, err)
	}
	return out, nil
}

// RunJSON runs script and unmarshals its (sanitised) output into v.
// v must be a non-nil pointer to a type json.Unmarshal accepts.
func (r *Runner) RunJSON(ctx context.Context, script hmenum.RegaScript, params map[string]string, v any) error {
	raw, err := r.Run(ctx, script, params)
	if err != nil {
		return err
	}
	clean := SanitizeJSONControls(raw)
	if err := json.Unmarshal([]byte(clean), v); err != nil {
		return fmt.Errorf("rega: %s: parse JSON: %w: %w", script, err, hmerr.ErrClientException)
	}
	return nil
}

// AcknowledgeMessage acknowledges a CCU service or alarm message identified
// by `messageID` (the rega ISE-ID). Returns true when the CCU confirmed the
// acknowledgement, false otherwise.
//
// Service messages require the trigger DP to be writable; alarm messages are
// unconditionally acknowledgeable. The script's stringified JSON return is
// parsed here; on parse failure the call returns `false, err`.
func (r *Runner) AcknowledgeMessage(ctx context.Context, messageID string) (bool, error) {
	if messageID == "" {
		return false, errors.New("rega: AcknowledgeMessage: messageID is required")
	}
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := r.RunJSON(ctx, hmenum.RegaScriptAcknowledgeMessage, map[string]string{
		"message_id": messageID,
	}, &resp); err != nil {
		return false, fmt.Errorf("rega.AcknowledgeMessage(%s): %w", messageID, err)
	}
	if !resp.Success && resp.Error != "" {
		// CCU reported a structured failure (e.g. message not found,
		// not acknowledgeable). Return the boolean as-is and surface
		// the reason via the wrapped error so callers can distinguish
		// transport failure from CCU-level rejection.
		return false, fmt.Errorf("rega.AcknowledgeMessage(%s): %s", messageID, resp.Error)
	}
	return resp.Success, nil
}

// AcknowledgeAllServiceMessages acknowledges every quittable service message
// on the CCU in a single ReGa pass and returns the number acknowledged. Only
// messages whose trigger data point is writable are acknowledged; the rest are
// left untouched (mirroring the single-message writability gate).
func (r *Runner) AcknowledgeAllServiceMessages(ctx context.Context) (int, error) {
	var resp struct {
		Acknowledged int `json:"acknowledged"`
	}
	if err := r.RunJSON(ctx, hmenum.RegaScriptAcknowledgeAllServiceMessages, nil, &resp); err != nil {
		return 0, fmt.Errorf("rega.AcknowledgeAllServiceMessages: %w", err)
	}
	return resp.Acknowledged, nil
}

// AcknowledgeAllAlarmMessages acknowledges every active alarm message on the
// CCU in a single ReGa pass and returns the number acknowledged. Alarm
// messages are acknowledged unconditionally.
func (r *Runner) AcknowledgeAllAlarmMessages(ctx context.Context) (int, error) {
	var resp struct {
		Acknowledged int `json:"acknowledged"`
	}
	if err := r.RunJSON(ctx, hmenum.RegaScriptAcknowledgeAllAlarmMessages, nil, &resp); err != nil {
		return 0, fmt.Errorf("rega.AcknowledgeAllAlarmMessages: %w", err)
	}
	return resp.Acknowledged, nil
}

// SetProgramState activates or deactivates the CCU automation program
// identified by its ISE-ID (pid). state=true enables the program, false
// disables it. Returns without error when the CCU accepted the change.
//
// Implemented as a ReGa script (set_program_state.fn) because the CCU does
// not expose a JSON-RPC method for this operation.
func (r *Runner) SetProgramState(ctx context.Context, pid string, state bool) error {
	stateStr := "0"
	if state {
		stateStr = "1"
	}
	_, err := r.Run(ctx, hmenum.RegaScriptSetProgramState, map[string]string{
		"id":    pid,
		"state": stateStr,
	})
	return err
}

// ExecuteProgramConditional evaluates the CCU automation program's "if"
// condition (identified by its ISE-ID pid) and runs the program only when
// the condition is currently satisfied. It returns whether the program
// actually executed.
//
// Implemented as a ReGa script (execute_program_conditional.fn) because the
// CCU's JSON-RPC Program.execute runs unconditionally and exposes no
// condition-gated variant.
func (r *Runner) ExecuteProgramConditional(ctx context.Context, pid string) (bool, error) {
	var resp struct {
		Executed bool `json:"executed"`
	}
	if err := r.RunJSON(ctx, hmenum.RegaScriptExecuteProgramConditional, map[string]string{
		"id": pid,
	}, &resp); err != nil {
		return false, fmt.Errorf("rega.ExecuteProgramConditional(%s): %w", pid, err)
	}
	return resp.Executed, nil
}

// SystemUpdateInfo holds the result of [Runner.GetSystemUpdateInfo].
// Fields mirror py:2149).
type SystemUpdateInfo struct {
	CurrentFirmware      string `json:"current_firmware"`
	AvailableFirmware    string `json:"available_firmware"`
	UpdateAvailable      bool   `json:"update_available"`
	CheckScriptAvailable bool   `json:"check_script_available"`
}

// GetSystemUpdateInfo queries the CCU for its current and available firmware
// versions by running the get_system_update_info.fn ReGa script.
func (r *Runner) GetSystemUpdateInfo(ctx context.Context) (SystemUpdateInfo, error) {
	var info SystemUpdateInfo
	if err := r.RunJSON(ctx, hmenum.RegaScriptGetSystemUpdateInfo, nil, &info); err != nil {
		return SystemUpdateInfo{}, err
	}
	return info, nil
}

// InboxDevice holds a single entry returned by [Runner.GetInboxDevices].
// Fields mirror py:2102).
type InboxDevice struct {
	DeviceID   string `json:"id"`
	Address    string `json:"address"`
	Name       string `json:"name"`
	DeviceType string `json:"type"`
	Interface  string `json:"interface"`
}

// GetInboxDevices returns all devices that have been paired with the CCU but
// not yet configured (the "inbox"). Runs the get_inbox_devices.fn ReGa
// script.
func (r *Runner) GetInboxDevices(ctx context.Context) ([]InboxDevice, error) {
	var devices []InboxDevice
	if err := r.RunJSON(ctx, hmenum.RegaScriptGetInboxDevices, nil, &devices); err != nil {
		return nil, err
	}
	return devices, nil
}

// AlarmMessage is one entry returned by [Runner.GetAlarmMessages].
// Fields mirror the JSON emitted by get_alarm_messages.fn.
//
// There is deliberately no device, room or trigger field: an alarm
// system variable is raised from a program rather than by a device, so
// the CCU reports its trigger data point as the 65535 "unknown"
// sentinel. Device-bound alerts are service messages — see
// [ServiceMessage].
type AlarmMessage struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Timestamp and LastTimestamp are Unix seconds in UTC. 0 means the
	// occurrence never happened, which is the normal state of
	// LastTimestamp for a variable that has been raised exactly once.
	// Unix seconds avoid the CCU's local-time rendering, which carries
	// no zone offset and is therefore not parsable on its own.
	Timestamp     int64 `json:"timestamp"`
	LastTimestamp int64 `json:"last_timestamp"`
	Counter       int   `json:"counter"`
}

// GetAlarmMessages returns all active alarm messages from the CCU by running
// the get_alarm_messages.fn ReGa script. Name and Description are
// URL-encoded; callers should apply url.QueryUnescape before display.
func (r *Runner) GetAlarmMessages(ctx context.Context) ([]AlarmMessage, error) {
	var messages []AlarmMessage
	if err := r.RunJSON(ctx, hmenum.RegaScriptGetAlarmMessages, nil, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

// ServiceMessage is one entry returned by [Runner.GetServiceMessages].
// Fields mirror the JSON emitted by get_service_messages.fn.
type ServiceMessage struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Timestamp and LastTimestamp are Unix seconds in UTC. 0 means the
	// occurrence never happened, mirroring [AlarmMessage].
	Timestamp     int64    `json:"timestamp"`
	Type          int      `json:"type"`
	Address       string   `json:"address"`
	DeviceName    string   `json:"device_name"`
	LastTimestamp int64    `json:"last_timestamp"`
	Counter       int      `json:"counter"`
	Rooms         []string `json:"rooms"`
	Functions     []string `json:"functions"`
	Quittable     bool     `json:"quittable"`
}

// GetServiceMessages returns all active service messages from the CCU by
// running the get_service_messages.fn ReGa script. Name, DeviceName, and
// each entry of Rooms and Functions are URL-encoded; callers should apply
// url.QueryUnescape before display.
func (r *Runner) GetServiceMessages(ctx context.Context) ([]ServiceMessage, error) {
	var messages []ServiceMessage
	if err := r.RunJSON(ctx, hmenum.RegaScriptGetServiceMessages, nil, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

// BackendInfo holds the result of [Runner.GetBackendInfo].
type BackendInfo struct {
	Version string `json:"version"`
	Product string `json:"product"`
	// Hostname is the CCU's network hostname.
	Hostname string `json:"hostname"`
	// IsHAApp reports whether the CCU is running inside a Home-Assistant
	// supervisor environment.
	IsHAApp bool `json:"is_ha_app"`
	// Longitude and Latitude are the CCU's astro reference position in
	// decimal degrees. Every sunrise/sunset time the CCU computes derives
	// from them, so a wrong position skews schedules rather than failing.
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
	// Timezone is the IANA zone from the CCU's time configuration
	// (e.g. "Europe/Berlin"). Read-only: it is set on the CCU itself.
	Timezone string `json:"timezone"`
}

// positionEpsilon bounds the read-back comparison in [Runner.SetPosition].
// The CCU renders six decimals, so a mismatch beyond this is a genuine
// rejection rather than a rounding artefact - 1e-5 degrees is about a
// metre, far below anything astro calculations care about.
const positionEpsilon = 1e-5

// SetPosition writes the CCU's astro reference position and confirms it
// by comparing the script's read-back against what was sent.
//
// The ranges are validated here rather than in the script: a ReGa script
// receives its parameters by textual substitution, so an out-of-range or
// non-finite value would be written verbatim before anything could
// object. Rejecting first also keeps the failure legible - the caller
// learns which bound it violated instead of reading a skewed read-back.
func (r *Runner) SetPosition(ctx context.Context, longitude, latitude float64) (gotLon, gotLat float64, err error) {
	switch {
	case math.IsNaN(longitude) || math.IsInf(longitude, 0):
		return 0, 0, fmt.Errorf("rega: longitude must be a finite number: %w", hmerr.ErrValidation)
	case math.IsNaN(latitude) || math.IsInf(latitude, 0):
		return 0, 0, fmt.Errorf("rega: latitude must be a finite number: %w", hmerr.ErrValidation)
	case longitude < -180 || longitude > 180:
		return 0, 0, fmt.Errorf("rega: longitude %g out of range [-180,180]: %w", longitude, hmerr.ErrValidation)
	case latitude < -90 || latitude > 90:
		return 0, 0, fmt.Errorf("rega: latitude %g out of range [-90,90]: %w", latitude, hmerr.ErrValidation)
	}
	var got BackendInfo
	err = r.RunJSON(ctx, hmenum.RegaScriptSetCCUPosition, map[string]string{
		"longitude": strconv.FormatFloat(longitude, 'f', 6, 64),
		"latitude":  strconv.FormatFloat(latitude, 'f', 6, 64),
	}, &got)
	if err != nil {
		return 0, 0, err
	}
	if math.Abs(got.Longitude-longitude) > positionEpsilon || math.Abs(got.Latitude-latitude) > positionEpsilon {
		return 0, 0, fmt.Errorf(
			"rega: CCU read back position %g/%g after writing %g/%g: %w",
			got.Longitude, got.Latitude, longitude, latitude, hmerr.ErrClientException,
		)
	}
	return got.Longitude, got.Latitude, nil
}

// GetBackendInfo queries the CCU for its firmware version, product label,
// hostname, and HA-App classification by running the get_backend_info.fn
// ReGa script.
func (r *Runner) GetBackendInfo(ctx context.Context) (BackendInfo, error) {
	var info BackendInfo
	if err := r.RunJSON(ctx, hmenum.RegaScriptGetBackendInfo, nil, &info); err != nil {
		return BackendInfo{}, err
	}
	return info, nil
}

// GetSerial queries the CCU for its hardware serial number by running the
// get_serial.fn ReGa script. Returns an empty string when the CCU cannot
// determine the serial.
//
// The CCU may report the full radio-module serial; only the last 10
// characters are the canonical hardware serial shown in the WebUI. Clients
// embed this value as the central-id slot of their unique_ids, so it must
// match that canonical form byte for byte.
func (r *Runner) GetSerial(ctx context.Context) (string, error) {
	var result struct {
		Serial string `json:"serial"`
	}
	if err := r.RunJSON(ctx, hmenum.RegaScriptGetSerial, nil, &result); err != nil {
		return "", err
	}
	// Canonical per-CCU serial (last 10, case preserved). Shared with SSDP
	// discovery via routingkey.CanonicalSerial so the runtime identity and the
	// discovery identity are byte-identical.
	return routingkey.CanonicalSerial(result.Serial), nil
}

// ProgramDescription is one entry returned by [Runner.GetProgramDescriptions].
type ProgramDescription struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	// ConditionSummary is a compact, language-neutral rendering of the
	// program's root-rule trigger conditions (object names joined by the
	// symbolic operators ==, >=, <=, >, <, &&, ||). Empty when the program
	// has no rule. URL-encoded on the wire.
	ConditionSummary string `json:"condition_summary"`
	// ActivitySummary is a compact, language-neutral rendering of the
	// program's root-rule activities (object name := value, joined by "; ").
	// Empty when the program has no rule. URL-encoded on the wire.
	ActivitySummary string `json:"activity_summary"`
	// LastExecuteSeconds is when the CCU last ran the program, in Unix
	// seconds (UTC); 0 when it never ran. Program.getAll reports the same
	// instant as a CCU-local wall-clock string that carries no zone offset
	// and is therefore not parsable on its own, so the script reads the
	// *Seconds() accessor instead — same rationale as [ServiceMessage].
	// A CCU whose script predates the field decodes to 0, which callers
	// treat as "never ran".
	LastExecuteSeconds int64 `json:"last_execute_seconds"`
}

// GetProgramDescriptions returns the URI-encoded description string and the
// compact rule summaries for every CCU automation program by running the
// get_program_descriptions.fn ReGa script. The Description, ConditionSummary,
// and ActivitySummary field values are URL-encoded; callers should apply
// url.QueryUnescape before display.
func (r *Runner) GetProgramDescriptions(ctx context.Context) ([]ProgramDescription, error) {
	var descs []ProgramDescription
	if err := r.RunJSON(ctx, hmenum.RegaScriptGetProgramDescriptions, nil, &descs); err != nil {
		return nil, err
	}
	return descs, nil
}

// SysvarUsageProgram is one program returned by [Runner.SysvarUsagePrograms].
// Name is URL-encoded; callers apply url.QueryUnescape before display.
type SysvarUsageProgram struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// SysvarUsagePrograms lists the CCU programs that reference the named
// system variable (usage_by_sysvar.fn). The script walks the program
// rules rather than the variable's own usage index, which is empty on
// some CCU installations and reports no error when it is. An unknown
// variable yields an empty slice. Each program's Name is URL-encoded.
//
// References inside else-if sub-rules and inside script-type activities
// are not visible to that walk; see the script header.
func (r *Runner) SysvarUsagePrograms(ctx context.Context, name string) ([]SysvarUsageProgram, error) {
	var out []SysvarUsageProgram
	if err := r.RunJSON(ctx, hmenum.RegaScriptUsageBySysvar, map[string]string{"name": name}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SystemVariableDescription is one entry returned by
// [Runner.GetSystemVariableDescriptions].
type SystemVariableDescription struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	// ChannelAddress is the address of the channel explicitly assigned to
	// the variable in the CCU WebUI ("Kanalzuordnung"); empty when the
	// variable has no assignment. Older CCU firmwares (or cached script
	// results) that omit the field decode to the empty string, so callers
	// treat "" as "no explicit assignment".
	ChannelAddress string `json:"channel_address"`
	// AlarmState is the triggered flag of an ALARM variable: "1" raised,
	// "0" not raised, "" for every variable that is not of the alarm
	// sub-type (and for a script result that predates the field). It is
	// the only state source for these variables: the CCU keeps it in
	// AlState() and answers SysVar.getAll with an empty value for them.
	AlarmState string `json:"alarm_state"`
}

// GetSystemVariableDescriptions returns the URI-encoded description string
// and the explicitly assigned channel address for every CCU system variable
// by running the get_system_variable_descriptions.fn ReGa script. The
// Description and ChannelAddress field values are URL-encoded; callers
// should apply url.QueryUnescape before use.
func (r *Runner) GetSystemVariableDescriptions(ctx context.Context) ([]SystemVariableDescription, error) {
	var descs []SystemVariableDescription
	if err := r.RunJSON(ctx, hmenum.RegaScriptGetSystemVariableDescriptions, nil, &descs); err != nil {
		return nil, err
	}
	return descs, nil
}

// SetSystemVariableString writes a string-typed system variable on the CCU.
// The ReGa path is required for string values; bool and float/enum variants
// have dedicated JSON-RPC methods (see [jsonrpc.Client.SetSystemVariableBool]
// and [jsonrpc.Client.SetSystemVariableFloat]).
func (r *Runner) SetSystemVariableString(ctx context.Context, name, value string) error {
	_, err := r.Run(ctx, hmenum.RegaScriptSetSystemVariable, map[string]string{
		"name":  name,
		"value": value,
	})
	return err
}

// substitute resolves every ##NAME## placeholder in body. Every key
// referenced in body must have an entry in params; unreferenced params
// are allowed (they just go unused). The replacement value is escaped
// via EscapeString so that it cannot break out of a double-quoted ReGa
// string literal, and any value carrying a control character is rejected
// outright (see firstControlChar) so it cannot break out of a `!# …`
// line comment either. Both failure modes name the offending placeholders
// and return before any network activity.
func substitute(body string, params map[string]string) (string, error) {
	return substituteWithLists(body, params, nil)
}

// substituteWithLists replaces the placeholders in body with the values in
// params. Keys named in listKeys are newline-joined lists: their value may
// carry the LF separators the caller inserted (they land inside a quoted
// string literal the script splits, never in a comment — see RunLists), so
// only non-newline control characters are rejected for them. Every other key
// rejects all control characters, because a value that reaches a `!#` comment
// context could otherwise terminate the comment and inject script — the class
// of defect the sysvar write path was found to have.
func substituteWithLists(body string, params map[string]string, listKeys map[string]bool) (string, error) {
	missing := map[string]struct{}{}
	unsafe := map[string]rune{}
	out := placeholderPattern.ReplaceAllStringFunc(body, func(match string) string {
		name := match[2 : len(match)-2]
		v, ok := params[name]
		if !ok {
			missing[name] = struct{}{}
			return match
		}
		r, bad := firstControlChar(v)
		if bad && listKeys[name] && r == '\n' {
			// A list key may only carry the LF separators RunLists inserted;
			// re-scan for any OTHER control character, which is never allowed.
			r, bad = firstNonNewlineControlChar(v)
		}
		if bad {
			if _, seen := unsafe[name]; !seen {
				unsafe[name] = r
			}
			return match
		}
		return EscapeString(v)
	})
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for n := range missing {
			names = append(names, n)
		}
		sort.Strings(names)
		return "", fmt.Errorf("missing params: %s", strings.Join(names, ", "))
	}
	if len(unsafe) > 0 {
		names := make([]string, 0, len(unsafe))
		for n := range unsafe {
			names = append(names, fmt.Sprintf("%s (U+%04X)", n, unsafe[n]))
		}
		sort.Strings(names)
		return "", fmt.Errorf(
			"rejected control character in params: %s: %w",
			strings.Join(names, ", "), hmerr.ErrValidation,
		)
	}
	return out, nil
}

// EscapeString makes v safe to interpolate inside a double-quoted ReGa
// string literal. The two dangerous characters are the backslash and
// the double-quote; everything else passes through unchanged.
//
// It deliberately does NOT touch control characters: doubling the
// backslash and escaping the quote leaves a newline in place, and a
// newline breaks out of a `!# …` line comment or the surrounding "…"
// literal regardless of the quoting. Control characters are neutralised
// one layer up, in substitute, which rejects them before they reach here.
func EscapeString(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return v
}

// firstControlChar reports the first C0 control character (rune < 0x20)
// or DEL (0x7F) in v, and whether one was found.
//
// A ReGa script receives its parameters by textual substitution (see
// substitute), and several scripts interpolate a placeholder inside a
// `!# …` line comment — a comment that ends at the first newline. A value
// carrying a line terminator therefore closes the comment (or the
// surrounding "…" string literal) and the CCU parses everything after it
// as privileged ReGa statements on its service session. EscapeString does
// not stop this: it is the newline, not the quote, that ends the line, so
// the escaped-quote/backslash combination cannot smuggle a value past this
// check either — it runs on the raw value before any escaping.
//
// The neutralisation therefore has to reject the control character itself.
// We fail closed rather than re-encode it because the ReGa tokeniser is a
// closed-source binary whose whitespace / line-break class we cannot
// verify, so no encoding can be proven reversible. This mirrors the ReGa
// username defence in internal/auth/ccuauth/store.go, and it is strictly
// better than the status quo: a benign multi-line value already fails
// silently on the CCU today (the post-newline text parse-errors and the
// write returns empty output), so a loud rejection surfaces a failure that
// was previously invisible.
func firstControlChar(v string) (rune, bool) {
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return r, true
		}
	}
	return 0, false
}

// firstNonNewlineControlChar is firstControlChar for list values: the LF
// separators RunLists inserts are permitted, every other control character
// (CR, TAB, NUL, DEL, …) is still reported.
func firstNonNewlineControlChar(v string) (rune, bool) {
	for _, r := range v {
		if r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return r, true
		}
	}
	return 0, false
}

// SanitizeJSONControls escapes control characters (ASCII < 0x20) that
// appear inside JSON string values, rewriting them as \uXXXX escapes.
// Whitespace outside string values is preserved — structural newlines
// between array elements stay valid JSON.
//
// The CCU occasionally emits device names with embedded newlines or
// tabs. Feeding such output directly to [json.Unmarshal] fails because
// the spec forbids raw control chars in strings; sanitising first
// yields valid JSON without losing information.
func SanitizeJSONControls(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && inString:
			b.WriteRune(r)
			escaped = true
		case r == '"':
			b.WriteRune(r)
			inString = !inString
		case inString && r < 0x20:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
