package stack

import (
	"fmt"
	"strconv"
	"strings"
)

// A small TOML reader: tables, dotted keys, strings, arrays and inline tables, with line numbers. It reads what a
// manifest holds and returns an error, with a line, for anything it cannot read. It does not decode numbers or dates.

type tval struct {
	kind byte // 's' string, 'a' array, 't' inline table, 'o' anything else
	s    string
	arr  []tval
	tab  []tentry
	line int
}

// tentry is one key of a table. Table is the header path it sits under; Key is its own, dotted, path.
type tentry struct {
	table []string
	key   []string
	val   tval
	line  int
}

type tparser struct {
	b    []byte
	i    int
	line int
}

func parseTOML(b []byte) ([]tentry, error) {
	p := &tparser{b: b, line: 1}
	var out []tentry
	var table []string
	for {
		p.skipAll()
		if p.eof() {
			return out, nil
		}
		if p.b[p.i] == '[' {
			p.i++
			if !p.eof() && p.b[p.i] == '[' {
				p.i++
			}
			keys, err := p.keyPath()
			if err != nil {
				return nil, err
			}
			p.skipSpace()
			if p.eof() || p.b[p.i] != ']' {
				return nil, p.errf("unterminated table header")
			}
			p.i++
			if !p.eof() && p.b[p.i] == ']' {
				p.i++
			}
			table = keys
			// A header is recorded as an entry with no key, so a reader sees the table and the line it opens on.
			out = append(out, tentry{table: table, line: p.line})
			if err := p.endOfLine(); err != nil {
				return nil, err
			}
			continue
		}
		line := p.line
		keys, err := p.keyPath()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.eof() || p.b[p.i] != '=' {
			return nil, p.errf("expected =")
		}
		p.i++
		p.skipSpace()
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		if err := p.endOfLine(); err != nil {
			return nil, err
		}
		out = append(out, tentry{table: table, key: keys, val: v, line: line})
	}
}

func (p *tparser) eof() bool { return p.i >= len(p.b) }

func (p *tparser) errf(format string, a ...any) error {
	return fmt.Errorf("line %d: %s", p.line, fmt.Sprintf(format, a...))
}

func (p *tparser) skipSpace() {
	for !p.eof() && (p.b[p.i] == ' ' || p.b[p.i] == '\t') {
		p.i++
	}
}

// skipAll skips white space, newlines and comments.
func (p *tparser) skipAll() {
	for !p.eof() {
		switch p.b[p.i] {
		case ' ', '\t', '\r':
			p.i++
		case '\n':
			p.line++
			p.i++
		case '#':
			for !p.eof() && p.b[p.i] != '\n' {
				p.i++
			}
		default:
			return
		}
	}
}

// endOfLine accepts trailing space and a comment, then a newline or the end of the file.
func (p *tparser) endOfLine() error {
	p.skipSpace()
	if !p.eof() && p.b[p.i] == '#' {
		for !p.eof() && p.b[p.i] != '\n' {
			p.i++
		}
	}
	if !p.eof() && p.b[p.i] == '\r' {
		p.i++
	}
	if p.eof() {
		return nil
	}
	if p.b[p.i] != '\n' {
		return p.errf("unexpected text after a value")
	}
	p.i++
	p.line++
	return nil
}

func isBare(c byte) bool {
	return c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func (p *tparser) keyPath() ([]string, error) {
	var keys []string
	for {
		p.skipSpace()
		if p.eof() {
			return nil, p.errf("expected a key")
		}
		switch c := p.b[p.i]; {
		case c == '"' || c == '\'':
			s, err := p.quoted()
			if err != nil {
				return nil, err
			}
			keys = append(keys, s)
		case isBare(c):
			start := p.i
			for !p.eof() && isBare(p.b[p.i]) {
				p.i++
			}
			keys = append(keys, string(p.b[start:p.i]))
		default:
			return nil, p.errf("expected a key")
		}
		p.skipSpace()
		if !p.eof() && p.b[p.i] == '.' {
			p.i++
			continue
		}
		return keys, nil
	}
}

// quoted reads a one-line basic or literal string, or a multi-line one.
func (p *tparser) quoted() (string, error) {
	q := p.b[p.i]
	multi := p.i+2 < len(p.b) && p.b[p.i+1] == q && p.b[p.i+2] == q
	if multi {
		p.i += 3
		if !p.eof() && p.b[p.i] == '\r' {
			p.i++
		}
		if !p.eof() && p.b[p.i] == '\n' {
			p.i++
			p.line++
		}
	} else {
		p.i++
	}
	var sb strings.Builder
	for !p.eof() {
		c := p.b[p.i]
		switch {
		case multi && c == q && p.i+2 < len(p.b) && p.b[p.i+1] == q && p.b[p.i+2] == q:
			p.i += 3
			return sb.String(), nil
		case !multi && c == q:
			p.i++
			return sb.String(), nil
		case c == '\n':
			if !multi {
				return "", p.errf("unterminated string")
			}
			p.line++
			sb.WriteByte(c)
			p.i++
		case c == '\\' && q == '"':
			p.i++
			if p.eof() {
				return "", p.errf("unterminated string")
			}
			e := p.b[p.i]
			p.i++
			switch e {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case 'b':
				sb.WriteByte('\b')
			case 'f':
				sb.WriteByte('\f')
			case '"', '\\':
				sb.WriteByte(e)
			case 'u', 'U':
				n := 4
				if e == 'U' {
					n = 8
				}
				if p.i+n > len(p.b) {
					return "", p.errf("bad escape")
				}
				r, err := strconv.ParseUint(string(p.b[p.i:p.i+n]), 16, 32)
				if err != nil {
					return "", p.errf("bad escape")
				}
				sb.WriteRune(rune(r))
				p.i += n
			case '\n':
				p.line++
				for !p.eof() && (p.b[p.i] == ' ' || p.b[p.i] == '\t' || p.b[p.i] == '\n') {
					if p.b[p.i] == '\n' {
						p.line++
					}
					p.i++
				}
			default:
				return "", p.errf("bad escape")
			}
		default:
			sb.WriteByte(c)
			p.i++
		}
	}
	return "", p.errf("unterminated string")
}

func (p *tparser) value() (tval, error) {
	if p.eof() {
		return tval{}, p.errf("expected a value")
	}
	line := p.line
	switch c := p.b[p.i]; c {
	case '"', '\'':
		s, err := p.quoted()
		return tval{kind: 's', s: s, line: line}, err
	case '[':
		p.i++
		v := tval{kind: 'a', line: line}
		for {
			p.skipAll()
			if p.eof() {
				return tval{}, p.errf("unterminated array")
			}
			if p.b[p.i] == ']' {
				p.i++
				return v, nil
			}
			e, err := p.value()
			if err != nil {
				return tval{}, err
			}
			v.arr = append(v.arr, e)
			p.skipAll()
			if !p.eof() && p.b[p.i] == ',' {
				p.i++
			}
		}
	case '{':
		p.i++
		v := tval{kind: 't', line: line}
		for {
			p.skipAll()
			if p.eof() {
				return tval{}, p.errf("unterminated inline table")
			}
			if p.b[p.i] == '}' {
				p.i++
				return v, nil
			}
			kl := p.line
			keys, err := p.keyPath()
			if err != nil {
				return tval{}, err
			}
			p.skipSpace()
			if p.eof() || p.b[p.i] != '=' {
				return tval{}, p.errf("expected =")
			}
			p.i++
			p.skipSpace()
			e, err := p.value()
			if err != nil {
				return tval{}, err
			}
			v.tab = append(v.tab, tentry{key: keys, val: e, line: kl})
			p.skipAll()
			if !p.eof() && p.b[p.i] == ',' {
				p.i++
			}
		}
	}
	start := p.i
	for !p.eof() && !strings.ContainsRune(",]}\n#", rune(p.b[p.i])) {
		p.i++
	}
	s := strings.TrimSpace(string(p.b[start:p.i]))
	if s == "" {
		return tval{}, p.errf("expected a value")
	}
	return tval{kind: 'o', s: s, line: line}, nil
}
