package csbgo

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// oracleSign is an independent reimplementation of the CSB signature protocol,
// written from the spec (sort keys bytewise, join "k=v" with "&", then
// base64(HMAC-SHA1(secret, canonical-string))). It deliberately does NOT call
// the package's canonicalize/sign, so a wiring bug in the SDK would surface as
// a mismatch at the HTTP boundary rather than being hidden by a shared helper.
func oracleSign(secret string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var b strings.Builder

	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}

		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(params[k])
	}

	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(b.String()))

	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// rawSplit parses a raw "k=v&k2=v2" string into a map without decoding values,
// mirroring how CSB feeds parameters into the signature.
func rawSplit(raw string) map[string]string {
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

// TestEndToEndSignatureMatchesWireParams is the core "does the real business
// logic work" test. The server side reconstructs the signed parameter set from
// what actually arrived on the wire — URL-embedded query, Query, Form (body for
// POST, query for GET) and the CSB protocol headers — then recomputes the
// expected signature with an independent implementation and compares it to the
// received _api_signature.
func TestEndToEndSignatureMatchesWireParams(t *testing.T) {
	cases := []struct {
		name   string
		method string
	}{
		{"POST-form", MethodPost},
		{"GET-query", MethodGet},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			type result struct{ got, want string }

			results := make(chan result, 1)

			srv := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					params := rawSplit(r.URL.RawQuery)
					if r.Method == http.MethodPost {
						body, err := io.ReadAll(r.Body)
						if err == nil {
							maps.Copy(params, rawSplit(string(body)))
						}
					}

					// Rebuild the signed set from the protocol headers.
					params[keyAPIName] = r.Header.Get(keyAPIName)
					params[keyAPIVersion] = r.Header.Get(keyAPIVersion)

					params[keyAPITimestamp] = r.Header.Get(keyAPITimestamp)
					if ak := r.Header.Get(keyAPIAccessKey); ak != "" {
						params[keyAPIAccessKey] = ak
					}

					delete(params, keyAPISecretKey)
					delete(params, keyAPISignature)

					results <- result{
						got:  r.Header.Get(keyAPISignature),
						want: oracleSign("sk", params),
					}

					w.WriteHeader(http.StatusOK)
				}),
			)
			defer srv.Close()

			// "base" lives in the client's base URL to exercise the URL-embedded query
			// merge that must also be signed.
			client := New(WithAK("ak"), WithSK("sk"), WithBaseURL(srv.URL+"/CSB?base=x"))
			req := NewRequest(tc.method).
				WithAPI("PING").
				WithVersion("vcsb").
				WithQuery("q1", "dog").
				WithForm("f1", "cat")

			if _, err := client.Do(context.Background(), req); err != nil {
				t.Fatalf("Do() error = %v", err)
			}

			r := <-results
			if r.got == "" {
				t.Fatal("signature header was not sent")
			}

			if r.got != r.want {
				t.Errorf("signature mismatch:\n got  %q\n want %q", r.got, r.want)
			}
		})
	}
}

// TestSecretAndSignatureExcludedFromSignature guards the "what must NOT be
// signed" rule: even if a caller passes _api_secret_key or _api_signature as a
// query parameter, the SDK must strip them from the signed set (the real
// signature is still computed over the remaining parameters). The server side
// reconstructs the set while holding these two keys out and expects the
// received _api_signature to match, proving the strip happened on the wire.
func TestSecretAndSignatureExcludedFromSignature(t *testing.T) {
	type result struct{ got, want string }

	results := make(chan result, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params := rawSplit(r.URL.RawQuery)

		// The SDK must have dropped these before signing.
		delete(params, keyAPISecretKey)
		delete(params, keyAPISignature)

		params[keyAPIName] = r.Header.Get(keyAPIName)
		params[keyAPIVersion] = r.Header.Get(keyAPIVersion)

		params[keyAPITimestamp] = r.Header.Get(keyAPITimestamp)
		if ak := r.Header.Get(keyAPIAccessKey); ak != "" {
			params[keyAPIAccessKey] = ak
		}

		results <- result{
			got:  r.Header.Get(keyAPISignature),
			want: oracleSign("sk", params),
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := New(WithAK("ak"), WithSK("sk"), WithBaseURL(srv.URL+"/CSB")).Do(context.Background(),
		NewRequest(MethodGet).
			WithAPI("PING").
			WithVersion("vcsb").
			WithQuery(keyAPISecretKey, "user-secret").
			WithQuery(keyAPISignature, "user-stale-sig").
			WithQuery("q1", "dog"))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	r := <-results
	if r.got == "" {
		t.Fatal("signature header was not sent")
	}

	if r.got != r.want {
		t.Errorf(
			"signature mismatch when secret/signature keys supplied:\n got  %q\n want %q",
			r.got,
			r.want,
		)
	}
}

// TestSignatureUsesRawValuesNotEncoded locks the protocol subtlety that the
// signature is computed over the RAW parameter values (as supplied by the
// caller) while transport encoding happens separately on the wire. The server
// decodes the received URL query and form body and expects the signature to
// match the decoded (== raw) values — if the SDK wrongly signed the encoded
// forms ("a+b" / "x%26y"), this would fail.
func TestSignatureUsesRawValuesNotEncoded(t *testing.T) {
	type result struct{ got, want string }

	results := make(chan result, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()

		params := make(map[string]string)

		for k, vv := range r.URL.Query() {
			if len(vv) > 0 {
				params[k] = vv[0]
			}
		}

		for k, vv := range r.PostForm {
			if len(vv) > 0 {
				params[k] = vv[0]
			}
		}

		params[keyAPIName] = r.Header.Get(keyAPIName)
		params[keyAPIVersion] = r.Header.Get(keyAPIVersion)

		params[keyAPITimestamp] = r.Header.Get(keyAPITimestamp)
		if ak := r.Header.Get(keyAPIAccessKey); ak != "" {
			params[keyAPIAccessKey] = ak
		}

		delete(params, keyAPISecretKey)
		delete(params, keyAPISignature)

		results <- result{
			got:  r.Header.Get(keyAPISignature),
			want: oracleSign("sk", params),
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := New(WithAK("ak"), WithSK("sk"), WithBaseURL(srv.URL+"/CSB")).Do(context.Background(),
		NewRequest(MethodPost).
			WithAPI("PING").
			WithVersion("vcsb").
			WithQuery("q", "a b").
			WithForm("f", "x&y"))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	r := <-results
	if r.got != r.want {
		t.Errorf(
			"signature should be computed over raw (decoded) values:\n got  %q\n want %q",
			r.got,
			r.want,
		)
	}
}
