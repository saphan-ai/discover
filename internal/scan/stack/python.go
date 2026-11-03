package stack

import (
	"fmt"
	"strings"
)

// depItem is one dependency a TOML manifest declares, gathered from the forms TOML allows: a key in a dependency
// table, a dotted key, or a table of its own.
type depItem struct {
	layer, name, version string
	line                 int
}

// collectDeps gathers dependencies. classify says, for a table path, the layer of its dependencies and whether the
// table is the dependency's own (its last segment is the name); ok is false for a table that holds none.
func collectDeps(entries []tentry, classify func(table []string) (layer string, own bool, ok bool)) []depItem {
	var items []*depItem
	index := map[string]*depItem{}
	for _, e := range entries {
		layer, own, ok := classify(e.table)
		if !ok {
			continue
		}
		var name, id string
		if own {
			name, id = e.table[len(e.table)-1], strings.Join(e.table, "\x00")
		} else {
			if len(e.key) == 0 {
				continue
			}
			name, id = e.key[0], strings.Join(e.table, "\x00")+"\x01"+e.key[0]
		}
		it := index[id]
		if it == nil {
			it = &depItem{layer: layer, name: name, line: e.line}
			index[id] = it
			items = append(items, it)
		}
		switch {
		case own && len(e.key) == 1 && e.key[0] == "version" && e.val.kind == 's':
			it.version = e.val.s
		case !own && len(e.key) == 1:
			it.version = depValue(e.val)
		case !own && len(e.key) == 2 && e.key[1] == "version" && e.val.kind == 's':
			it.version = e.val.s
		}
	}
	out := make([]depItem, len(items))
	for i, it := range items {
		out[i] = *it
	}
	return out
}

// depValue is the version a dependency value states: the string itself, or the version key of an inline table.
func depValue(v tval) string {
	switch v.kind {
	case 's':
		return v.s
	case 't':
		for _, e := range v.tab {
			if len(e.key) == 1 && e.key[0] == "version" && e.val.kind == 's' {
				return e.val.s
			}
		}
	}
	return ""
}

func tableIs(table []string, want ...string) bool {
	if len(table) != len(want) {
		return false
	}
	for i := range want {
		if table[i] != want[i] {
			return false
		}
	}
	return true
}

func keyIs(key []string, want string) bool { return len(key) == 1 && key[0] == want }

// pyproject reads PEP 621 metadata, dependency groups, build requirements and Poetry tables.
func pyproject(c *ctx, file string) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	entries, err := parseTOML(b)
	if err != nil {
		c.refuse(file, "not valid TOML, "+err.Error())
		return
	}
	reqs := func(e tentry, layer string) {
		for _, el := range e.val.arr {
			if el.kind != 's' {
				continue
			}
			if name, spec, ok := parseReq(el.s); ok {
				c.add(layer, name, spec, file, name, el.line)
			}
		}
	}
	for _, e := range entries {
		switch {
		case tableIs(e.table, "project") && keyIs(e.key, "requires-python") && e.val.kind == 's':
			c.add(layerLanguage, "Python", e.val.s, file, "requires-python", e.line)
		case tableIs(e.table, "project") && keyIs(e.key, "dependencies"):
			reqs(e, layerDependency)
		case tableIs(e.table, "project", "optional-dependencies") && len(e.key) == 1:
			reqs(e, layerOptional)
		case tableIs(e.table, "dependency-groups") && len(e.key) == 1:
			reqs(e, layerDevDependency)
		case tableIs(e.table, "build-system") && keyIs(e.key, "requires"):
			reqs(e, layerBuild)
		}
	}
	items := collectDeps(entries, func(t []string) (string, bool, bool) {
		if len(t) < 3 || t[0] != "tool" || t[1] != "poetry" {
			return "", false, false
		}
		rest := t[2:]
		layer := layerDependency
		var depAt int
		switch {
		case rest[0] == "dependencies":
			depAt = 0
		case rest[0] == "dev-dependencies":
			depAt, layer = 0, layerDevDependency
		case rest[0] == "group" && len(rest) >= 3 && rest[2] == "dependencies":
			depAt = 2
			if rest[1] != "main" {
				layer = layerDevDependency
			}
		default:
			return "", false, false
		}
		switch len(rest) - depAt {
		case 1:
			return layer, false, true
		case 2:
			return layer, true, true
		}
		return "", false, false
	})
	for _, it := range items {
		if it.name == "python" && it.layer == layerDependency {
			c.add(layerLanguage, "Python", it.version, file, "python", it.line)
			continue
		}
		c.add(it.layer, it.name, it.version, file, it.name, it.line)
	}
}

// parseReq splits a PEP 508 requirement into its name and what it says about the version: the specifier as written
// without spaces, or the URL of a direct reference. The environment marker is dropped.
func parseReq(s string) (name, spec string, ok bool) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ";"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if s == "" || !isAlnum(s[0]) {
		return "", "", false
	}
	i := 0
	for i < len(s) && (isAlnum(s[i]) || s[i] == '.' || s[i] == '_' || s[i] == '-') {
		i++
	}
	name = s[:i]
	rest := strings.TrimSpace(s[i:])
	if strings.HasPrefix(rest, "[") {
		j := strings.Index(rest, "]")
		if j < 0 {
			return "", "", false
		}
		rest = strings.TrimSpace(rest[j+1:])
	}
	switch {
	case strings.HasPrefix(rest, "@"):
		spec = strings.TrimSpace(rest[1:])
	case strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "/"):
		return "", "", false
	default:
		rest = strings.TrimSuffix(strings.TrimPrefix(rest, "("), ")")
		spec = strings.NewReplacer(" ", "", "\t", "").Replace(rest)
	}
	return name, spec, true
}

func isAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// requirements reads a requirements*.txt. A line that is an option, an editable install, an include or malformed is
// not read, and the file is listed as skipped with the line numbers.
func requirements(c *ctx, file string) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	lines := strings.Split(string(b), "\n")
	var bad []string
	for i := 0; i < len(lines); i++ {
		start := i + 1
		l := strings.TrimRight(lines[i], "\r")
		for strings.HasSuffix(l, "\\") && i+1 < len(lines) {
			i++
			l = strings.TrimSuffix(l, "\\") + " " + strings.TrimRight(lines[i], "\r")
		}
		if j := strings.Index(l, " #"); j >= 0 {
			l = l[:j]
		}
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if strings.HasPrefix(l, "-") {
			bad = append(bad, fmt.Sprint(start))
			continue
		}
		if j := strings.Index(l, " --"); j >= 0 {
			l = l[:j]
		}
		name, spec, ok := parseReq(l)
		if !ok {
			bad = append(bad, fmt.Sprint(start))
			continue
		}
		c.add(layerDependency, name, spec, file, name, start)
	}
	if len(bad) > 0 {
		c.skip(file, "requirements lines not read (options, editable or malformed): lines "+strings.Join(bad, ", "))
	}
}

// pythonVersion reads .python-version: one version per line.
func pythonVersion(c *ctx, file string) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	for i, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			c.add(layerRuntime, "Python", l, file, "", i+1)
		}
	}
}

// cargo reads Cargo.toml.
func cargo(c *ctx, file string) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	entries, err := parseTOML(b)
	if err != nil {
		c.refuse(file, "not valid TOML, "+err.Error())
		return
	}
	for _, e := range entries {
		if !(tableIs(e.table, "package") || tableIs(e.table, "workspace", "package")) || e.val.kind != 's' {
			continue
		}
		switch {
		case keyIs(e.key, "edition"):
			c.add(layerLanguage, "Rust edition", e.val.s, file, "edition", e.line)
		case keyIs(e.key, "rust-version"):
			c.add(layerLanguage, "Rust", e.val.s, file, "rust-version", e.line)
		}
	}
	items := collectDeps(entries, func(t []string) (string, bool, bool) {
		for i := len(t) - 1; i >= 0; i-- {
			var layer string
			switch t[i] {
			case "dependencies":
				layer = layerDependency
			case "dev-dependencies":
				layer = layerDevDependency
			case "build-dependencies":
				layer = layerBuild
			default:
				continue
			}
			switch len(t) - 1 - i {
			case 0:
				return layer, false, true
			case 1:
				return layer, true, true
			}
			return "", false, false
		}
		return "", false, false
	})
	for _, it := range items {
		c.add(it.layer, it.name, it.version, file, it.name, it.line)
	}
}
