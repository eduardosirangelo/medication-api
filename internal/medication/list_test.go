package medication

import (
	"encoding/base64"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	q, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestParseListQuery_Valid(t *testing.T) {
	after := uuid.MustParse("019a9c3e-7a10-7000-8000-000000000009")
	filter := ListFilter{NameContains: "Amox Cl", Form: FormTablet}
	tests := []struct {
		query string
		want  ListParams
	}{
		{"", ListParams{Limit: DefaultPageSize}},
		{"limit=1", ListParams{Limit: 1}},
		{"limit=100", ListParams{Limit: MaxPageSize}},
		{"name=+Amox++Cl+&form=Tablet", ListParams{ListFilter: filter, Limit: DefaultPageSize}},
		{"name=&form=", ListParams{Limit: DefaultPageSize}},
		{"name=amo", ListParams{ListFilter: ListFilter{NameContains: "amo"}, Limit: DefaultPageSize}},
		// Characters, not bytes: three runes are enough.
		{"name=%C3%A9l%C3%A9", ListParams{ListFilter: ListFilter{NameContains: "élé"}, Limit: DefaultPageSize}},
		{
			"name=Amox+Cl&form=tablet&cursor=" + encodeCursor(after, filter),
			ListParams{ListFilter: filter, After: after, Limit: DefaultPageSize},
		},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got, err := parseListQuery(mustQuery(t, tt.query))
			if err != nil {
				t.Fatalf("parseListQuery: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseListQuery_Invalid(t *testing.T) {
	tests := []struct {
		query      string
		wantFields []string
	}{
		{"limit=0", []string{"limit"}},
		{"limit=101", []string{"limit"}},
		{"limit=ten", []string{"limit"}},
		{"limit=-5", []string{"limit"}},
		{"form=syrup", []string{"form"}},
		{"name=" + strings.Repeat("a", MaxNameLength+1), []string{"name"}},
		{"name=am", []string{"name"}},
		{"name=+am+", []string{"name"}}, // counted after trimming
		{"from=tablet", []string{"from"}},
		{"form=tablet&form=capsule", []string{"form"}},
		{"cursor=not-base64!", []string{"cursor"}},
		{"cursor=" + base64.RawURLEncoding.EncodeToString([]byte("{}")), []string{"cursor"}},
		{"limit=0&form=syrup", []string{"form", "limit"}},
	}
	for _, tt := range tests {
		t.Run(tt.query[:min(len(tt.query), 40)], func(t *testing.T) {
			_, err := parseListQuery(mustQuery(t, tt.query))
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("want *ValidationError, got %v", err)
			}
			var got []string
			for _, f := range verr.Fields {
				got = append(got, f.Field)
			}
			if !sameElements(got, tt.wantFields) {
				t.Errorf("fields = %v, want %v", got, tt.wantFields)
			}
		})
	}
}

func sameElements(a, b []string) bool {
	count := map[string]int{}
	for _, s := range a {
		count[s]++
	}
	for _, s := range b {
		count[s]--
	}
	for _, n := range count {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestCursor_RoundTripAndFilterBinding(t *testing.T) {
	after := uuid.MustParse("019a9c3e-7a10-7000-8000-000000000009")
	f := ListFilter{NameContains: "amox", Form: FormCapsule}
	token := encodeCursor(after, f)

	got, err := decodeCursor(token, f)
	if err != nil || got != after {
		t.Fatalf("round trip: %v %v", got, err)
	}
	// Case-insensitive search returns the same rows, so the cursor stays valid.
	if _, err := decodeCursor(token, ListFilter{NameContains: "AMOX", Form: FormCapsule}); err != nil {
		t.Errorf("name case should not invalidate the cursor: %v", err)
	}
	for _, other := range []ListFilter{{NameContains: "ibu", Form: FormCapsule}, {NameContains: "amox"}, {}} {
		if _, err := decodeCursor(token, other); !errors.Is(err, errInvalidCursor) {
			t.Errorf("cursor accepted for different filters %+v", other)
		}
	}
	if strings.ContainsAny(token, "+/=") {
		t.Errorf("cursor must be URL-safe: %s", token)
	}
}

func TestBuildListQuery(t *testing.T) {
	after := uuid.MustParse("019a9c3e-7a10-7000-8000-000000000009")
	tests := []struct {
		name      string
		filter    ListFilter
		after     uuid.UUID
		wantWhere string
		wantArgs  []any
	}{
		{"no filters", ListFilter{}, uuid.Nil,
			"WHERE deleted_at IS NULL ORDER BY id DESC LIMIT $1", []any{21}},
		{"all filters", ListFilter{NameContains: "50%_a\\b", Form: FormLiquid}, after,
			"WHERE deleted_at IS NULL AND form = $1 AND name ILIKE $2 AND id < $3 ORDER BY id DESC LIMIT $4",
			[]any{"liquid", `%50\%\_a\\b%`, after, 21}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, args := buildListQuery(tt.filter, tt.after, 21)
			if !strings.HasSuffix(query, tt.wantWhere) {
				t.Errorf("query = %q\nwant suffix %q", query, tt.wantWhere)
			}
			if !reflect.DeepEqual(args, tt.wantArgs) {
				t.Errorf("args = %#v, want %#v", args, tt.wantArgs)
			}
		})
	}
}
