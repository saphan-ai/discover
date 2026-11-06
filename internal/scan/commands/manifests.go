package commands

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

type keyLine struct {
	key  string
	line int
}

// objectKeys lists the keys of the JSON object in raw, each with the line it sits on in the whole file. base is the
// offset of raw in data.
func objectKeys(data, raw []byte, base int) ([]keyLine, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	var out []keyLine
	for dec.More() {
		t, err := dec.Token()
		key, ok := t.(string)
		if err != nil || !ok {
			return nil, false
		}
		out = append(out, keyLine{key, lineAt(data, base+int(dec.InputOffset()))})
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return nil, false
		}
	}
	return out, true
}

// packageJSON cites the scripts and the bin field. JSON that does not parse is refused by name.
func (s *scanner) packageJSON(p string, _ []string, data []byte) bool {
	if !json.Valid(data) {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return false
	}
	var name string
	var scripts, bins []keyLine
	binLine := 0
	var binRaw json.RawMessage
	for dec.More() {
		t, err := dec.Token()
		key, ok := t.(string)
		if err != nil || !ok {
			return false
		}
		keyLn := lineAt(data, int(dec.InputOffset()))
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return false
		}
		start := int(dec.InputOffset()) - len(raw)
		switch key {
		case "name":
			_ = json.Unmarshal(raw, &name)
		case "scripts":
			scripts, _ = objectKeys(data, raw, start)
		case "bin":
			binLine, binRaw = keyLn, raw
			bins, _ = objectKeys(data, raw, start)
		}
	}
	for _, k := range scripts {
		s.command(classify(k.key), "npm run "+k.key, p, k.line)
	}
	if bins != nil {
		for _, k := range bins {
			s.entry("cli", k.key, p, k.line)
		}
	} else if binLine > 0 && len(binRaw) > 0 && binRaw[0] == '"' {
		n := name
		if i := strings.LastIndexByte(n, '/'); i >= 0 {
			n = n[i+1:]
		}
		if n != "" {
			s.entry("cli", n, p, binLine)
		}
	}
	return true
}

var (
	tomlTable = regexp.MustCompile(`^\s*\[([^\[\]]+)\]\s*(?:#.*)?$`)
	tomlArray = regexp.MustCompile(`^\s*\[\[([^\[\]]+)\]\]\s*(?:#.*)?$`)
	tomlKey   = regexp.MustCompile(`^\s*(?:"([^"]+)"|'([^']+)'|([A-Za-z0-9_.-]+))\s*=`)
	tomlName  = regexp.MustCompile(`^\s*name\s*=\s*["']([^"']*)["']`)
)

func tomlKeyName(l string) string {
	m := tomlKey.FindStringSubmatch(l)
	if m == nil {
		return ""
	}
	return m[1] + m[2] + m[3]
}

func noSpace(s string) string { return strings.Join(strings.Fields(s), "") }

// pyproject cites the console scripts of [project.scripts] and [tool.poetry.scripts]: each is an entry point and the
// command a user types once the package is installed.
func (s *scanner) pyproject(p string, lines []string, _ []byte) bool {
	table := ""
	for i, l := range lines {
		if m := tomlArray.FindStringSubmatch(l); m != nil {
			table = "[[" + noSpace(m[1]) + "]]"
			continue
		}
		if m := tomlTable.FindStringSubmatch(l); m != nil {
			table = noSpace(m[1])
			continue
		}
		if table != "project.scripts" && table != "tool.poetry.scripts" {
			continue
		}
		if name := tomlKeyName(l); name != "" {
			s.entry("cli", name, p, i+1)
			s.command("run", name, p, i+1)
		}
	}
	return true
}

// cargo cites each [[bin]] table by the name it gives its binary.
func (s *scanner) cargo(p string, lines []string, _ []byte) bool {
	start, name := 0, ""
	flush := func() {
		if start > 0 && name != "" {
			s.entry("cli", name, p, start)
		}
		start, name = 0, ""
	}
	for i, l := range lines {
		if tomlTable.MatchString(l) || tomlArray.MatchString(l) {
			flush()
			if m := tomlArray.FindStringSubmatch(l); m != nil && noSpace(m[1]) == "bin" {
				start = i + 1
			}
			continue
		}
		if m := tomlName.FindStringSubmatch(l); m != nil && start > 0 && name == "" {
			name = m[1]
		}
	}
	flush()
	return true
}
