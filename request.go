package csbgo

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"strings"
)

// Method and content-type convenience constants.
const (
	MethodGet  = http.MethodGet
	MethodPost = http.MethodPost
)

const (
	ContentTypeJSON   = "application/json"
	ContentTypeForm   = "application/x-www-form-urlencoded"
	ContentTypeBinary = "application/octet-stream"
)

// Request describes a single CSB HTTP invocation. Use NewRequest to create it
// and the With* methods to build it up fluently.
//
// Query and Form values are both feed into the CSB signature (the broker signs
// the union of URL query params and form params), so do not pass a parameter in
// a different way than the service contract expects.
type Request struct {
	jsonBody any

	query  map[string]string
	form   map[string]string
	header map[string]string

	statusCheck func(int) bool
	api         string
	version     string
	ak          string
	sk          string

	method      string
	host        string
	contentType string

	paths []string

	body    []byte
	hasJSON bool
}

// NewRequest creates a request for the given method. method is case-insensitive;
// CSB services are typically called with "GET" or "POST".
//
// The request URL is the client's base URL (set with WithBaseURL), since a CSB
// client targets a single endpoint. Use Path/Host to refine it per request when
// needed.
func NewRequest(method string) *Request {
	return &Request{
		method: strings.ToUpper(method),
		query:  make(map[string]string),
		form:   make(map[string]string),
		header: make(map[string]string),
	}
}

// WithAPI sets the CSB API (service) name.
func (r *Request) WithAPI(api string) *Request { r.api = api; return r }

// WithVersion sets the API version.
func (r *Request) WithVersion(v string) *Request { r.version = v; return r }

// WithAK overrides the access key for this request only.
func (r *Request) WithAK(ak string) *Request { r.ak = ak; return r }

// WithSK overrides the secret key for this request only.
func (r *Request) WithSK(sk string) *Request { r.sk = sk; return r }

// WithMethod overrides the HTTP method used for this request.
func (r *Request) WithMethod(m string) *Request { r.method = strings.ToUpper(m); return r }

// Host overrides the host of the request URL.
func (r *Request) Host(host string) *Request { r.host = host; return r }

// Path joins a path segment onto the request URL per RFC 3986. A leading "/"
// overrides the existing path; "./" and "../" are resolved into their absolute
// form when the URL is built.
func (r *Request) Path(path string) *Request {
	r.paths = append(r.paths, path)
	return r
}

// Pathf calls Path with fmt.Sprintf. Do not pass user input through %s.
func (r *Request) Pathf(format string, a ...any) *Request {
	return r.Path(fmt.Sprintf(format, a...))
}

// WithQueryInt adds a URL query parameter whose value is an integer.
func (r *Request) WithQueryInt(key string, value int) *Request {
	return r.WithQuery(key, strconv.Itoa(value))
}

// WithQuery adds a single URL query parameter.
func (r *Request) WithQuery(key, value string) *Request { r.query[key] = value; return r }

// WithQueries adds multiple URL query parameters.
func (r *Request) WithQueries(pairs map[string]string) *Request {
	maps.Copy(r.query, pairs)

	return r
}

// WithForm adds a single form (body) parameter.
func (r *Request) WithForm(key, value string) *Request { r.form[key] = value; return r }

// WithForms adds multiple form (body) parameters.
func (r *Request) WithForms(pairs map[string]string) *Request {
	maps.Copy(r.form, pairs)

	return r
}

// WithHeader adds a single HTTP header.
func (r *Request) WithHeader(key, value string) *Request { r.header[key] = value; return r }

// WithHeaders adds multiple HTTP headers.
func (r *Request) WithHeaders(h map[string]string) *Request {
	maps.Copy(r.header, h)

	return r
}

// WithBody sets a raw request body with an explicit content type. For POST
// requests without an explicit body, form parameters are sent as
// application/x-www-form-urlencoded automatically.
func (r *Request) WithBody(contentType string, body []byte) *Request {
	r.body = body
	r.hasJSON = false
	r.jsonBody = nil
	r.contentType = contentType

	return r
}

// WithJSON marshals v to JSON at call time and sets content type to
// application/json. Marshal errors are surfaced by Client.Do.
func (r *Request) WithJSON(v any) *Request {
	r.jsonBody = v
	r.hasJSON = true
	r.body = nil
	r.contentType = ContentTypeJSON

	return r
}

// WithStatusCheck overrides the status check for this request only. Pass nil to
// disable status checking entirely, or a function such as AcceptStatus to
// accept specific codes.
func (r *Request) WithStatusCheck(f func(int) bool) *Request {
	r.statusCheck = f
	return r
}

// Clone returns a copy of the request that is safe to mutate independently. It
// is useful for holding a reusable "endpoint template" and deriving per-call
// variants without disturbing the original.
func (r *Request) Clone() *Request {
	cp := *r
	cp.query = cloneMap(r.query)
	cp.form = cloneMap(r.form)
	cp.header = cloneMap(r.header)

	cp.paths = append([]string(nil), r.paths...)
	if r.body != nil {
		cp.body = append([]byte(nil), r.body...)
	}

	return &cp
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}

	out := make(map[string]string, len(m))
	maps.Copy(out, m)

	return out
}

// resolveBody materializes the request body for POST-style methods. It reports
// (nil, "", nil) when the request carries no body of its own.
func (r *Request) resolveBody() ([]byte, string, error) {
	if r.hasJSON {
		b, err := json.Marshal(r.jsonBody)
		if err != nil {
			return nil, "", wrapError(err, "marshal request body")
		}

		ct := r.contentType
		if ct == "" {
			ct = ContentTypeJSON
		}

		return b, ct, nil
	}

	if r.body != nil {
		ct := r.contentType
		if ct == "" {
			ct = ContentTypeBinary
		}

		return r.body, ct, nil
	}

	return nil, "", nil
}
