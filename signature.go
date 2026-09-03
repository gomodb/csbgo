package csbgo

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CSB protocol keys. These are part of the CSB wire protocol and must not be
// changed; a service published on CSB validates the signature against them.
const (
	keyAPIName      = "_api_name"
	keyAPIVersion   = "_api_version"
	keyAPIAccessKey = "_api_access_key"
	keyAPISecretKey = "_api_secret_key"
	keyAPISignature = "_api_signature"
	keyAPITimestamp = "_api_timestamp"
)

// canonicalize renders params into the canonical form CSB signs: keys sorted
// in ascending byte order, each pair joined as "k=v", pairs joined with "&".
// Values are used verbatim (no escaping), matching the CSB reference client.
func canonicalize(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}

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

	return b.String()
}

// sign computes CSB's request signature:
//
//	base64(HMAC-SHA1(secretKey, canonicalize(params)))
func sign(params map[string]string, secretKey string) string {
	mac := hmac.New(sha1.New, []byte(secretKey))
	mac.Write([]byte(canonicalize(params)))

	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// buildSignedHeaders derives the CSB protocol headers from the request params.
//
// It works on a copy of params (the caller's map is never mutated) and, when
// ak is non-empty, computes the _api_signature over the merged parameter set.
// now is injected so tests can pin the timestamp deterministically.
func buildSignedHeaders(
	params map[string]string,
	api, version, ak, sk string,
	now time.Time,
) map[string]string {
	merged := make(map[string]string, len(params)+4)
	maps.Copy(merged, params)

	merged[keyAPIName] = api
	merged[keyAPIVersion] = version
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	merged[keyAPITimestamp] = ts

	headers := map[string]string{
		keyAPIName:      api,
		keyAPIVersion:   version,
		keyAPITimestamp: ts,
	}

	if ak != "" {
		merged[keyAPIAccessKey] = ak
		headers[keyAPIAccessKey] = ak

		// The secret key and any pre-existing signature must never be part of
		// the signed string.
		delete(merged, keyAPISecretKey)
		delete(merged, keyAPISignature)

		headers[keyAPISignature] = sign(merged, sk)
	}

	return headers
}
