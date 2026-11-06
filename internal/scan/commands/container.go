package commands

import (
	"strings"

	"github.com/saphan-ai/discover/internal/model"
)

func (s *scanner) container(kind, image, file string, line int) {
	s.res.Containers = append(s.res.Containers, model.Container{Kind: kind, Image: image, Source: model.Source{File: file, Line: line}})
}

// dockerfile cites the image of the last stage and every CMD and ENTRYPOINT. A continued instruction is joined into
// one command and cited at the line it starts on.
func (s *scanner) dockerfile(p string, lines []string, _ []byte) bool {
	image, imageLine := "", 1
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		start, parts := i, []string{}
		for {
			cont := strings.HasSuffix(t, `\`)
			parts = append(parts, strings.TrimSpace(strings.TrimSuffix(t, `\`)))
			if !cont || i+1 >= len(lines) {
				break
			}
			i++
			t = strings.TrimSpace(lines[i])
			for strings.HasPrefix(t, "#") && i+1 < len(lines) {
				i++
				t = strings.TrimSpace(lines[i])
			}
		}
		text := strings.Join(parts, " ")
		word, rest, _ := strings.Cut(text, " ")
		switch strings.ToUpper(word) {
		case "FROM":
			for _, f := range strings.Fields(rest) {
				if !strings.HasPrefix(f, "--") {
					image, imageLine = f, start+1
					break
				}
			}
		case "CMD", "ENTRYPOINT":
			s.command("run", text, p, start+1)
		}
	}
	s.container("dockerfile", image, p, imageLine)
	return true
}

// compose cites each service of a Compose file with its image, and the command it overrides.
func (s *scanner) compose(p string, lines []string, _ []byte) bool {
	in, svcInd, childInd := false, -1, -1
	type service struct {
		line  int
		image string
	}
	var cur *service
	n := 0
	flush := func() {
		if cur != nil {
			s.container("compose", cur.image, p, cur.line)
			n++
		}
		cur = nil
	}
	for i, l := range lines {
		ind, text := indentOf(l)
		t := strings.TrimSpace(l)
		if !text || strings.HasPrefix(t, "#") {
			continue
		}
		if ind == 0 {
			flush()
			in, svcInd = strings.TrimSpace(l) == "services:", -1
			continue
		}
		if !in {
			continue
		}
		if svcInd < 0 {
			svcInd = ind
		}
		if ind == svcInd {
			flush()
			if m := yamlKey.FindStringSubmatch(t); m != nil {
				cur, childInd = &service{line: i + 1}, -1
			}
			continue
		}
		if cur == nil {
			continue
		}
		if childInd < 0 {
			childInd = ind
		}
		if ind != childInd {
			continue
		}
		if v, ok := strings.CutPrefix(t, "image:"); ok {
			cur.image = scalar(v)
		} else if v, ok := strings.CutPrefix(t, "command:"); ok {
			if v = strings.TrimSpace(v); v != "" && !isBlockHead(v) {
				if _, list := flowList(v); !list {
					v = scalar(v)
				} else if c := strings.Index(v, "] #"); c >= 0 {
					v = v[:c+1]
				}
				s.command("run", v, p, i+1)
			}
		}
	}
	flush()
	if n == 0 {
		s.container("compose", "", p, 1)
	}
	return true
}
