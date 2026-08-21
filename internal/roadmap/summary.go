package roadmap

import (
	"regexp"
	"strings"
)

// defaultSummaryMax caps a cleaned description. Cards are a glance, not a read.
const defaultSummaryMax = 160

var (
	// <linear-embed>, <issue>, and friends.
	tagPattern = regexp.MustCompile(`<[^>]*>`)
	// Markdown links and images collapse to their label.
	mdLinkPattern = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	// Bullets, quotes, and headings at the start of a line.
	lineMarkPattern = regexp.MustCompile("(?m)^[ \t]*[#>*-]+[ \t]+")
	// Inline emphasis, but in-word hyphens survive.
	emphasisPattern = regexp.MustCompile("[*`_]")
	whitespaceRun   = regexp.MustCompile(`\s+`)
	trailingWord    = regexp.MustCompile(`\s+\S*$`)
)

// CleanDescription strips Linear markup and collapses to a short single line.
func CleanDescription(raw string, max int) string {
	if raw == "" {
		return ""
	}
	text := tagPattern.ReplaceAllString(raw, " ")
	text = mdLinkPattern.ReplaceAllString(text, "${1}")
	text = lineMarkPattern.ReplaceAllString(text, "")
	text = emphasisPattern.ReplaceAllString(text, " ")
	text = whitespaceRun.ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	// Count in runes, not bytes, so a multi-byte character can't be cut in half.
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return trailingWord.ReplaceAllString(string(runes[:max]), "") + "…"
}

var (
	// An inline "Roadmap Summary:" label or a "## Roadmap Summary" heading.
	summaryHeader = regexp.MustCompile("(?i)(?:^|\n)[ \t]*(?:#{1,6}[ \t]*)?\\**roadmap summary\\**[ \t]*:?[ \t]*")
	// The block ends at the next blank line. RE2 has no lookahead, so the
	// TypeScript original's (?=\n[ \t]*\n|$) becomes an explicit scan.
	blankLine = regexp.MustCompile(`\n[ \t]*\n`)
)

// RoadmapSummary pulls the authored card copy out of a Linear project document
// or issue description. Card bodies are written for the board on purpose: a
// project without a "Roadmap Summary:" block gets an empty body rather than a
// scrape of its internal prose, so nothing leaks by default.
func RoadmapSummary(raw string) string {
	loc := summaryHeader.FindStringIndex(raw)
	if loc == nil {
		return ""
	}
	body := strings.TrimLeft(raw[loc[1]:], " \t\r\n")
	if end := blankLine.FindStringIndex(body); end != nil {
		body = body[:end[0]]
	}
	return CleanDescription(body, defaultSummaryMax)
}
