package httpx

import (
	"strconv"
	"strings"
)

// ETag returns a strong entity tag for a resource version, e.g. `"3"`.
//
// It must be strong: RFC 9110 §13.1.1 says If-Match uses the strong
// comparison, under which a weak tag (W/"3") never matches.
func ETag(version int64) string {
	return `"` + strconv.FormatInt(version, 10) + `"`
}

// NoneMatch reports whether an If-None-Match header value matches etag, using
// the weak comparison (RFC 9110 §13.1.2): W/"3" and "3" are equivalent.
// A true result on GET means the client's copy is current → 304.
func NoneMatch(header, etag string) bool {
	return matches(header, etag, false)
}

// Match reports whether an If-Match header value matches etag, using the
// strong comparison (RFC 9110 §13.1.1): weak tags never match.
func Match(header, etag string) bool {
	return matches(header, etag, true)
}

// matches handles "*" and comma-separated lists. Our tags never contain
// commas, so a plain split is sufficient.
func matches(header, etag string, strong bool) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if strings.HasPrefix(candidate, "W/") {
			if strong {
				continue
			}
			candidate = strings.TrimPrefix(candidate, "W/")
		}
		if candidate == etag {
			return true
		}
	}
	return false
}
