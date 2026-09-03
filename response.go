package csbgo

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Response holds the result of a CSB invocation.
type Response struct {
	// Header holds the response HTTP headers (nil if none were returned).
	Header http.Header
	// Body is the raw response body.
	Body []byte
	// StatusCode is the HTTP status code returned by the broker.
	StatusCode int
}

// String returns the response body as a string.
func (r *Response) String() string { return string(r.Body) }

// OK reports whether the response carries a 2xx status code.
func (r *Response) OK() bool { return r.StatusCode >= 200 && r.StatusCode < 300 }

// JSON unmarshals the response body into v.
func (r *Response) JSON(v any) error { return json.Unmarshal(r.Body, v) }

// StatusError is returned by Client.Do for responses whose status code is not
// accepted by the client's status check (a 2xx-only check by default). It wraps
// the full *Response so callers can still inspect headers and body (e.g. to
// decode a CSB error payload) via errors.As.
type StatusError struct {
	Response *Response
}

// Error implements the error interface. The body is truncated to keep error
// strings from becoming huge.
func (e *StatusError) Error() string {
	if e.Response == nil {
		return "csb: unexpected response status"
	}

	detail := ""
	if b := truncate(string(e.Response.Body), 256); b != "" {
		detail = ": " + b
	}

	return fmt.Sprintf("csb: unexpected status %d%s", e.Response.StatusCode, detail)
}

// AcceptStatus returns a status checker that accepts exactly the given codes.
// Pass it to WithStatusCheck to replace the default 2xx-only check.
func AcceptStatus(codes ...int) func(int) bool {
	set := make(map[int]struct{}, len(codes))
	for _, c := range codes {
		set[c] = struct{}{}
	}

	return func(code int) bool {
		_, ok := set[code]
		return ok
	}
}

// is2xx is the default status check.
func is2xx(code int) bool { return code >= 200 && code < 300 }

func truncate(s string, n int) string {
	if n < 0 {
		n = 0
	}

	if len(s) <= n {
		return s
	}

	return s[:n] + "…"
}
