// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
)

// alarmPanelItem is the leaf of the alarm zone's panel item: the
// `alarm_control_panel` state under `status`, its `ARM_*` commands under
// `set` (ADR 0083: one path, state tokens out, command tokens in).
const alarmPanelItem = "panel"

// payloadRedactor renders a command payload for a log line with whatever it
// must not reveal removed.
type payloadRedactor func([]byte) string

// normalized wraps a route handler with mqtt-smarthome 2.0 §5.3's `set`
// normalisation, through the shared [hapublisher.ParseSet] so this plane and
// the bridges read one grammar:
//
//   - a plain value and `{"val": …}` arrive at the handler as the same plain
//     payload — a string without its quotes, a number or boolean as its JSON
//     literal;
//   - any other JSON object or array arrives unchanged, as structured
//     parameters (ADR 0009's service-method bodies, the alarm panel's
//     `{"action", "code"}`, the custom-DP operation body);
//   - an empty payload, or `{"val": null}`, never reaches the handler. It is
//     logged at debug: an empty message is also what clearing a retained
//     topic looks like on a live subscription, so it is not a request;
//   - malformed JSON is a rejected request and logged at warn with its topic
//     and payload (spec §3.3).
//
// Retained `set` messages never get here: the router drops them
// ([hapublisher.CommandConfig.DeliverRetained] is off).
func (c *CommandSubscriber) normalized(h hapublisher.CommandHandler) hapublisher.CommandHandler {
	return c.normalizedRedacted(h, nil)
}

// normalizedRedacted is [CommandSubscriber.normalized] for a route whose
// payload carries a secret: the rejection log renders the payload through
// redact.
func (c *CommandSubscriber) normalizedRedacted(h hapublisher.CommandHandler, redact payloadRedactor) hapublisher.CommandHandler {
	return func(ctx context.Context, cmd hapublisher.Command) {
		v, err := hapublisher.ParseSet(cmd.Payload)
		switch {
		case errors.Is(err, hapublisher.ErrEmptySet):
			c.logger.Debug("mqtt.command.empty_set", slog.String("topic", cmd.Topic))
			return
		case err != nil:
			c.logger.Warn("mqtt.command.rejected",
				slog.String("topic", cmd.Topic),
				slog.String("payload", renderPayload(cmd.Payload, redact)),
				slog.String("err", err.Error()))
			return
		}
		if v.Structured() {
			cmd.Payload = v.Params
		} else {
			cmd.Payload = []byte(v.Text)
		}
		h(ctx, cmd)
	}
}

// setAttrs are the attributes every rejected or failed `set` is logged with:
// the topic and the (normalised) payload, as spec §3.3 asks.
func (c *CommandSubscriber) setAttrs(cmd hapublisher.Command) []any {
	return []any{
		slog.String("topic", cmd.Topic),
		slog.String("payload", string(cmd.Payload)),
	}
}

// alarmAttrs is [CommandSubscriber.setAttrs] for the alarm panel: the
// payload is logged with its `code` redacted, because a log line is read by
// people who must not learn a disarm code from it.
func (c *CommandSubscriber) alarmAttrs(cmd hapublisher.Command) []any {
	return []any{
		slog.String("topic", cmd.Topic),
		slog.String("payload", redactAlarmCode(cmd.Payload)),
	}
}

func renderPayload(payload []byte, redact payloadRedactor) string {
	if redact != nil {
		return redact(payload)
	}
	return string(payload)
}

// redactedCode is what a redacted alarm code reads as in a log line.
const redactedCode = "***"

// redactAlarmCode renders an alarm command payload with every `code` field
// replaced by [redactedCode] — at the top level of the JSON command form and
// inside a `{"val": {…}}` wrapper. A plain token carries no code and is
// returned as is. A payload that opens like JSON but does not parse could
// carry a code the parser cannot locate, so it is not echoed at all.
func redactAlarmCode(payload []byte) string {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return string(trimmed)
	}
	// Wire-decoded JSON before type-dispatch: the payload is any JSON value
	// a broker client chose to send.
	var doc any
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return "<redacted: malformed JSON>"
	}
	redactCodeFields(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		return "<redacted>"
	}
	return string(out)
}

// redactCodeFields replaces every `code` value in doc, at any depth.
func redactCodeFields(doc any) {
	switch v := doc.(type) {
	case map[string]any:
		for k, inner := range v {
			if k == "code" {
				v[k] = redactedCode
				continue
			}
			redactCodeFields(inner)
		}
	case []any:
		for _, inner := range v {
			redactCodeFields(inner)
		}
	}
}
