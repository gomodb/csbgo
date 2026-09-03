package csbgo

import (
	"testing"
	"time"
)

func TestCanonicalize(t *testing.T) {
	params := map[string]string{
		"a":               "1",
		"b":               "2",
		"_api_name":       "PING",
		"_api_version":    "vcsb",
		"_api_timestamp":  "1234567890123",
		"_api_access_key": "ak",
	}

	want := "_api_access_key=ak&_api_name=PING&_api_timestamp=1234567890123&_api_version=vcsb&a=1&b=2"
	if got := canonicalize(params); got != want {
		t.Fatalf("canonicalize() = %q, want %q", got, want)
	}
}

func TestSignKnownAnswer(t *testing.T) {
	// Ground truth computed independently (Python hmac/hashlib/base64).
	params := map[string]string{
		"a":               "1",
		"b":               "2",
		"_api_name":       "PING",
		"_api_version":    "vcsb",
		"_api_timestamp":  "1234567890123",
		"_api_access_key": "ak",
	}
	if got, want := sign(params, "sk"), "FJZJ2EhieLec7iJQXe8XzuE9kL4="; got != want {
		t.Fatalf("sign() = %q, want %q", got, want)
	}
}

func TestBuildSignedHeadersDoesNotMutateInput(t *testing.T) {
	params := map[string]string{"a": "1"}
	before := params["a"]
	buildSignedHeaders(params, "PING", "vcsb", "ak", "sk", time.UnixMilli(1234567890123))

	if params["a"] != before || len(params) != 1 {
		t.Fatalf("input params were mutated: %v", params)
	}
}

func TestBuildSignedHeadersContent(t *testing.T) {
	now := time.UnixMilli(1234567890123)
	h := buildSignedHeaders(map[string]string{"p1": "dog"}, "PING", "vcsb", "ak", "sk", now)

	tests := map[string]string{
		keyAPIName:      "PING",
		keyAPIVersion:   "vcsb",
		keyAPITimestamp: "1234567890123",
		keyAPIAccessKey: "ak",
		keyAPISignature: "QXP3ZUR1eqaEVDN8VgSd6F29U9k=",
	}
	for k, want := range tests {
		if h[k] != want {
			t.Errorf("header %s = %q, want %q", k, h[k], want)
		}
	}

	if _, ok := h[keyAPISecretKey]; ok {
		t.Errorf("%s must not be present in signed headers", keyAPISecretKey)
	}
}
