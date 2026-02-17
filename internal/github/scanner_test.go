package github

import (
	"reflect"
	"testing"
)

func TestScanIdentifiers(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "single identifier",
			input: "Fixed MIR-42 in latest commit",
			want:  []string{"MIR-42"},
		},
		{
			name:  "multiple identifiers",
			input: "This fixes MIR-1 and MIR-2, related to MIR-100",
			want:  []string{"MIR-1", "MIR-2", "MIR-100"},
		},
		{
			name:  "duplicates",
			input: "MIR-42 is the same as MIR-42",
			want:  []string{"MIR-42"},
		},
		{
			name:  "no identifiers",
			input: "Just a regular commit message",
			want:  nil,
		},
		{
			name:  "different team prefixes",
			input: "ABC-1 and DEF-99",
			want:  []string{"ABC-1", "DEF-99"},
		},
		{
			name:  "in URL",
			input: "See https://linear.app/miren/issue/MIR-42/some-title",
			want:  []string{"MIR-42"},
		},
		{
			name:  "lowercase not matched",
			input: "mir-42 should not match",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScanIdentifiers(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ScanIdentifiers(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
