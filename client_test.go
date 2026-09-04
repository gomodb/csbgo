package csbgo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClientSignsAndSends(t *testing.T) {
	var (
		gotURI, gotSig, gotTS, gotAK, gotName, gotVersion, gotCT, gotMethod string
		gotBody                                                             []byte
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotURI = r.RequestURI
		gotSig = r.Header.Get(keyAPISignature)
		gotTS = r.Header.Get(keyAPITimestamp)
		gotAK = r.Header.Get(keyAPIAccessKey)
		gotName = r.Header.Get(keyAPIName)
		gotVersion = r.Header.Get(keyAPIVersion)
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)

		w.Header().Set("x-csb", "ok")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"result":"pong"}`)
	}))
	defer srv.Close()

	c := New(WithAK("ak"), WithSK("sk"), WithBaseURL(srv.URL))
	req := NewRequest(MethodPost).
		WithAPI("PING").
		WithVersion("vcsb").
		Path("CSB").
		WithQuery("name", "wiseking").
		WithForm("p1", "dog").
		WithHeader("h1", "wiseking")

	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	if !resp.OK() {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	if resp.ToString() != `{"result":"pong"}` {
		t.Fatalf("body = %q", resp.ToString())
	}

	if resp.Header.Get("x-csb") != "ok" {
		t.Fatalf("response header lost: %v", resp.Header)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s", gotMethod)
	}

	if !strings.Contains(gotURI, "name=wiseking") {
		t.Errorf("existing query not preserved: %s", gotURI)
	}

	if gotName != "PING" || gotVersion != "vcsb" || gotAK != "ak" {
		t.Errorf("protocol headers: name=%s version=%s ak=%s", gotName, gotVersion, gotAK)
	}

	if gotTS == "" {
		t.Errorf("timestamp header empty")
	}
	// timestamp must be a millisecond epoch integer
	if !allDigits(gotTS) {
		t.Errorf("timestamp not numeric: %q", gotTS)
	}

	if gotSig == "" {
		t.Errorf("signature header empty")
	}

	if gotCT != ContentTypeForm {
		t.Errorf("content-type = %q, want %q", gotCT, ContentTypeForm)
	}

	if string(gotBody) != "p1=dog" {
		t.Errorf("body = %q, want %q", gotBody, "p1=dog")
	}
}

func TestClientGETPutsParamsInQuery(t *testing.T) {
	var gotURI string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI

		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := New(WithAK("ak"), WithSK("sk"), WithBaseURL(srv.URL))
	req := NewRequest(MethodGet).
		WithAPI("PING").
		WithVersion("vcsb").
		Path("CSB").
		WithQuery("q", "a b").
		WithForm("f", "x&y")

	if _, err := c.Do(context.Background(), req); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	// query values must be URL-encoded for transport
	if !strings.Contains(gotURI, "q=a+b") {
		t.Errorf("query not encoded: %s", gotURI)
	}

	if !strings.Contains(gotURI, "f=x%26y") {
		t.Errorf("form param not encoded into GET query: %s", gotURI)
	}
}

func TestClientValidation(t *testing.T) {
	c := New()

	_, err := c.Do(context.Background(), NewRequest(MethodGet))
	if err == nil || !strings.Contains(err.Error(), "api name and version are required") {
		t.Fatalf("expected missing api/version error, got %v", err)
	}
}

func TestClientJSONBody(t *testing.T) {
	var gotCT string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := New(WithBaseURL(srv.URL))
	req := NewRequest(MethodPost).WithAPI("A").WithVersion("v1").
		WithJSON(map[string]string{"k": "v"})

	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	if gotCT != ContentTypeJSON {
		t.Errorf("content-type = %q", gotCT)
	}

	if string(resp.Body) != `{"k":"v"}` {
		t.Errorf("body = %q", resp.Body)
	}
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func TestClientStatusErrorByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = io.WriteString(w, "not found")
	}))
	defer srv.Close()

	c := New(WithBaseURL(srv.URL))

	resp, err := c.Do(context.Background(),
		NewRequest(MethodGet).WithAPI("A").WithVersion("v1"))
	if err == nil {
		t.Fatal("expected a non-2xx status error")
	}

	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("err has type %T, want *StatusError", err)
	}

	if se.Response != resp {
		t.Error("StatusError does not carry the response")
	}

	if resp.StatusCode != 404 || resp.ToString() != "not found" {
		t.Errorf("response wrong: status=%d body=%q", resp.StatusCode, resp.ToString())
	}

	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error does not mention status: %v", err)
	}
}

func TestClientDisableStatusCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := New(WithStatusCheck(nil), WithBaseURL(srv.URL))

	resp, err := c.Do(context.Background(),
		NewRequest(MethodGet).WithAPI("A").WithVersion("v1"))
	if err != nil {
		t.Fatalf("expected nil error with check disabled, got %v", err)
	}

	if resp.StatusCode != 500 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestClientCustomStatusCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	req := func() *Request {
		return NewRequest(MethodGet).WithAPI("A").WithVersion("v1")
	}

	// Client-level check accepts only 200, so 418 is an error.
	c := New(WithStatusCheck(AcceptStatus(200)), WithBaseURL(srv.URL))
	if _, err := c.Do(context.Background(), req()); err == nil {
		t.Fatal("expected 418 to fail the client status check")
	}

	// A per-request override replaces the client-level check.
	overridden := req().WithStatusCheck(AcceptStatus(200, http.StatusTeapot))
	if _, err := c.Do(context.Background(), overridden); err != nil {
		t.Fatalf("per-request override should accept 418: %v", err)
	}
}

func TestRequestCloneIndependent(t *testing.T) {
	base := NewRequest(MethodPost).
		WithAPI("A").WithVersion("v1").WithForm("k", "v")

	derived := base.Clone().WithAPI("B").WithQuery("q", "1")

	if base.api != "A" {
		t.Errorf("base api mutated: %q", base.api)
	}

	if derived.api != "B" {
		t.Errorf("derived api wrong: %q", derived.api)
	}

	if _, ok := base.query["q"]; ok {
		t.Error("base query mutated by derived request")
	}

	if derived.query["q"] != "1" {
		t.Errorf("derived query wrong: %v", derived.query)
	}

	if base.form["k"] != "v" || derived.form["k"] != "v" {
		t.Error("form not preserved across clone")
	}
}

func TestClientPathAndQueryInt(t *testing.T) {
	var gotURI string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI

		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := New(WithAK("ak"), WithSK("sk"), WithBaseURL(srv.URL+"/"))
	req := NewRequest(MethodGet).WithAPI("PING").WithVersion("v1").
		Path("api/").Path("users/1").WithQueryInt("page", 2)

	if _, err := c.Do(context.Background(), req); err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	if !strings.Contains(gotURI, "/api/users/1") {
		t.Errorf("path building failed: %s", gotURI)
	}

	if !strings.Contains(gotURI, "page=2") {
		t.Errorf("query int failed: %s", gotURI)
	}
}

func TestRequestHostOverride(t *testing.T) {
	c := New()
	req := NewRequest(MethodGet).Host("b.example.com")
	parsed, _ := url.Parse("https://a.example.com/x")

	_, u := c.computeParamsAndURL(parsed, req, MethodGet)
	if !strings.HasPrefix(u, "https://b.example.com/x") {
		t.Errorf("host override failed: %s", u)
	}
}

func TestClientCustomTransport(t *testing.T) {
	rt := RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get(keyAPIName) != "PING" {
			t.Errorf("transport did not see signed request")
		}

		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"X-Mock": []string{"1"}},
			Body:       io.NopCloser(strings.NewReader("mocked")),
		}, nil
	})

	c := New(WithAK("ak"), WithSK("sk"), WithTransport(rt), WithBaseURL("http://example.com/CSB"))

	resp, err := c.Do(context.Background(),
		NewRequest(MethodGet).WithAPI("PING").WithVersion("v1"))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	if resp.ToString() != "mocked" {
		t.Errorf("body = %q", resp.ToString())
	}

	if resp.Header.Get("X-Mock") != "1" {
		t.Errorf("mock header missing: %v", resp.Header)
	}
}
