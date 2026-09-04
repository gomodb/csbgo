package csbgo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultUserAgent = "csbBroker"

// Client invokes CSB-published HTTP services. It is safe for concurrent use
// once created: Do does not mutate the client. Create one Client per broker
// (or per credential set) and reuse it for many calls.
type Client struct {
	logger Logger

	httpClient *http.Client

	statusCheck func(int) bool
	baseURL     string
	ak          string
	sk          string
	api         string
	version     string

	userAgent string
	retries   int

	debug bool
}

// Option customizes a Client. Options are applied in order by New.
type Option func(*Client)

// WithHTTPClient sets a custom *http.Client. Use it for advanced transport
// needs (proxy, TLS config, keep-alive tuning, custom RoundTripper). Do not
// call WithTimeout on a client you supplied here in a way that conflicts with
// its own Timeout.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) {
		if c != nil {
			cl.httpClient = c
		}
	}
}

// WithBaseURL sets the CSB endpoint the client calls. It is required: a CSB
// client targets a single endpoint, and every request is sent to this URL's
// host/path. End it with "/" if the path should behave as a directory.
func WithBaseURL(u string) Option {
	return func(cl *Client) { cl.baseURL = u }
}

// WithAK sets the default access key for requests that do not set their own.
func WithAK(ak string) Option { return func(cl *Client) { cl.ak = ak } }

// WithSK sets the default secret key for requests that do not set their own.
func WithSK(sk string) Option { return func(cl *Client) { cl.sk = sk } }

// WithAPI sets a default API name for requests that do not set their own.
func WithAPI(api string) Option { return func(cl *Client) { cl.api = api } }

// WithVersion sets a default API version for requests that do not set their own.
func WithVersion(v string) Option { return func(cl *Client) { cl.version = v } }

// WithTimeout sets the total request timeout on the client's *http.Client.
// Options apply in order, so call it after WithHTTPClient if you want it to
// override a custom client's timeout.
func WithTimeout(d time.Duration) Option {
	return func(cl *Client) { cl.httpClient.Timeout = d }
}

// WithUserAgent sets the User-Agent header value.
func WithUserAgent(ua string) Option { return func(cl *Client) { cl.userAgent = ua } }

// WithDebug enables debug logging via the configured Logger.
func WithDebug(debug bool) Option { return func(cl *Client) { cl.debug = debug } }

// WithLogger sets the debug log sink. Defaults to a mutex-guarded stderr writer.
func WithLogger(l Logger) Option {
	return func(cl *Client) {
		if l != nil {
			cl.logger = l
		}
	}
}

// WithStatusCheck sets how response status codes are classified. It defaults to
// accepting 2xx only. Pass nil to return every response with a nil error, or a
// checker such as AcceptStatus to accept specific codes.
func WithStatusCheck(f func(int) bool) Option {
	return func(cl *Client) { cl.statusCheck = f }
}

// WithTransport sets the http.RoundTripper used by the client's http.Client.
// Use RoundTripFunc to adapt a plain function, or wrap http.DefaultTransport
// for proxy/TLS/logging needs.
func WithTransport(rt http.RoundTripper) Option {
	return func(cl *Client) { cl.httpClient.Transport = rt }
}

// WithRetries sets how many times a call is retried on transport errors and
// 5xx responses. A value <= 0 disables retries (the default).
func WithRetries(n int) Option {
	return func(cl *Client) {
		if n < 0 {
			n = 0
		}

		cl.retries = n
	}
}

// New creates a Client with sensible defaults and applies opts in order.
func New(opts ...Option) *Client {
	c := &Client{
		userAgent: defaultUserAgent,
		logger:    &defaultLogger{},
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		statusCheck: is2xx,
	}

	for _, o := range opts {
		if o != nil {
			o(c)
		}
	}

	return c
}

func (c *Client) debugf(format string, v ...any) {
	if c.debug && c.logger != nil {
		c.logger.Printf(format, v...)
	}
}

// Do executes req against CSB and returns the response.
//
// A non-nil error is returned for local failures (invalid request, URL/marshal
// errors, or an exhausted transport error) and for responses that fail the
// status check. By default a non-2xx status is such a failure and yields a
// *StatusError carrying the response (use errors.As to recover it); disable or
// customize this with WithStatusCheck.
func (c *Client) Do(ctx context.Context, req *Request) (*Response, error) {
	if req == nil {
		return nil, &Error{Message: "request is nil"}
	}

	api := firstNonEmpty(req.api, c.api)
	version := firstNonEmpty(req.version, c.version)
	ak := firstNonEmpty(req.ak, c.ak)
	sk := firstNonEmpty(req.sk, c.sk)
	method := firstNonEmpty(req.method, MethodGet)

	if err := validate(method, api, version, ak, sk); err != nil {
		return nil, err
	}

	if c.baseURL == "" {
		return nil, &Error{Message: "base url is not set: configure the client with WithBaseURL"}
	}

	resp, err := c.send(ctx, req, method, api, version, ak, sk)
	if err != nil {
		return nil, err
	}

	if check := c.checkStatusFor(req); check != nil && !check(resp.StatusCode) {
		return resp, &StatusError{Response: resp}
	}

	return resp, nil
}

// DoJSON performs Do and, on success, decodes the response body as JSON into
// out. The status check still applies: a rejected status is returned as a
// *StatusError and out is left untouched. Deserialization errors surface as an
// *Error wrapping the encoding/json error.
func (c *Client) DoJSON(ctx context.Context, req *Request, out any) (*Response, error) {
	resp, err := c.Do(ctx, req)
	if err != nil {
		return resp, err
	}

	if err := resp.ToJSON(out); err != nil {
		return resp, err
	}

	return resp, nil
}

// send performs the request with retries and returns the final response, or a
// transport error. It never applies the status check.
func (c *Client) send(
	ctx context.Context,
	req *Request,
	method, api, version, ak, sk string,
) (*Response, error) {
	var lastErr error

	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoff(attempt)); err != nil {
				return nil, wrapError(err, "context expired before retry %d", attempt)
			}
		}

		resp, err := c.doOnce(ctx, req, method, api, version, ak, sk)
		if err == nil {
			if resp == nil || resp.StatusCode < 500 {
				return resp, nil
			}
			// 5xx: retry if we have attempts left.
			lastErr = &Error{
				Message:    "broker returned 5xx",
				StatusCode: resp.StatusCode,
			}
			if attempt == c.retries {
				return resp, nil
			}

			continue
		}

		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
	}

	return nil, lastErr
}

// checkStatusFor resolves the status checker for a request: the request's own
// override wins, otherwise the client default is used.
func (c *Client) checkStatusFor(req *Request) func(int) bool {
	if req.statusCheck != nil {
		return req.statusCheck
	}

	return c.statusCheck
}

// doOnce performs a single build+sign+send round trip.
func (c *Client) doOnce(
	ctx context.Context,
	req *Request,
	method, api, version, ak, sk string,
) (*Response, error) {
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, wrapError(err, "parse base url")
	}

	params, finalURL := c.computeParamsAndURL(parsed, req, method)

	sigHeaders := buildSignedHeaders(params, api, version, ak, sk, time.Now())
	c.debugf("signature params=%v headers=%v", params, sigHeaders)

	body, contentType, err := methodBody(req, method)
	if err != nil {
		return nil, err
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, finalURL, reader)
	if err != nil {
		return nil, wrapError(err, "build http request")
	}

	httpReq.Header.Set("User-Agent", c.userAgent)

	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}

	for k, v := range req.header {
		httpReq.Header.Set(k, v)
	}

	for k, v := range sigHeaders {
		httpReq.Header.Set(k, v)
	}

	c.debugf("%s %s", method, finalURL)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, wrapError(err, "send request")
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapError(err, "read response body")
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       respBody,
	}, nil
}

// computeParamsAndURL merges the URL's own query string, the request's Query
// and Form maps into a single signature parameter set, and derives the final
// request URL. It returns both so signing and transport stay consistent.
func (c *Client) computeParamsAndURL(
	parsed *url.URL,
	req *Request,
	method string,
) (map[string]string, string) {
	params := make(map[string]string)
	maps.Copy(params, parseRawQuery(parsed.RawQuery))

	maps.Copy(params, req.query)

	maps.Copy(params, req.form)

	// Apply host/path overrides, mirroring how requests composes URLs.
	if req.host != "" {
		parsed.Host = req.host
	}

	for _, p := range req.paths {
		parsed.Path = parsed.ResolveReference(&url.URL{Path: p}).Path
	}

	// Build the final query string: keep the URL's original raw query, then
	// append the request's encoded query params (and, for GET, form params).
	extra := url.Values{}
	for k, v := range req.query {
		extra.Set(k, v)
	}

	if method == MethodGet {
		for k, v := range req.form {
			extra.Set(k, v)
		}
	}

	q := parsed.RawQuery
	if enc := extra.Encode(); enc != "" {
		if q == "" {
			q = enc
		} else {
			q += "&" + enc
		}
	}

	parsed.RawQuery = q
	parsed.Fragment = ""

	return params, parsed.String()
}

// methodBody returns the materialized body (and content type) for the method.
// GET never carries a body; POST defaults to form-encoding when no explicit
// body was supplied.
func methodBody(req *Request, method string) ([]byte, string, error) {
	if method != MethodPost {
		return nil, "", nil
	}

	if req.hasJSON || req.body != nil {
		body, ct, err := req.resolveBody()
		if err != nil {
			return nil, "", err
		}

		if body == nil {
			return nil, "", nil
		}

		return body, ct, nil
	}

	if len(req.form) > 0 {
		v := url.Values{}
		for k, val := range req.form {
			v.Set(k, val)
		}

		return []byte(v.Encode()), ContentTypeForm, nil
	}

	return nil, "", nil
}

func validate(method, api, version, ak, sk string) *Error {
	if method != MethodGet && method != MethodPost {
		return &Error{Message: "unsupported method " + method + ": only GET and POST are supported"}
	}

	if api == "" || version == "" {
		return &Error{Message: "api name and version are required"}
	}

	if ak != "" && sk == "" {
		return &Error{Message: "access key and secret key must be set together"}
	}

	return nil
}

// parseRawQuery splits a raw query string into a map without decoding values,
// matching how the CSB reference client feeds query params into the signature.
func parseRawQuery(raw string) map[string]string {
	m := make(map[string]string)
	if raw == "" {
		return m
	}

	for kv := range strings.SplitSeq(raw, "&") {
		if kv == "" {
			continue
		}

		if before, after, ok := strings.Cut(kv, "="); ok {
			m[before] = after
		} else {
			m[kv] = ""
		}
	}

	return m
}

func isRetryable(err error) bool {
	// Do not retry after cancellation or deadline: the caller has already given
	// up, so a retry would only delay the inevitable.
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// backoff returns the delay before the given retry attempt (1-based).
func backoff(attempt int) time.Duration {
	return time.Duration(1<<uint(attempt-1)) * 100 * time.Millisecond
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}
