package main

import (
	"net/http"
	"strings"
)

// contentSecurityPolicy keeps any markup that slips into a page from running
// script: scripts load only from us and Fathom, never inline. Styles allow
// inline because templates color status pills and labels with style=""
// attributes, which can't execute code. Images come from anywhere over HTTPS
// because issue descriptions link uploads and screenshots from many hosts.
var contentSecurityPolicy = strings.Join([]string{
	"default-src 'self'",
	"script-src 'self' https://cdn.usefathom.com",
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
	"font-src https://fonts.gstatic.com",
	"img-src 'self' https: data:",
	"connect-src 'self' https://cdn.usefathom.com",
	"object-src 'none'",
	"base-uri 'none'",
	"form-action 'self'",
	"frame-ancestors 'none'",
}, "; ")

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}
