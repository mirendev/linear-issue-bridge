package github

import "regexp"

var issuePattern = regexp.MustCompile(`\b([A-Z]+-\d+)\b`)

// ScanIdentifiers extracts all Linear issue identifiers (e.g. MIR-42) from text.
func ScanIdentifiers(text string) []string {
	matches := issuePattern.FindAllString(text, -1)
	seen := make(map[string]bool, len(matches))
	var unique []string
	for _, m := range matches {
		if !seen[m] {
			seen[m] = true
			unique = append(unique, m)
		}
	}
	return unique
}
