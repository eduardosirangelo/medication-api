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
func NoneMatch(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "*" {
		return true
	}
	for _, candidate := range splitTags(header) {
		if strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}

// IfMatchVersions returns the versions named by strong tags in an If-Match
// value. The comparison itself happens in the database update, so the check
// and the write are one atomic step; weak or malformed tags can never match
// under the strong comparison and are skipped.
func IfMatchVersions(header string) []int64 {
	var versions []int64
	for _, candidate := range splitTags(header) {
		if v, ok := parseStrongETag(candidate); ok {
			versions = append(versions, v)
		}
	}
	return versions
}

func parseStrongETag(tag string) (int64, bool) {
	if len(tag) < 3 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		return 0, false
	}
	v, err := strconv.ParseInt(tag[1:len(tag)-1], 10, 64)
	return v, err == nil && v > 0
}

// A plain comma split is enough because our tags are digits only.
func splitTags(header string) []string {
	var tags []string
	for _, t := range strings.Split(header, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}
