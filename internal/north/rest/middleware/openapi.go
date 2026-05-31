// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// OpenAPIValidator binds a parsed [openapi3.T] document to a runtime
// router so the [OpenAPIValidator.Middleware] can validate every
// inbound request against its operation. Constructed once at boot via
// [NewOpenAPIValidator] and shared across the request hot path.
//
// Closes audit R10: the spec was historically only a doc artifact
// even though `kin-openapi` was already in `go.mod`. Activating the
// validator at the REST boundary makes silent drift between handlers
// and `assets/openapi.yaml` impossible.
type OpenAPIValidator struct {
	doc    *openapi3.T
	router routers.Router
	// logger surfaces validation failures at info level so operators
	// can spot client-side bugs without burying the response stream
	// in error noise. nil falls back to slog.Default().
	logger *slog.Logger
	// failOpen, when true, lets requests through if FindRoute returns
	// ErrPathNotFound — useful for endpoints that exist in code but
	// have not been backfilled into the spec yet. Production sets it
	// to false (every endpoint must be in the spec).
	failOpen bool
}

// OpenAPIValidatorConfig parametrises [NewOpenAPIValidator].
type OpenAPIValidatorConfig struct {
	// Spec is the YAML/JSON-encoded OpenAPI 3.1 document. Required.
	Spec []byte
	// BasePath strips a leading prefix from incoming request paths
	// before route matching. The daemon mounts the spec under
	// `/api/v1`, but the spec itself is rooted at `/`; pass `/api/v1`
	// here so routes match.
	BasePath string
	// Logger receives one info-level record per validation rejection.
	Logger *slog.Logger
	// FailOpen lets unspecced paths through (useful during a spec
	// migration). Default false — the safer setting once the spec is
	// considered authoritative.
	FailOpen bool
}

// NewOpenAPIValidator parses cfg.Spec and returns a ready validator.
// The spec is validated for structural correctness up-front so a
// broken spec fails the daemon at boot rather than at first request.
func NewOpenAPIValidator(cfg OpenAPIValidatorConfig) (*OpenAPIValidator, error) {
	if len(cfg.Spec) == 0 {
		return nil, errors.New("openapi: empty spec")
	}
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromData(cfg.Spec)
	if err != nil {
		return nil, fmt.Errorf("openapi: load: %w", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("openapi: validate: %w", err)
	}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, fmt.Errorf("openapi: router: %w", err)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &OpenAPIValidator{
		doc:      doc,
		router:   router,
		logger:   logger,
		failOpen: cfg.FailOpen,
	}, nil
}

// Middleware returns a chi-compatible middleware that validates every
// incoming request against the OpenAPI document. Validation failures
// produce a `problem+json` response with the HTTP status that fits
// the failure (400 for bad parameters, 404 for unknown route, 405 for
// wrong method).
//
// Path resolution: the gorillamux router built from the spec uses
// `servers[].url` as a path prefix, so production paths arrive at
// FindRoute in their full form (`/api/v1/<...>`). The middleware
// passes [http.Request.URL.Path] through unchanged.
func (v *OpenAPIValidator) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route, pathParams, err := v.router.FindRoute(r)
			if err != nil {
				if v.failOpen && errors.Is(err, routers.ErrPathNotFound) {
					next.ServeHTTP(w, r)
					return
				}
				v.respond(w, r, err)
				return
			}

			// The validator may consume the body; restore it for the
			// downstream handler via a tee buffer.
			var bodyCopy []byte
			validatorReq := r
			if r.Body != nil && r.ContentLength > 0 {
				buf, readErr := io.ReadAll(r.Body)
				if readErr == nil {
					bodyCopy = buf
					vr := r.Clone(r.Context())
					vr.Body = io.NopCloser(bytes.NewReader(buf))
					validatorReq = vr
					r.Body = io.NopCloser(bytes.NewReader(buf))
				}
			}

			input := &openapi3filter.RequestValidationInput{
				Request:    validatorReq,
				PathParams: pathParams,
				Route:      route,
				Options: &openapi3filter.Options{
					AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
				},
			}
			if err := openapi3filter.ValidateRequest(r.Context(), input); err != nil {
				v.respond(w, r, err)
				return
			}
			// Restore body just in case the validator left the readers
			// in a partially-consumed state.
			if bodyCopy != nil {
				r.Body = io.NopCloser(bytes.NewReader(bodyCopy))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// respond writes a problem+json describing the validation failure.
// The HTTP status follows kin-openapi's RouteError when available;
// generic ValidateRequest errors land as 400.
func (v *OpenAPIValidator) respond(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusBadRequest
	title := "Request does not match OpenAPI spec"
	switch {
	case errors.Is(err, routers.ErrPathNotFound):
		status = http.StatusNotFound
		title = "Route not found in OpenAPI spec"
	case errors.Is(err, routers.ErrMethodNotAllowed):
		status = http.StatusMethodNotAllowed
		title = "Method not allowed by OpenAPI spec"
	}
	v.logger.LogAttrs(
		r.Context(), slog.LevelInfo, "rest.openapi.reject",
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Int("status", status),
		slog.String("err", err.Error()),
	)
	problem.Write(w, status,
		problem.New(problem.TypeBadRequest, r, title, err.Error()))
}
