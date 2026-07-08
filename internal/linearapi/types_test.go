package linearapi

import "testing"

func TestIssueIsPublic(t *testing.T) {
	labels := func(names ...string) []Label {
		out := make([]Label, len(names))
		for i, n := range names {
			out[i] = Label{Name: n}
		}
		return out
	}

	tests := []struct {
		name   string
		labels []Label
		want   bool
	}{
		{"public", labels("public"), true},
		{"no labels", nil, false},
		{"unrelated label", labels("bug"), false},
		{"security only", labels("security"), false},
		// The security override always wins over public, even when both are
		// present — a sensitive issue must never leak onto the bridge.
		{"public and security", labels("public", "security"), false},
		{"security and public order-independent", labels("security", "public"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := &Issue{Labels: tt.labels}
			if got := issue.IsPublic(); got != tt.want {
				t.Errorf("IsPublic() = %v, want %v", got, tt.want)
			}
		})
	}
}
