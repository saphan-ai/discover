package stack

import (
	"regexp"
	"strings"
)

// pinOf is the one place a version string becomes a row's version. It returns what the file says, as written, and nil
// when the file says nothing a reader could call a pin: an empty value or the floating tag "latest". It never
// supplies a version the file does not hold.
//
// A URL in a version carries no credentials and no query string into the report.
func pinOf(spec string) *string {
	s := strings.TrimSpace(spec)
	if s == "" || strings.EqualFold(s, "latest") {
		return nil
	}
	s = redactURLs(s)
	return &s
}

var (
	urlUserinfo = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s@]+@`)
	urlQuery    = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^\s?#]*)\?[^\s#]*`)
)

func redactURLs(s string) string {
	s = urlUserinfo.ReplaceAllString(s, "${1}")
	return urlQuery.ReplaceAllString(s, "${1}")
}
