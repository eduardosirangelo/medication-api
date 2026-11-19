package httpx

import (
	"reflect"
	"testing"
)

func TestETag(t *testing.T) {
	if got := ETag(3); got != `"3"` {
		t.Fatalf("ETag(3) = %s", got)
	}
}

func TestNoneMatch(t *testing.T) {
	etag := ETag(3)
	tests := map[string]bool{
		``:          false,
		`"3"`:       true,
		`"4"`:       false,
		`*`:         true,
		`W/"3"`:     true, // weak comparison
		`"1", "3"`:  true,
		`"1",W/"3"`: true,
		` "3" `:     true,
		`3`:         false, // unquoted is not an entity tag
	}
	for header, want := range tests {
		if got := NoneMatch(header, etag); got != want {
			t.Errorf("NoneMatch(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestIfMatchVersions(t *testing.T) {
	tests := []struct {
		header string
		want   []int64
	}{
		{``, nil},
		{`"3"`, []int64{3}},
		{` "3" `, []int64{3}},
		{`"1", "3"`, []int64{1, 3}},
		{`W/"3"`, nil}, // weak tags never satisfy If-Match
		{`W/"2", "3"`, []int64{3}},
		{`3`, nil},     // unquoted
		{`"abc"`, nil}, // not one of our tags
		{`"0"`, nil},   // versions start at 1
		{`"-1"`, nil},
		{`"99999999999999999999"`, nil}, // overflow
		{`*`, nil},                      // handled by the caller
	}
	for _, tt := range tests {
		if got := IfMatchVersions(tt.header); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("IfMatchVersions(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}
