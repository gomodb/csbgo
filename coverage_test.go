package csbgo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// --- error.go ----------------------------------------------------------------

func TestErrorStringAndUnwrap(t *testing.T) {
	e := &Error{Message: "boom", RequestID: "rid-1", StatusCode: 500, Err: io.EOF}
	if !errors.Is(e, io.EOF) {
		t.Error("Error.Unwrap did not expose the cause")
	}

	s := e.Error()
	for _, want := range []string{"boom", "rid-1", "500", "EOF"} {
		if !strings.Contains(s, want) {
			t.Errorf("Error() = %q missing %q", s, want)
		}
	}

	if got := (&Error{}).Error(); !strings.Contains(got, "request failed") {
		t.Errorf("empty Error() = %q", got)
	}
}

// --- logger.go ---------------------------------------------------------------

func TestDefaultLoggerPrintf(t *testing.T) {
	// Writes to stderr; just ensure it does not panic.
	(&defaultLogger{}).Printf("hello %d", 1)
}

// --- request/options ---------------------------------------------------------

func TestTrivialBuildersAndOptions(t *testing.T) {
	c := New(
		WithHTTPClient(&http.Client{}),
		WithBaseURL("http://host/"),
		WithAK("ak"), WithSK("sk"),
		WithAPI("api"), WithVersion("v1"),
		WithTimeout(time.Second),
		WithUserAgent("ua"),
		WithDebug(true),
		WithLogger(&defaultLogger{}),
		WithRetries(1),
	)
	if c.baseURL != "http://host/" {
		t.Errorf("baseURL = %q", c.baseURL)
	}

	if c.ak != "ak" || c.sk != "sk" || c.api != "api" || c.version != "v1" {
		t.Error("credential defaults not applied")
	}

	r := NewRequest(MethodPost).
		WithMethod("post").
		WithAK("a").WithSK("s").
		WithQueries(map[string]string{"a": "1"}).
		WithForms(map[string]string{"f": "2"}).
		WithHeaders(map[string]string{"H": "V"}).
		WithBody("text/plain", []byte("b")).
		Pathf("v%d", 1)

	if r.method != "POST" || r.ak != "a" || r.sk != "s" {
		t.Error("request chaining broken")
	}

	if r.query["a"] != "1" || r.form["f"] != "2" || r.header["H"] != "V" {
		t.Errorf("bulk setters broken: %v %v %v", r.query, r.form, r.header)
	}

	if len(r.paths) != 1 || r.paths[0] != "v1" {
		t.Errorf("Pathf = %v", r.paths)
	}

	resp := &Response{Body: []byte(`{"k":1}`)}

	var m map[string]int
	if err := resp.JSON(&m); err != nil || m["k"] != 1 {
		t.Errorf("Response.JSON = %v %v", err, m)
	}
}

func TestResponseJSONError(t *testing.T) {
	var v map[string]int
	if err := (&Response{Body: []byte("not json")}).JSON(&v); err == nil {
		t.Error("expected decode error")
	}
}

// --- canonicalize / truncate / parseRawQuery / cloneMap ----------------------

func TestCanonicalizeEmpty(t *testing.T) {
	if canonicalize(nil) != "" || canonicalize(map[string]string{}) != "" {
		t.Error("canonicalize of empty must be empty")
	}
}

func TestTruncateEdges(t *testing.T) {
	if truncate("abc", 10) != "abc" {
		t.Error("short string changed")
	}

	if got := truncate("abcdef", 3); got != "abc…" {
		t.Errorf("truncate = %q", got)
	}

	if got := truncate("x", -1); got != "…" {
		t.Errorf("negative truncate = %q", got)
	}
}

func TestParseRawQueryEdges(t *testing.T) {
	m := parseRawQuery("a=1&b&c=3&&")
	if m["a"] != "1" || m["b"] != "" || m["c"] != "3" || len(m) != 3 {
		t.Errorf("parseRawQuery = %v", m)
	}

	if len(parseRawQuery("")) != 0 {
		t.Error("empty raw query must be empty map")
	}
}

func TestCloneBodyAndNilMap(t *testing.T) {
	base := NewRequest(MethodPost).WithBody("text/plain", []byte("abc"))
	derived := base.Clone()

	derived.body[0] = 'X'
	if base.body[0] != 'a' {
		t.Error("Clone did not deep-copy the body")
	}

	derived.query["only"] = "here"
	if _, ok := base.query["only"]; ok {
		t.Error("Clone did not deep-copy query")
	}

	if cloneMap(nil) != nil {
		t.Error("cloneMap(nil) != nil")
	}
}

// --- validate / methodBody / resolveBody / baseURL --------------------------

func TestValidateBranches(t *testing.T) {
	if err := validate("DELETE", "a", "v", "", ""); err == nil {
		t.Error("expected unsupported method error")
	}

	if err := validate(MethodGet, "a", "v", "ak", ""); err == nil {
		t.Error("expected ak-without-sk error")
	}

	if err := validate(MethodGet, "a", "v", "ak", "sk"); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestDoRequiresBaseURL(t *testing.T) {
	c := New()

	_, err := c.Do(context.Background(), NewRequest(MethodGet).WithAPI("A").WithVersion("v1"))
	if err == nil || !strings.Contains(err.Error(), "base url is not set") {
		t.Fatalf("expected base-url error, got %v", err)
	}
}

func TestMethodBodyBranches(t *testing.T) {
	if b, _, _ := methodBody(NewRequest(MethodGet).WithForm("f", "1"), MethodGet); b != nil {
		t.Error("GET must not carry a body")
	}

	b, ct, err := methodBody(NewRequest(MethodPost).WithBody("text/plain", []byte("x")), MethodPost)
	if err != nil || string(b) != "x" || ct != "text/plain" {
		t.Errorf("explicit body = %q %q %v", b, ct, err)
	}

	if b, _, _ := methodBody(NewRequest(MethodPost), MethodPost); b != nil {
		t.Error("empty POST must not produce a body")
	}
}

func TestResolveBodyErrors(t *testing.T) {
	r := NewRequest(MethodPost).WithJSON(make(chan int)) // unmarshalable
	if _, _, err := r.resolveBody(); err == nil {
		t.Error("expected marshal error")
	}
}

// --- retries -----------------------------------------------------------------

func TestRetryTransientTransportError(t *testing.T) {
	calls := 0
	rt := RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, io.ErrUnexpectedEOF
		}

		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("ok")),
		}, nil
	})

	c := New(
		WithAK("ak"),
		WithSK("sk"),
		WithTransport(rt),
		WithRetries(1),
		WithBaseURL("http://example.com/CSB"),
	)

	resp, err := c.Do(context.Background(),
		NewRequest(MethodGet).WithAPI("A").WithVersion("v1"))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	if resp.String() != "ok" || calls != 2 {
		t.Errorf("resp=%q calls=%d, want ok/2", resp.String(), calls)
	}
}

func TestRetry5xxThenSuccess(t *testing.T) {
	calls := 0
	rt := RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++

		status := 500
		if calls > 1 {
			status = 200
		}

		return &http.Response{
			StatusCode: status,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})

	c := New(
		WithAK("ak"),
		WithSK("sk"),
		WithTransport(rt),
		WithRetries(1),
		WithBaseURL("http://example.com/CSB"),
	)

	resp, err := c.Do(context.Background(),
		NewRequest(MethodGet).WithAPI("A").WithVersion("v1"))
	if err != nil || resp.StatusCode != 200 || calls != 2 {
		t.Fatalf("resp=%v err=%v calls=%d", resp, err, calls)
	}
}

func TestRetry5xxExhaustedReturnsStatusError(t *testing.T) {
	calls := 0
	rt := RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++

		return &http.Response{
			StatusCode: 503,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("down")),
		}, nil
	})

	c := New(
		WithAK("ak"),
		WithSK("sk"),
		WithTransport(rt),
		WithRetries(1),
		WithBaseURL("http://example.com/CSB"),
	)
	resp, err := c.Do(context.Background(),
		NewRequest(MethodGet).WithAPI("A").WithVersion("v1"))

	var se *StatusError
	if !errors.As(err, &se) || se.Response != resp || calls != 2 {
		t.Fatalf("err=%v calls=%d (want StatusError after 2 tries)", err, calls)
	}
}

func TestNilRequest(t *testing.T) {
	if _, err := New().Do(context.Background(), nil); err == nil {
		t.Error("expected nil-request error")
	}
}

func TestBackoffAndSleepCtx(t *testing.T) {
	if backoff(1) != 100*time.Millisecond || backoff(2) != 200*time.Millisecond {
		t.Error("backoff values wrong")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := sleepCtx(ctx, time.Hour); err == nil {
		t.Error("expected context error")
	}
}

func TestIsRetryable(t *testing.T) {
	if !isRetryable(io.ErrUnexpectedEOF) {
		t.Error("transport error should be retryable")
	}

	if isRetryable(context.Canceled) {
		t.Error("context.Canceled must not be retried")
	}

	if isRetryable(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded must not be retried")
	}
	// Cancellation wrapped in our *Error must still be recognized.
	if isRetryable(&Error{Err: context.Canceled}) {
		t.Error("wrapped context.Canceled must not be retried")
	}
}
