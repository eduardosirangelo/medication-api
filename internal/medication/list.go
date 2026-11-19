package medication

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// ListFilter narrows a listing. Zero values mean "no filter".
type ListFilter struct {
	NameContains string
	Form         Form
}

// ListParams is a validated list request. Results are ordered newest first
// by ID; After is the keyset position (uuid.Nil starts from the newest).
type ListParams struct {
	ListFilter
	After uuid.UUID
	Limit int
}

// Page is one page of results. HasMore means another page exists after the
// last item.
type Page struct {
	Items   []Medication
	HasMore bool
}

var errInvalidCursor = errors.New("is invalid or was issued for different filters")

var listQueryParams = map[string]bool{"name": true, "form": true, "limit": true, "cursor": true}

// Query problems are reported as field errors, like body validation, but the
// handler answers them with 400: the request target itself is malformed.
func parseListQuery(q url.Values) (ListParams, error) {
	var verr ValidationError
	rejectUnknownOrRepeated(q, &verr)
	p := ListParams{
		ListFilter: ListFilter{
			NameContains: parseNameFilter(q.Get("name"), &verr),
			Form:         parseFormFilter(q.Get("form"), &verr),
		},
		Limit: parseLimit(q.Get("limit"), &verr),
	}
	if len(verr.Fields) == 0 {
		p.After = parseCursorParam(q.Get("cursor"), p.ListFilter, &verr)
	}
	if len(verr.Fields) > 0 {
		return ListParams{}, &verr
	}
	return p, nil
}

// Strict like the JSON decoder: a typo such as ?from=tablet must fail rather
// than silently return an unfiltered list.
func rejectUnknownOrRepeated(q url.Values, verr *ValidationError) {
	for key, vals := range q {
		switch {
		case !listQueryParams[key]:
			verr.add(key, "is not a supported query parameter")
		case len(vals) > 1:
			verr.add(key, "must be given at most once")
		}
	}
}

func parseNameFilter(raw string, verr *ValidationError) string {
	name := NormalizeName(raw)
	if utf8.RuneCountInString(name) > MaxNameLength {
		verr.add("name", "must be at most 200 characters")
	}
	return name
}

func parseFormFilter(raw string, verr *ValidationError) Form {
	if isBlank(raw) {
		return ""
	}
	f, ok := ParseForm(raw)
	if !ok {
		verr.add("form", "must be one of: "+formList)
	}
	return f
}

func parseLimit(raw string, verr *ValidationError) int {
	if raw == "" {
		return DefaultPageSize
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > MaxPageSize {
		verr.add("limit", "must be an integer from 1 to "+strconv.Itoa(MaxPageSize))
		return DefaultPageSize
	}
	return n
}

func parseCursorParam(raw string, f ListFilter, verr *ValidationError) uuid.UUID {
	if raw == "" {
		return uuid.Nil
	}
	after, err := decodeCursor(raw, f)
	if err != nil {
		verr.add("cursor", err.Error())
	}
	return after
}

// The cursor is opaque to clients so its format can change freely. It is not
// signed: forging one only lets a client choose where its own listing starts.
// Binding it to the filters makes reuse with different filters an explicit
// error instead of a silently inconsistent page.
type cursor struct {
	After   uuid.UUID `json:"a"`
	Filters string    `json:"f"`
}

func encodeCursor(after uuid.UUID, f ListFilter) string {
	b, _ := json.Marshal(cursor{After: after, Filters: filterFingerprint(f)}) // cannot fail: fixed types
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(token string, f ListFilter) (uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return uuid.Nil, errInvalidCursor
	}
	var c cursor
	if json.Unmarshal(raw, &c) != nil || c.After == uuid.Nil || c.Filters != filterFingerprint(f) {
		return uuid.Nil, errInvalidCursor
	}
	return c.After, nil
}

func filterFingerprint(f ListFilter) string {
	sum := sha256.Sum256([]byte(strings.ToLower(f.NameContains) + "\x00" + string(f.Form)))
	return hex.EncodeToString(sum[:8])
}
