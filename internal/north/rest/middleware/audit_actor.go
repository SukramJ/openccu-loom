// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package middleware

import (
	"log/slog"
	"net/http"
	"unicode"
	"unicode/utf8"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// OnBehalfOfHeader names the person an entry credential acts for. A relay
// that forwards a Home Assistant user's edit sends e.g. `Alex (ha:<id>)`.
const OnBehalfOfHeader = "X-On-Behalf-Of"

// onBehalfOfMaxBytes bounds the header: it is a display label for the change
// log, not a document, and every row stores it.
const onBehalfOfMaxBytes = 128

// AuditActor attaches the request's [audit.Actor] once authentication has
// resolved an identity, so every change-log row the request produces names
// the same principal and the same assertion.
//
// The principal is always the authenticated credential. `X-On-Behalf-Of` is
// accepted only from an entry credential — an API token (`bearer`) or a box
// API token (`occulite-token`), the credentials an integration holds on its
// users' behalf. From any other principal it is ignored and logged at debug
// level: a browser session speaks for its own user and has no one to name.
// An accepted value is recorded as asserted and never used for authorization.
// A value that is too long, not UTF-8, carries control characters or is sent
// more than once answers 400, because a silently dropped assertion would
// leave a row that looks like the integration acted on its own.
//
// It must run after the global identity resolver and before the handlers.
// A request without an identity passes unchanged.
func AuditActor(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := auth.IdentityFrom(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			actor := audit.Actor{User: id.Subject}
			if values := r.Header.Values(OnBehalfOfHeader); len(values) > 0 {
				switch {
				case id.Scheme != auth.SchemeBearer && id.Scheme != auth.SchemeOcculiteToken:
					logger.DebugContext(r.Context(), "ignoring X-On-Behalf-Of from a non-entry credential",
						"scheme", string(id.Scheme))
				case len(values) > 1:
					problem.Write(w, http.StatusBadRequest, problem.New(problem.TypeBadRequest, r,
						"Invalid "+OnBehalfOfHeader, "the header may be sent once"))
					return
				default:
					if reason := invalidOnBehalfOf(values[0]); reason != "" {
						problem.Write(w, http.StatusBadRequest, problem.New(problem.TypeBadRequest, r,
							"Invalid "+OnBehalfOfHeader, reason))
						return
					}
					actor.OnBehalfOf = values[0]
				}
			}
			next.ServeHTTP(w, r.WithContext(audit.WithActor(r.Context(), actor)))
		})
	}
}

// invalidOnBehalfOf names what is wrong with v, or returns "" for a value the
// change log may store. An empty value is wrong too: a relay that sends the
// header has someone to name.
func invalidOnBehalfOf(v string) string {
	switch {
	case v == "":
		return "the value is empty"
	case len(v) > onBehalfOfMaxBytes:
		return "the value is longer than 128 bytes"
	case !utf8.ValidString(v):
		return "the value is not UTF-8"
	}
	for _, c := range v {
		// Format characters (bidi overrides, zero-width marks) are invisible
		// and would make the stored label display as a different name.
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return "the value carries a control or format character"
		}
	}
	return ""
}
