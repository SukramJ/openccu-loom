# Security Model & Audit Checklist

This document tracks the security posture of OpenCCU-Loom 0.1.0.
It is updated every release; each item links to a test or code
region that enforces the invariant.

## Threat model

| Asset | Threat | Mitigation |
|---|---|---|
| CCU credentials | Credential theft via configuration leak | Read-only config in `/api/v1/config`; Basic/OIDC/Token secrets are never echoed |
| Session cookie | XSS / CSRF | `HttpOnly`, `Secure`, `SameSite=Lax` cookies; CSRF double-submit middleware |
| API tokens | Constant-time comparison to avoid timing leaks | `subtle.ConstantTimeCompare` in `internal/auth.MemoryTokenStore` |
| XML-RPC / BIN-RPC callbacks | Malformed payload | Parsers reject on framing errors; 1 MiB payload cap |
| REST request body | Overlarge JSON | Request timeout middleware + `http.Server.ReadHeaderTimeout` |
| MQTT commands | Replay / injection | QoS 1 + Idempotency-Key middleware on HTTP writes; QoS defaults per category |

## Auth flows

- **Basic** — form-free HTTP Basic, backed by `MemoryUserStore`. Pins a constant-time password compare.
- **Bearer** — `Authorization: Bearer <token>`. Tokens compared in constant time.
- **Session** — HTTP-only cookie issued after successful `/login` POST. Revoked via `/logout`.
- **OIDC** — authorization-code flow with PKCE (RFC 7636). Discovery via `.well-known/openid-configuration`. ID-token signatures are verified against the IdP's JWKS (RS256 / cached with a freshness TTL — `internal/auth/oidc/jwks.go`); the Issuer claim is pinned against the configured authority.
- **CSRF** — double-submit `X-CSRF-Token` header or `_csrf` form field; validated against the `openccu_loom_csrf` cookie on every mutating request.

## Audit Checklist (every release)

- [ ] `golangci-lint run` clean (`gosec` included)
- [ ] `go vet ./...` clean
- [ ] `go test -race ./...` green
- [ ] `make bench` regressions <20% vs last release
- [ ] `goreleaser release --snapshot --clean` succeeds
- [ ] `docker build` + `docker run … version` succeeds
- [ ] OpenAPI spec kin-openapi validates (`tests/contract/openapi_schema_test.go`)
- [ ] Integration tests against godevccu green (`make integration`)
- [ ] Contract tests green (`tests/contract/...`)
- [ ] No GPL / AGPL / LGPL / MPL license files anywhere in the tree (`git grep -l "SPDX-License-Identifier: \(GPL\|AGPL\|LGPL\|MPL\)"`)
- [ ] No CGo imports (`go list -deps -f '{{.CgoFiles}}' ./...`)

## Known 0.1.0 limitations

- mTLS on MQTT — the `NorthMQTT.TLSConfig` slot already accepts a
  `*tls.Config` so an operator can wire client certificates from
  external code, but the YAML config does not expose
  cert-/key-file paths yet. Server-side TLS (broker URL `tls://`)
  works.
- Rate-limiting on REST — not implemented. The expected production
  position is a reverse proxy (Traefik / Caddy / nginx) in front of
  the daemon for rate-limiting + WAF responsibilities.
