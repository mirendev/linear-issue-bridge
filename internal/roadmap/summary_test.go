package roadmap

import (
	"strings"
	"testing"
)

// These cases are ported from the website's roadmap.test.ts. They are the
// "no internal prose leaks" guarantee, so the wording of the expectations is
// deliberately identical to the TypeScript original.
func TestRoadmapSummary(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "inline label yields the summary sentence only",
			raw:  "Roadmap Summary: Deploy without a Dockerfile wrapper.\n\nInternal: RFD-91, depends on MIR-1428.",
			want: "Deploy without a Dockerfile wrapper.",
		},
		{
			name: "heading form ends at the next blank line",
			raw:  "Context first.\n\n## Roadmap Summary\n\nMachine-readable docs entry points.\n\n## Design\nInternals.",
			want: "Machine-readable docs entry points.",
		},
		{
			name: "no block means a blank body, not the scraped prose",
			raw:  "Long internal prose with no summary block. Never for the public board.",
			want: "",
		},
		{
			name: "issue descriptions use the same rule",
			raw:  "Roadmap Summary: Upgrades without dropped requests.\n\n## Problem\nInternal notes that must never render.",
			want: "Upgrades without dropped requests.",
		},
		{
			name: "empty input stays empty",
			raw:  "",
			want: "",
		},
		{
			name: "bold-wrapped label is recognised",
			raw:  "**Roadmap Summary:** Bring your own image.\n\nInternal.",
			want: "Bring your own image.",
		},
		{
			name: "match is case-insensitive",
			raw:  "roadmap summary: Lowercase authors count too.\n\nInternal.",
			want: "Lowercase authors count too.",
		},
		{
			name: "a summary mid-document still wins over surrounding prose",
			raw:  "Internal preamble.\n\nRoadmap Summary: The public sentence.\n\nMore internals.",
			want: "The public sentence.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RoadmapSummary(tt.raw); got != tt.want {
				t.Errorf("RoadmapSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCleanDescription(t *testing.T) {
	// The website's "description is cleaned" case: embeds, emphasis markers,
	// and markdown links all have to come out.
	raw := "Durable disks.\n\n<linear-embed>junk</linear-embed> **bold** [rfd](http://x)"
	got := CleanDescription(raw, defaultSummaryMax)

	if strings.Contains(got, "<") {
		t.Errorf("CleanDescription() = %q, still contains markup", got)
	}
	if strings.Contains(got, "linear-embed") {
		t.Errorf("CleanDescription() = %q, still contains an embed tag", got)
	}
	if !strings.Contains(got, "bold") {
		t.Errorf("CleanDescription() = %q, lost the emphasised text", got)
	}
	if strings.Contains(got, "http://x") {
		t.Errorf("CleanDescription() = %q, kept a link target", got)
	}
	if !strings.Contains(got, "rfd") {
		t.Errorf("CleanDescription() = %q, lost the link label", got)
	}
}

func TestCleanDescriptionTruncates(t *testing.T) {
	long := strings.Repeat("word ", 60)
	got := CleanDescription(long, 20)

	if !strings.HasSuffix(got, "…") {
		t.Errorf("CleanDescription() = %q, want an ellipsis suffix", got)
	}
	if len([]rune(got)) > 21 { // 20 runes plus the ellipsis
		t.Errorf("CleanDescription() = %q, longer than the cap", got)
	}
	if strings.HasSuffix(strings.TrimSuffix(got, "…"), " ") {
		t.Errorf("CleanDescription() = %q, left a dangling separator", got)
	}
}

// A multi-byte body must not be sliced mid-character. The TypeScript original
// counts UTF-16 units; counting runes here is the closest correct equivalent.
func TestCleanDescriptionRuneSafe(t *testing.T) {
	got := CleanDescription(strings.Repeat("héllo wörld ", 30), 25)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("CleanDescription() = %q, want an ellipsis suffix", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Errorf("CleanDescription() = %q, split a multi-byte character", got)
	}
}
