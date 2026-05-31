// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rega

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
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
type AlarmMessage struct {
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

// GetAlarmMessages returns all active alarm messages from the CCU by running
// the get_alarm_messages.fn ReGa script. Name, Description, DeviceName,
// LastTrigger and Rooms are URL-encoded; callers should apply
// url.QueryUnescape before display.
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

// GetServiceMessages returns all active service messages from the CCU by
// running the get_service_messages.fn ReGa script. Name, DeviceName, Rooms
// and Functions are URL-encoded; callers should apply url.QueryUnescape
// before display.
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
func (r *Runner) GetSerial(ctx context.Context) (string, error) {
	var result struct {
		Serial string `json:"serial"`
	}
	if err := r.RunJSON(ctx, hmenum.RegaScriptGetSerial, nil, &result); err != nil {
		return "", err
	}
	return result.Serial, nil
}

// ProgramDescription is one entry returned by [Runner.GetProgramDescriptions].
type ProgramDescription struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// GetProgramDescriptions returns the URI-encoded description string for every
// CCU automation program by running the get_program_descriptions.fn ReGa
// script. The Description field values are URL-encoded; callers should apply
// url.QueryUnescape before display.
func (r *Runner) GetProgramDescriptions(ctx context.Context) ([]ProgramDescription, error) {
	var descs []ProgramDescription
	if err := r.RunJSON(ctx, hmenum.RegaScriptGetProgramDescriptions, nil, &descs); err != nil {
		return nil, err
	}
	return descs, nil
}

// SystemVariableDescription is one entry returned by
// [Runner.GetSystemVariableDescriptions].
type SystemVariableDescription struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// GetSystemVariableDescriptions returns the URI-encoded description string for
// every CCU system variable by running the get_system_variable_descriptions.fn
// ReGa script. The Description field values are URL-encoded; callers should
// apply url.QueryUnescape before display.
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
// string literal.
func substitute(body string, params map[string]string) (string, error) {
	missing := map[string]struct{}{}
	out := placeholderPattern.ReplaceAllStringFunc(body, func(match string) string {
		name := match[2 : len(match)-2]
		v, ok := params[name]
		if !ok {
			missing[name] = struct{}{}
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
	return out, nil
}

// EscapeString makes v safe to interpolate inside a double-quoted ReGa
// string literal. The two dangerous characters are the backslash and
// the double-quote; everything else passes through unchanged.
func EscapeString(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return v
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
