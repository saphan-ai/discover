package report

import (
	"fmt"
	"strings"
	"unicode"
)

// Every string that comes from the repository (a name, a key, a command, a quoted line) passes through one of the
// three functions below before it reaches the report. None of them can open a heading, a link, an HTML tag or a
// code span, and none lets a hidden character (a control, a bidi override, a zero-width mark) through unseen.

// textEscaped is every ASCII character that can start Markdown syntax inside a line: emphasis, code spans, links,
// images, HTML, headings, table cells, strikethrough, math, entities, and the GFM autolinks (`scheme:` and `@`).
const textEscaped = "\\`*_{}[]()<>#!|~$&:@"

// hidden reports whether r would print as nothing, or would change how its neighbours print.
func hidden(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)
}

// visible names a hidden character by its code point.
func visible(r rune) string {
	return fmt.Sprintf("<U+%04X>", r)
}

// text escapes s for running Markdown prose, a table cell included: each syntax character is backslash-escaped and
// each hidden character is printed by its code point. A dot after "www" is escaped so no GFM autolink forms.
func text(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case hidden(r):
			fmt.Fprintf(&b, `\<U+%04X\>`, r)
		case strings.ContainsRune(textEscaped, r):
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '.' && i >= 3 && strings.EqualFold(string(runes[i-3:i]), "www"):
			b.WriteString(`\.`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// code puts s in a code span, and is used only inside a table cell. The fence is one backtick longer than the
// longest run of backticks in s, so s cannot close it; a pipe is escaped so it cannot end the cell (GFM removes the
// backslash before a pipe in a cell, even inside a code span); hidden characters are named.
func code(s string) string {
	var b strings.Builder
	run, longest := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
		switch {
		case hidden(r):
			b.WriteString(visible(r))
		case r == '|':
			b.WriteString(`\|`)
		default:
			b.WriteRune(r)
		}
	}
	body := b.String()
	if strings.HasPrefix(body, "`") || strings.HasSuffix(body, "`") || strings.HasPrefix(body, " ") || strings.HasSuffix(body, " ") {
		body = " " + body + " "
	}
	fence := strings.Repeat("`", longest+1)
	return fence + body + fence
}

// yamlString writes s as one double-quoted YAML scalar for the frontmatter.
func yamlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case hidden(r) && r > 0xFFFF:
			fmt.Fprintf(&b, `\U%08X`, r)
		case hidden(r):
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
