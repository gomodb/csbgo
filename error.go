// Package csbgo implements a minimal, dependency-free client for invoking
// HTTP services published through Alibaba Cloud CSB (Cloud Service Bus).
package csbgo

import "fmt"

// Error is the error type returned by this package. It carries CSB's own
// error fields (when present), the HTTP status code, and any underlying error
// so callers can introspect failures without string matching.
type Error struct {
	// Err is the underlying error that triggered the failure (may be nil).
	Err error
	// Message is CSB's detailed error message, when the broker returned one.
	Message string
	// RequestID correlates this call with the CSB/console log, when present.
	RequestID string
	// StatusCode is the HTTP status code; it is 0 when no response was received.
	StatusCode int
}

// Error implements the error interface.
func (e *Error) Error() string {
	msg := "csb: "
	if e.Message != "" {
		msg += e.Message
	} else {
		msg += "request failed"
	}

	if e.RequestID != "" {
		msg += " (request_id=" + e.RequestID + ")"
	}

	if e.StatusCode > 0 {
		msg += " (status=" + fmt.Sprint(e.StatusCode) + ")"
	}

	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}

	return msg
}

// Unwrap returns the underlying error so errors.Is/errors.As work as expected.
func (e *Error) Unwrap() error { return e.Err }

// wrapError builds an *Error from an underlying error and optional context.
func wrapError(err error, format string, a ...any) *Error {
	return &Error{Message: fmt.Sprintf(format, a...), Err: err}
}
