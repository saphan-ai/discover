package stack

import (
	"regexp"
	"strings"
)

// dockerfile reads the FROM lines of a Dockerfile. A stage that builds on an earlier stage, "scratch" and an image
// named by a variable are not rows; a line with a variable or a URL for an image is listed as skipped.
func dockerfile(c *ctx, file string) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	lines := strings.Split(string(b), "\n")
	stages := map[string]bool{}
	var bad []string
	for i := 0; i < len(lines); i++ {
		start := i + 1
		l := strings.TrimRight(lines[i], "\r")
		for strings.HasSuffix(l, "\\") && i+1 < len(lines) {
			i++
			l = strings.TrimSuffix(l, "\\") + " " + strings.TrimRight(lines[i], "\r")
		}
		fields := strings.Fields(l)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
			continue
		}
		instr, args := fields[0], fields[1:]
		for len(args) > 0 && strings.HasPrefix(args[0], "--") {
			args = args[1:]
		}
		if len(args) == 0 {
			continue
		}
		image := args[0]
		alias := ""
		if len(args) >= 3 && strings.EqualFold(args[1], "AS") {
			alias = strings.ToLower(args[2])
		}
		switch {
		case stages[strings.ToLower(image)] || strings.EqualFold(image, "scratch"):
		case strings.HasPrefix(image, "$") || strings.Contains(image, "://"):
			bad = append(bad, itoa(start))
		default:
			name, version := splitImage(image)
			c.add(layerContainer, name, version, file, instr, start)
		}
		if alias != "" {
			stages[alias] = true
		}
	}
	if len(bad) > 0 {
		c.skip(file, "FROM lines not read (image named by a variable or a URL): lines "+strings.Join(bad, ", "))
	}
}

// splitImage splits an image reference into its name and the tag, or the digest when there is no tag.
func splitImage(ref string) (name, version string) {
	digest := ""
	if i := strings.Index(ref, "@"); i >= 0 {
		ref, digest = ref[:i], ref[i+1:]
	}
	slash := strings.LastIndex(ref, "/")
	tag := ""
	if i := strings.LastIndex(ref, ":"); i > slash {
		ref, tag = ref[:i], ref[i+1:]
	}
	if tag != "" {
		return ref, tag
	}
	return ref, digest
}

// toolVersions reads a .tool-versions: a tool name, then one or more versions.
func toolVersions(c *ctx, file string) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	for i, l := range strings.Split(string(b), "\n") {
		if j := strings.Index(l, "#"); j >= 0 {
			l = l[:j]
		}
		fields := strings.Fields(l)
		if len(fields) == 0 {
			continue
		}
		c.add(layerTooling, fields[0], strings.Join(fields[1:], " "), file, fields[0], i+1)
	}
}

// nvmrc reads the first line of a .nvmrc. The aliases that float to whatever is newest pin nothing.
func nvmrc(c *ctx, file string) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	for i, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		switch strings.ToLower(l) {
		case "node", "stable", "latest", "system", "default":
			l = ""
		}
		c.add(layerRuntime, "Node.js", l, file, "", i+1)
		return
	}
}

var gemLine = regexp.MustCompile(`^\s*gem\s+(?:'([^']+)'|"([^"]+)")((?:\s*,\s*(?:'[^']*'|"[^"]*"))*)`)
var rubyLine = regexp.MustCompile(`^\s*ruby\s+(?:'([^']*)'|"([^"]*)")`)
var quoted = regexp.MustCompile(`'([^']*)'|"([^"]*)"`)

func either(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// gemfile reads the gem and ruby lines of a Gemfile. The quoted arguments that follow a gem's name are its version
// constraints; options such as git: or require: are not.
func gemfile(c *ctx, file string) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	for i, l := range strings.Split(string(b), "\n") {
		if m := rubyLine.FindStringSubmatch(l); m != nil {
			c.add(layerLanguage, "Ruby", either(m[1], m[2]), file, "ruby", i+1)
			continue
		}
		m := gemLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		var cons []string
		for _, q := range quoted.FindAllStringSubmatch(m[3], -1) {
			cons = append(cons, either(q[1], q[2]))
		}
		name := either(m[1], m[2])
		c.add(layerDependency, name, strings.Join(cons, ", "), file, name, i+1)
	}
}
