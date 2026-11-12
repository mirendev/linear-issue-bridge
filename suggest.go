package main

import (
	"strings"
	"unicode"
)

// attachmentMarkdown is the markdown /suggest appends for an uploaded file.
// The filename is whatever the submitter's browser sent, so it's cut down to
// characters that can't close the link text or open markup.
func attachmentMarkdown(filename, assetURL string, image bool) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), strings.ContainsRune(" .-_", r):
			return r
		default:
			return '_'
		}
	}, filename)
	if strings.TrimSpace(name) == "" {
		name = "attachment"
	}
	link := "[" + name + "](" + assetURL + ")"
	if image {
		return "!" + link
	}
	return link
}
