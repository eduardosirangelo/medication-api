package httpx

import (
	"strconv"
	"strings"
)

// Strong on purpose: If-Match uses the strong comparison (RFC 9110 §13.1.1),
// under which a weak tag like W/"3" never matches.
func ETag(version int64) string {
	return `"` + strconv.FormatInt(version, 10) + `"`
}

// NoneMatch evaluates If-None-Match with the weak comparison (W/"3" equals "3").
func NoneMatch(header, etag string) bool { return matches(header, etag, false) }

// Match evaluates If-Match with the strong comparison (weak tags never match).
func Match(header, etag string) bool { return matches(header, etag, true) }

// A plain comma split is enough because our tags are digits only.
func matches(header, etag string, strong bool) bool {
	header = strings.TrimSpace(header)
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
		if candidate != "" && candidate == etag {
			return true
		}
	}
	return false
}
