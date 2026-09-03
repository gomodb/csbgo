package csbgo

import "net/http"

// RoundTripFunc adapts a plain function into an http.RoundTripper. It is handy
// for tests (returning canned responses) and for lightweight middleware:
//
//	c := csbgo.New(
//		csbgo.WithAK("ak"), csbgo.WithSK("sk"),
//		csbgo.WithTransport(csbgo.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
//			// inspect or rewrite req, then delegate
//			return http.DefaultTransport.RoundTrip(req)
//		})),
//	)
type RoundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper.
func (f RoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

var _ http.RoundTripper = RoundTripFunc(nil)
