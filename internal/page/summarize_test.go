package page

import "testing"

func TestSummarize(t *testing.T) {
	tests := []struct {
		name string
		src  string
		max  int
		want string
	}{
		{
			name: "strips emphasis and code markers",
			src:  "This is a **bold** and `code` description.",
			max:  200,
			want: "This is a bold and code description.",
		},
		{
			name: "links collapse to their text",
			src:  "Fixed in [mirendev/runtime#858](https://linear.app/x), surfaced by [MIR-1242](https://linear.app/y).",
			max:  200,
			want: "Fixed in mirendev/runtime#858, surfaced by MIR-1242.",
		},
		{
			name: "images collapse to alt text",
			src:  "Before ![a screenshot](https://img/x.png) after.",
			max:  200,
			want: "Before a screenshot after.",
		},
		{
			name: "collapses whitespace and newlines",
			src:  "# Heading\n\nFirst para.\n\n- one\n- two",
			max:  200,
			want: "Heading First para. one two",
		},
		{
			name: "truncates on a word boundary with ellipsis",
			src:  "one two three four five six seven eight nine ten",
			max:  20,
			want: "one two three four…",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summarize(tt.src, tt.max); got != tt.want {
				t.Errorf("summarize(%q, %d) = %q, want %q", tt.src, tt.max, got, tt.want)
			}
		})
	}
}
