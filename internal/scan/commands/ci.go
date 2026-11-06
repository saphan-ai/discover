package commands

import (
	"regexp"
	"strings"

	"github.com/saphan-ai/discover/internal/model"
)

func (s *scanner) ci(system, file string) {
	s.res.CI = append(s.res.CI, model.CI{System: system, Source: model.Source{File: file, Line: 1}})
}

var (
	runKey    = regexp.MustCompile(`^(\s*)(-\s+)?run:\s*(.*)$`)
	scriptKey = regexp.MustCompile(`^(\s*)(-\s+)?(script|before_script|after_script):\s*(.*)$`)
)

// isBlockHead is true for the header of a YAML block scalar: |, >, and their indicators.
func isBlockHead(v string) bool {
	v = strings.TrimSpace(v)
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v != "" && (v[0] == '|' || v[0] == '>') && strings.Trim(v[1:], "+-0123456789") == ""
}

// block reads the lines of a block scalar that follow line i and sit deeper than col. Each non-empty line is one
// command, cited at its own line. It returns the index of the last line it consumed.
func (s *scanner) block(p string, lines []string, i, col int) int {
	j := i
	for k := i + 1; k < len(lines); k++ {
		ind, text := indentOf(lines[k])
		if !text {
			j = k
			continue
		}
		if ind <= col {
			break
		}
		j = k
		if t := strings.TrimSpace(lines[k]); !strings.HasPrefix(t, "#") {
			s.command(classify(t), t, p, k+1)
		}
	}
	return j
}

// workflow cites the run steps of a GitHub Actions or Gitea Actions file.
func (s *scanner) workflow(p, system string, lines []string) bool {
	s.ci(system, p)
	for i := 0; i < len(lines); i++ {
		m := runKey.FindStringSubmatch(lines[i])
		if m == nil || strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
			continue
		}
		col := len(m[1]) + len(m[2])
		if isBlockHead(m[3]) {
			i = s.block(p, lines, i, col)
			continue
		}
		if v := scalar(m[3]); v != "" {
			s.command(classify(v), v, p, i+1)
		}
	}
	return true
}

// flowList splits [a, "b c"] into its items. It returns false when v is not a one-line flow list.
func flowList(v string) ([]string, bool) {
	v = strings.TrimSpace(v)
	if len(v) < 2 || v[0] != '[' || v[len(v)-1] != ']' {
		return nil, false
	}
	var out []string
	var cur strings.Builder
	var quote byte
	flush := func() {
		if item := scalar(cur.String()); item != "" {
			out = append(out, item)
		}
		cur.Reset()
	}
	for i := 1; i < len(v)-1; i++ {
		c := v[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
		case c == '"' || c == '\'':
			quote = c
			cur.WriteByte(c)
		case c == ',':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out, true
}

// gitlab cites the script, before_script and after_script commands of .gitlab-ci.yml.
func (s *scanner) gitlab(p string, lines []string, _ []byte) bool {
	s.ci("gitlab-ci", p)
	for i := 0; i < len(lines); i++ {
		m := scriptKey.FindStringSubmatch(lines[i])
		if m == nil || strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
			continue
		}
		col := len(m[1]) + len(m[2])
		if v := strings.TrimSpace(m[4]); v != "" {
			if items, ok := flowList(v); ok {
				for _, it := range items {
					s.command(classify(it), it, p, i+1)
				}
			} else if isBlockHead(v) {
				i = s.block(p, lines, i, col)
			} else if v := scalar(v); v != "" {
				s.command(classify(v), v, p, i+1)
			}
			continue
		}
		last := i
		for k := i + 1; k < len(lines); k++ {
			ind, text := indentOf(lines[k])
			t := strings.TrimSpace(lines[k])
			if !text || strings.HasPrefix(t, "#") {
				continue
			}
			item := t == "-" || strings.HasPrefix(t, "- ")
			if ind < col || (ind == col && !item) {
				break
			}
			last = k
			if !item {
				continue
			}
			v := strings.TrimSpace(strings.TrimPrefix(t, "-"))
			if isBlockHead(v) {
				k = s.block(p, lines, k, ind)
				last = k
			} else if v = scalar(v); v != "" {
				s.command(classify(v), v, p, k+1)
			}
		}
		i = last
	}
	return true
}
