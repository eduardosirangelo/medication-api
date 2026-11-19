package httpx

import "testing"

func TestETag(t *testing.T) {
	if got := ETag(3); got != `"3"` {
		t.Fatalf("ETag(3) = %s", got)
	}
}

func TestMatchAndNoneMatch(t *testing.T) {
	etag := ETag(3)
	tests := []struct {
		header        string
		wantMatch     bool // If-Match, strong comparison
		wantNoneMatch bool // If-None-Match, weak comparison
	}{
		{``, false, false},
		{`"3"`, true, true},
		{`"4"`, false, false},
		{`*`, true, true},
		{`W/"3"`, false, true}, // weak tags never satisfy If-Match
		{`"1", "3"`, true, true},
		{`"1",W/"3"`, false, true},
		{` "3" `, true, true},
		{`3`, false, false}, // unquoted is not a valid entity tag
	}
	for _, tt := range tests {
		if got := Match(tt.header, etag); got != tt.wantMatch {
			t.Errorf("Match(%q) = %v, want %v", tt.header, got, tt.wantMatch)
		}
		if got := NoneMatch(tt.header, etag); got != tt.wantNoneMatch {
			t.Errorf("NoneMatch(%q) = %v, want %v", tt.header, got, tt.wantNoneMatch)
		}
	}
}
