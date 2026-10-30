package stack

import (
	"strings"
)

// goMod reads the go, toolchain and require lines of a go.mod.
func goMod(c *ctx, file string) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	inRequire := false
	for i, raw := range strings.Split(string(b), "\n") {
		line := i + 1
		text := strings.TrimSpace(stripSlashComment(raw))
		if text == "" {
			continue
		}
		if inRequire {
			if text == ")" {
				inRequire = false
				continue
			}
			goRequire(c, file, text, line)
			continue
		}
		fields := strings.Fields(text)
		switch fields[0] {
		case "go":
			if len(fields) >= 2 {
				c.add(layerLanguage, "Go", fields[1], file, "go", line)
			}
		case "toolchain":
			if len(fields) >= 2 {
				c.add(layerRuntime, "Go toolchain", fields[1], file, "toolchain", line)
			}
		case "require":
			rest := strings.TrimSpace(strings.TrimPrefix(text, "require"))
			if rest == "(" {
				inRequire = true
			} else if rest != "" {
				goRequire(c, file, rest, line)
			}
		}
	}
}

func goRequire(c *ctx, file, text string, line int) {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return
	}
	mod := strings.Trim(fields[0], "\"`")
	c.add(layerDependency, mod, fields[1], file, mod, line)
}

// stripSlashComment drops a // comment that starts the line or follows white space.
func stripSlashComment(s string) string {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '/' && s[i+1] == '/' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return s[:i]
		}
	}
	return s
}
