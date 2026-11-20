package main

import "testing"

func TestParseCommand(t *testing.T) {
	tests := map[string]struct {
		args    []string
		want    string
		wantErr bool
	}{
		"default":    {nil, "serve", false},
		"serve":      {[]string{"serve"}, "serve", false},
		"migrate":    {[]string{"migrate"}, "migrate", false},
		"seed":       {[]string{"seed"}, "seed", false},
		"unknown":    {[]string{"drop"}, "", true},
		"extra args": {[]string{"seed", "now"}, "", true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseCommand(tt.args)
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Errorf("parseCommand(%v) = %q, %v", tt.args, got, err)
			}
		})
	}
}
