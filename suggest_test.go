package main

import "testing"

func TestAttachmentMarkdown(t *testing.T) {
	tests := []struct {
		filename string
		image    bool
		want     string
	}{
		{"screenshot 2.png", true, "![screenshot 2.png](https://u/x)"},
		{"notes.txt", false, "[notes.txt](https://u/x)"},
		{"résumé.pdf", false, "[résumé.pdf](https://u/x)"},
		// The MIR-2202 probe's filename.
		{`x.png"><img src=x onerror=document.title="mfn-1">.png`, true,
			"![x.png___img src_x onerror_document.title__mfn-1__.png](https://u/x)"},
		{"a](javascript:alert(1))[b", false, "[a__javascript_alert_1___b](https://u/x)"},
		{"", true, "![attachment](https://u/x)"},
	}
	for _, tt := range tests {
		if got := attachmentMarkdown(tt.filename, "https://u/x", tt.image); got != tt.want {
			t.Errorf("attachmentMarkdown(%q) = %q, want %q", tt.filename, got, tt.want)
		}
	}
}
