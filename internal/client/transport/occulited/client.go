// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/httpx"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// Timings of the HTTP clients.
const (
	// DefaultTimeout bounds an ordinary call when the caller's context
	// has no earlier deadline. It sits above the lite-rpc proxy's own
	// 30 s forward timeout, so a slow interface process surfaces as the
	// box's 503 down answer rather than as a client-side timeout.
	DefaultTimeout = 40 * time.Second
	// streamHeaderTimeout bounds connect, TLS and the response header of
	// a stream request; the body itself is unbounded.
	streamHeaderTimeout = 15 * time.Second
	// errorBodyLimit caps how much of an error answer is read.
	errorBodyLimit = 64 << 10
	// answerBodyLimit caps a success answer. Every JSON answer is
	// buffered whole before decoding, so without a cap a misbehaving or
	// compromised box could make the daemon allocate without bound.
	answerBodyLimit = 16 << 20
)

// Config configures a [Client].
type Config struct {
	// BaseURL is the box's root, e.g. "https://openccu-lite.local".
	BaseURL string
	// Token is the API token ("olt_…") sent as a bearer credential. An
	// empty token sends no credential (open endpoints, detection).
	Token string
	// TokenSource yields the bearer credential per request and wins over
	// Token when set. A file-backed source (see [FileToken]) follows
	// occulited's rotation — the box mints an addon's token anew at every
	// occulited start — without any reconnect or retry logic: the next
	// request simply carries the fresh credential.
	TokenSource TokenSource
	// TLSFingerprint pins the server certificate: the hex SHA-256 of the
	// leaf certificate's DER, colons and case ignored. When set, the
	// certificate chain is not verified against a CA; only the pin is.
	TLSFingerprint string
	// InsecureSkipVerify accepts any certificate. Ignored when a
	// fingerprint is set (the pin is the stronger check).
	InsecureSkipVerify bool
	// Logger receives debug logs of the stream readers. Nil means
	// [slog.Default].
	Logger *slog.Logger
	// Timeout overrides [DefaultTimeout] for ordinary calls.
	Timeout time.Duration
}

// Client talks to one openccu-lite box. Safe for concurrent use.
type Client struct {
	base   *url.URL
	cfg    Config
	logger *slog.Logger
	calls  *http.Client
	stream *http.Client
}

// ErrFingerprintMismatch reports that the server presented a certificate
// other than the pinned one. [*FingerprintError] carries both values.
var ErrFingerprintMismatch = errors.New("occulited: TLS certificate does not match the pinned fingerprint")

// FingerprintError is the TLS pin failure; Got is the fingerprint the
// server presented, so an operator can compare it with the box.
type FingerprintError struct {
	Got  string
	Want string
}

// Error implements error.
func (e *FingerprintError) Error() string {
	return fmt.Sprintf("%s: got %s, want %s", ErrFingerprintMismatch, e.Got, e.Want)
}

// Is makes a FingerprintError match [ErrFingerprintMismatch].
func (e *FingerprintError) Is(target error) bool { return target == ErrFingerprintMismatch }

// New validates cfg and builds a Client.
func New(cfg Config) (*Client, error) {
	base, err := parseBase(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	tlsCfg, err := tlsConfig(cfg)
	if err != nil {
		return nil, err
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	callT := httpx.NewTransport()
	callT.TLSClientConfig = tlsCfg
	streamT := httpx.NewTransport()
	streamT.TLSClientConfig = tlsCfg.Clone()
	streamT.ResponseHeaderTimeout = streamHeaderTimeout
	source := cfg.TokenSource
	if source == nil {
		source = StaticToken(cfg.Token)
	}
	return &Client{
		base:   base,
		cfg:    cfg,
		logger: logger,
		calls:  &http.Client{Timeout: timeout, Transport: NewTransport(callT, source)},
		stream: &http.Client{Transport: NewTransport(streamT, source)},
	}, nil
}

// parseBase accepts an http(s) URL and drops a trailing slash, so paths
// can be appended verbatim.
func parseBase(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("occulited: Config.BaseURL is required")
	}
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil {
		return nil, fmt.Errorf("occulited: base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("occulited: base URL %q needs an http or https scheme and a host", raw)
	}
	u.RawQuery, u.Fragment = "", ""
	return u, nil
}

// tlsConfig builds the TLS settings: a fingerprint pin replaces chain
// verification with a comparison against the leaf certificate.
func tlsConfig(cfg Config) (*tls.Config, error) {
	c := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLSFingerprint == "" {
		c.InsecureSkipVerify = cfg.InsecureSkipVerify //nolint:gosec // explicit operator opt-in for a self-signed box
		return c, nil
	}
	pin, err := ParseFingerprint(cfg.TLSFingerprint)
	if err != nil {
		return nil, err
	}
	want := hex.EncodeToString(pin)
	c.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return &FingerprintError{Got: "", Want: want}
		}
		sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
		if subtle.ConstantTimeCompare(sum[:], pin) != 1 {
			return &FingerprintError{Got: hex.EncodeToString(sum[:]), Want: want}
		}
		return nil
	}
	// Chain verification is replaced, not skipped: the stdlib's chain check
	// is switched off only because VerifyConnection above is installed, and
	// that accepts exactly the pinned certificate.
	c.InsecureSkipVerify = c.VerifyConnection != nil //nolint:gosec // the pin in VerifyConnection is the verification
	return c, nil
}

// ParseFingerprint decodes a SHA-256 fingerprint written as hex, with or
// without colons or spaces, in either case.
func ParseFingerprint(s string) ([]byte, error) {
	clean := strings.NewReplacer(":", "", " ", "", "-", "").Replace(strings.TrimSpace(s))
	b, err := hex.DecodeString(strings.ToLower(clean))
	if err != nil || len(b) != sha256.Size {
		return nil, fmt.Errorf("occulited: TLS fingerprint %q is not a hex SHA-256", s)
	}
	return b, nil
}

// Fingerprint returns the pin value of a certificate: the lowercase hex
// SHA-256 of its DER.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// FingerprintOf extracts the fingerprint of the certificate a
// server presented from a failed TLS handshake (an unverifiable chain or
// a pin mismatch), so a first contact can show the operator what to pin.
func FingerprintOf(err error) (string, bool) {
	var fe *FingerprintError
	if errors.As(err, &fe) && fe.Got != "" {
		return fe.Got, true
	}
	var ve *tls.CertificateVerificationError
	if errors.As(err, &ve) && len(ve.UnverifiedCertificates) > 0 {
		return Fingerprint(ve.UnverifiedCertificates[0]), true
	}
	return "", false
}

// BaseURL returns the normalised base URL.
func (c *Client) BaseURL() string { return c.base.String() }

// HTTPClient returns the client for ordinary calls, carrying the bearer
// credential, the body rule, the error classification and the TLS pin.
// The XML-RPC client for the lite-rpc proxy uses it as its HTTP client.
func (c *Client) HTTPClient() *http.Client { return c.calls }

// XMLRPCURL returns the lite-rpc proxy endpoint of an interface. iface is
// the InterfacesList name, matched case-sensitively by the box.
func (c *Client) XMLRPCURL(iface string) string {
	return c.base.String() + "/api/rpc/v1/xmlrpc/" + url.PathEscape(iface)
}

// endpoint joins the base URL with an already escaped path and a query.
func (c *Client) endpoint(escapedPath string, q url.Values) string {
	s := c.base.String() + escapedPath
	if len(q) > 0 {
		s += "?" + q.Encode()
	}
	return s
}

// ----------------------------------------------------------------------
// Transport
// ----------------------------------------------------------------------

// NewTransport wraps next with the box's request and answer rules:
//
//   - the bearer credential when token is set and the request carries no
//     Authorization header of its own;
//   - a body with a Content-Length on every POST, PUT, PATCH and DELETE:
//     "{}" when there is nothing to send, a buffered copy when the
//     length is unknown (the web server in front of occulited answers
//     411 to a body without a length);
//   - an answer the box uses to refuse a request becomes an [*APIError]
//     returned as the round-trip error: 503 with the JSON error "down"
//     or "starting" (matches [hmerr.ErrNoConnection]), 401 (matches
//     [hmerr.ErrAuthFailure]) and 403 naming a scope (unwraps to
//     [*hmerr.ScopeMissingError]). Other answers pass through unchanged.
//
// Returning the refusal from RoundTrip is what makes a caller that only
// sees transport errors, the XML-RPC client above all, classify a
// stopped interface process as a connection failure.
func NewTransport(next http.RoundTripper, source TokenSource) http.RoundTripper {
	return &roundTripper{next: next, source: source}
}

// TokenSource yields the current API token; "" sends no credential.
type TokenSource func() (string, error)

// StaticToken returns a TokenSource that always yields token.
func StaticToken(token string) TokenSource {
	return func() (string, error) { return token, nil }
}

// fileTokenShape is the exact shape occulited mints (olt_ followed by
// 32 lower-case hex digits). Enforcing it on the file's content keeps a
// misdirected path from ever leaking arbitrary file contents as a
// bearer header: only a genuine occulited token leaves the process.
var fileTokenShape = regexp.MustCompile(`^olt_[0-9a-f]{32}$`)

// FileToken returns a TokenSource that reads path at every call,
// trimming surrounding whitespace (occulited writes the secret with a
// trailing newline). Reading per request is what makes rotation safe:
// the file lives on a tmpfs and the box replaces it whenever occulited
// mints anew. The path must be absolute and the content must be an
// occulited API token; anything else fails the request naming the file
// — never quoting its content.
func FileToken(path string) TokenSource {
	clean := filepath.Clean(path)
	return func() (string, error) {
		if !filepath.IsAbs(clean) {
			return "", fmt.Errorf("occulited: token file %s: an absolute path is required", clean)
		}
		raw, err := os.ReadFile(clean) //nolint:gosec // an admin-configured absolute path; the content is shape-checked below
		if err != nil {
			return "", fmt.Errorf("occulited: token file %s: %w", clean, err)
		}
		token := strings.TrimSpace(string(raw))
		if !fileTokenShape.MatchString(token) {
			return "", fmt.Errorf("occulited: token file %s does not hold an occulited API token (olt_ followed by 32 lower-case hex digits)", clean)
		}
		return token, nil
	}
}

type roundTripper struct {
	next   http.RoundTripper
	source TokenSource
}

type noAuthKey struct{}

// withoutCredential marks a request context so the transport does not
// attach the token; used for probes that may reach a system that is not
// the box (a CCU during detection).
func withoutCredential(ctx context.Context) context.Context {
	return context.WithValue(ctx, noAuthKey{}, true)
}

// RoundTrip implements http.RoundTripper.
func (t *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	skip, _ := out.Context().Value(noAuthKey{}).(bool)
	if !skip && out.Header.Get("Authorization") == "" {
		token, err := t.source()
		if err != nil {
			return nil, err
		}
		if token != "" {
			out.Header.Set("Authorization", "Bearer "+token)
		}
	}
	if err := ensureBody(out); err != nil {
		return nil, err
	}
	resp, err := t.next.RoundTrip(out)
	if err != nil {
		return nil, err
	}
	if apiErr := refusal(resp, req); apiErr != nil {
		return nil, apiErr
	}
	return resp, nil
}

// methodCarriesBody reports whether the web server wants a length.
func methodCarriesBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// ensureBody applies the Content-Length rule to a cloned request.
func ensureBody(req *http.Request) error {
	if !methodCarriesBody(req.Method) {
		return nil
	}
	if req.Body == nil || req.Body == http.NoBody {
		setBody(req, []byte("{}"))
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		return nil
	}
	if req.ContentLength > 0 {
		return nil
	}
	raw, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return fmt.Errorf("occulited: buffer request body: %w", err)
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	setBody(req, raw)
	return nil
}

func setBody(req *http.Request, raw []byte) {
	req.Body = io.NopCloser(bytes.NewReader(raw))
	req.ContentLength = int64(len(raw))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil }
	req.Header.Set("Content-Length", strconv.Itoa(len(raw)))
}

// refusal turns a refusing answer into an APIError (closing the body),
// or returns nil and leaves resp intact.
func refusal(resp *http.Response, req *http.Request) *APIError {
	switch resp.StatusCode {
	case http.StatusServiceUnavailable, http.StatusUnauthorized, http.StatusForbidden:
	default:
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
	rest := resp.Body
	if err != nil {
		raw = nil
	}
	apiErr := parseAPIError(resp, req, raw)
	take := false
	switch resp.StatusCode {
	case http.StatusServiceUnavailable:
		take = apiErr.Code == CodeDown || apiErr.Code == CodeStarting
	case http.StatusUnauthorized:
		take = true
	case http.StatusForbidden:
		take = apiErr.Scope != ""
	}
	if take {
		_ = rest.Close()
		return apiErr
	}
	resp.Body = readCloser{Reader: io.MultiReader(bytes.NewReader(raw), rest), Closer: rest}
	return nil
}

type readCloser struct {
	io.Reader
	io.Closer
}

// ----------------------------------------------------------------------
// Errors
// ----------------------------------------------------------------------

// Error codes of the box's JSON error answers that the client branches
// on.
const (
	CodeDown            = "down"
	CodeStarting        = "starting"
	CodeUnauthenticated = "unauthenticated"
	CodeForbidden       = "forbidden"
	CodeTooManyStreams  = "too-many-streams"
	// CodeRevisionConflict answers a metadata write whose If-Match
	// revision the store has moved past (409).
	CodeRevisionConflict = "revision-conflict"
	// CodeUnknownObject answers a metadata read or write of an object the
	// store does not hold (404); the store holds only named objects.
	CodeUnknownObject = "unknown-object"
	// CodeUnknownGroup answers a heating-group read or write of a group
	// the box does not hold (404).
	CodeUnknownGroup = "unknown-group"
)

// ErrTooManyStreams matches the event stream's 429 too-many-streams
// answer.
var ErrTooManyStreams = errors.New("occulited: too many event streams for this token")

// ErrRevisionConflict matches the metadata store's 409 revision-conflict:
// the write was based on a revision the store has moved past.
var ErrRevisionConflict = errors.New("occulited: metadata revision conflict")

// ErrUnknownObject matches the metadata store's 404 unknown-object.
var ErrUnknownObject = errors.New("occulited: unknown metadata object")

// ErrUnknownGroup matches the system API's 404 unknown-group.
var ErrUnknownGroup = errors.New("occulited: unknown heating group")

// ErrProtocol reports an answer that breaks the wire contract the client
// relies on (a stream frame, a missing ETag, an undecodable body).
var ErrProtocol = errors.New("occulited: protocol violation")

// APIError is a non-success answer of the box: {"error", "message",
// "detail"?, "scope"?}.
type APIError struct {
	Status  int
	Code    string
	Message string
	// Scope is the scope a 403 names.
	Scope string
	// Detail is the error's detail member, raw.
	Detail json.RawMessage
	// RetryAfter is the Retry-After header, zero when absent.
	RetryAfter time.Duration
	Method     string
	Path       string
}

// Error implements error.
func (e *APIError) Error() string {
	msg := fmt.Sprintf("occulited: %s %s: http %d", e.Method, e.Path, e.Status)
	if e.Code != "" {
		msg += " " + e.Code
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// Is classifies the answer against the hmerr sentinels and the
// package's own.
func (e *APIError) Is(target error) bool {
	switch target {
	case hmerr.ErrNoConnection:
		return e.Status == http.StatusServiceUnavailable && (e.Code == CodeDown || e.Code == CodeStarting)
	case hmerr.ErrAuthFailure:
		return e.Status == http.StatusUnauthorized
	case ErrTooManyStreams:
		return e.Status == http.StatusTooManyRequests && e.Code == CodeTooManyStreams
	case ErrRevisionConflict:
		return e.Status == http.StatusConflict && e.Code == CodeRevisionConflict
	case ErrUnknownObject:
		return e.Status == http.StatusNotFound && e.Code == CodeUnknownObject
	case ErrUnknownGroup:
		return e.Status == http.StatusNotFound && e.Code == CodeUnknownGroup
	default:
		return false
	}
}

// Unwrap exposes the missing scope of a 403 as [*hmerr.ScopeMissingError].
func (e *APIError) Unwrap() error {
	if e.Status == http.StatusForbidden && e.Scope != "" {
		return &hmerr.ScopeMissingError{Scope: e.Scope, Operation: e.Method + " " + e.Path}
	}
	return nil
}

type errorBody struct {
	Error   string          `json:"error"`
	Message string          `json:"message"`
	Scope   string          `json:"scope"`
	Detail  json.RawMessage `json:"detail"`
}

// parseAPIError reads the JSON error shape; a body that is not JSON
// leaves Code empty and keeps the text as the message.
func parseAPIError(resp *http.Response, req *http.Request, raw []byte) *APIError {
	e := &APIError{Status: resp.StatusCode, RetryAfter: retryAfter(resp.Header)}
	if req != nil {
		e.Method, e.Path = req.Method, req.URL.Path
	}
	var body errorBody
	if json.Unmarshal(raw, &body) == nil {
		e.Code, e.Message, e.Scope, e.Detail = body.Error, body.Message, body.Scope, body.Detail
	} else {
		e.Message = strings.TrimSpace(string(raw))
		if len(e.Message) > 200 {
			e.Message = e.Message[:200]
		}
	}
	return e
}

// retryAfter reads a Retry-After header given in seconds.
func retryAfter(h http.Header) time.Duration {
	s := strings.TrimSpace(h.Get("Retry-After"))
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// ----------------------------------------------------------------------
// JSON calls
// ----------------------------------------------------------------------

// request is one JSON call.
type request struct {
	method string
	path   string // escaped
	query  url.Values
	body   []byte
	header http.Header
	// allow304 accepts a 304 answer as a success.
	allow304 bool
}

// send performs r and returns the answer for a 2xx (or an allowed 304);
// every other status becomes an *APIError. The caller closes the body.
func (c *Client) send(ctx context.Context, hc *http.Client, r request) (*http.Response, error) {
	var body io.Reader
	if r.body != nil {
		body = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, c.endpoint(r.path, r.query), body)
	if err != nil {
		return nil, fmt.Errorf("occulited: build request: %w", err)
	}
	for k, vs := range r.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if r.body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		if apiErr, ok := errors.AsType[*APIError](err); ok {
			return nil, apiErr
		}
		return nil, fmt.Errorf("occulited: %s %s: %w: %w", r.method, req.URL.Path, hmerr.ErrNoConnection, err)
	}
	if resp.StatusCode/100 == 2 || r.allow304 && resp.StatusCode == http.StatusNotModified {
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
	return nil, parseAPIError(resp, req, raw)
}

// call performs a JSON call and decodes the answer into T. T is the
// caller's answer type; the constraint is json.Unmarshal's own.
func call[T any](ctx context.Context, c *Client, r request) (T, error) {
	var zero T
	resp, err := c.send(ctx, c.calls, r)
	if err != nil {
		return zero, err
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeBody[T](resp, r)
}

// decodeBody decodes a JSON answer body into T (see [call] for T).
func decodeBody[T any](resp *http.Response, r request) (T, error) {
	var out T
	// One byte past the cap tells a truncated answer from one that ends
	// exactly at it; decoding a truncated document would only surface as
	// a confusing syntax error.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, answerBodyLimit+1))
	if err != nil {
		return out, fmt.Errorf("occulited: %s %s: read answer: %w: %w", r.method, r.path, hmerr.ErrNoConnection, err)
	}
	if len(raw) > answerBodyLimit {
		return out, fmt.Errorf("occulited: %s %s: %w: answer exceeds the %d-byte limit", r.method, r.path, ErrProtocol, answerBodyLimit)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("occulited: %s %s: %w: decode answer: %w", r.method, r.path, ErrProtocol, err)
	}
	return out, nil
}

// get is call for a GET without a body.
func get[T any](ctx context.Context, c *Client, path string, q url.Values) (T, error) { // T: see call.
	return call[T](ctx, c, request{method: http.MethodGet, path: path, query: q})
}

// marshal encodes a request body. T is the caller's request type.
func marshal[T any](v T) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("occulited: encode request: %w", err)
	}
	return raw, nil
}

// withRaw decodes raw into T and hands back both; used by answers that
// keep their whole document for untyped members (see the package doc).
func withRaw[T any](raw json.RawMessage) (T, error) { // T: see call.
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("occulited: %w: decode answer: %w", ErrProtocol, err)
	}
	return out, nil
}
