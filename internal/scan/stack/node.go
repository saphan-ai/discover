package stack

import (
	"encoding/json"
	"path"
	"sort"
	"strings"
)

// jsonObject decodes b as one JSON object.
func jsonObject(b []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errNotObject
	}
	return m, nil
}

type stringError string

func (e stringError) Error() string { return string(e) }

const errNotObject = stringError("not a JSON object")

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// stringMap is the string-valued entries of m[name], sorted by key, and the keys whose value is not a string.
func stringMap(m map[string]any, name string) (keys []string, vals map[string]string, other []string) {
	obj, _ := m[name].(map[string]any)
	vals = map[string]string{}
	for _, k := range sortedKeys(obj) {
		if s, ok := obj[k].(string); ok {
			keys = append(keys, k)
			vals[k] = s
		} else {
			other = append(other, k)
		}
	}
	return keys, vals, other
}

// packageJSON reads package.json and, beside it, package-lock.json.
func packageJSON(c *ctx, file string) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	m, err := jsonObject(b)
	if err != nil {
		c.refuse(file, "not valid JSON")
		return
	}
	src := string(b)
	declared := map[string]bool{}
	var notStrings []string

	if pm, ok := m["packageManager"].(string); ok {
		name, version, _ := strings.Cut(pm, "@")
		if name != "" {
			c.add(layerPackageManager, name, version, file, "packageManager", jsonLine(src, "", "packageManager"))
		}
	}
	keys, vals, other := stringMap(m, "engines")
	notStrings = append(notStrings, other...)
	for _, k := range keys {
		tech := k
		if k == "node" {
			tech = "Node.js"
		}
		c.add(layerRuntime, tech, vals[k], file, k, jsonLine(src, "engines", k))
	}
	for _, sec := range []struct{ name, layer string }{
		{"dependencies", layerDependency},
		{"devDependencies", layerDevDependency},
		{"peerDependencies", layerDependency},
		{"optionalDependencies", layerOptional},
	} {
		keys, vals, other := stringMap(m, sec.name)
		notStrings = append(notStrings, other...)
		for _, k := range keys {
			declared[k] = true
			c.add(sec.layer, k, vals[k], file, k, jsonLine(src, sec.name, k))
		}
	}
	if len(notStrings) > 0 {
		sort.Strings(notStrings)
		c.refuse(file, "values that are not strings: "+strings.Join(notStrings, ", "))
	}

	dir := path.Dir(file)
	lock := path.Join(dir, "package-lock.json")
	if c.files[lock] {
		packageLock(c, lock, declared)
	}
}

// packageLock reads the resolved version of each package that package.json declares. Packages the lockfile holds
// for other reasons are not rows.
func packageLock(c *ctx, file string, declared map[string]bool) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	m, err := jsonObject(b)
	if err != nil {
		c.refuse(file, "not valid JSON")
		return
	}
	src := string(b)
	packages, _ := m["packages"].(map[string]any)
	legacy, _ := m["dependencies"].(map[string]any)
	names := make([]string, 0, len(declared))
	for n := range declared {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if e, ok := packages["node_modules/"+n].(map[string]any); ok {
			if v, ok := e["version"].(string); ok {
				c.add(layerLocked, n, v, file, "node_modules/"+n, jsonLine(src, "packages", "node_modules/"+n))
			}
		} else if e, ok := legacy[n].(map[string]any); ok && packages == nil {
			if v, ok := e["version"].(string); ok {
				c.add(layerLocked, n, v, file, n, jsonLine(src, "dependencies", n))
			}
		}
	}
}

// composer reads composer.json.
func composer(c *ctx, file string) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	m, err := jsonObject(b)
	if err != nil {
		c.refuse(file, "not valid JSON")
		return
	}
	src := string(b)
	var notStrings []string
	for _, sec := range []struct{ name, layer string }{{"require", layerDependency}, {"require-dev", layerDevDependency}} {
		keys, vals, other := stringMap(m, sec.name)
		notStrings = append(notStrings, other...)
		for _, k := range keys {
			layer, tech := sec.layer, k
			if k == "php" {
				layer, tech = layerLanguage, "PHP"
			}
			c.add(layer, tech, vals[k], file, k, jsonLine(src, sec.name, k))
		}
	}
	if len(notStrings) > 0 {
		sort.Strings(notStrings)
		c.refuse(file, "values that are not strings: "+strings.Join(notStrings, ", "))
	}
}

// globalJSON reads the .NET SDK pin of a global.json.
func globalJSON(c *ctx, file string) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	m, err := jsonObject(b)
	if err != nil {
		c.refuse(file, "not valid JSON")
		return
	}
	src := string(b)
	if sdk, ok := m["sdk"].(map[string]any); ok {
		v, _ := sdk["version"].(string)
		key := "version"
		line := jsonLine(src, "sdk", "version")
		if _, has := sdk["version"]; !has {
			key, line = "sdk", jsonLine(src, "", "sdk")
		}
		c.add(layerRuntime, ".NET SDK", v, file, key, line)
	}
	keys, vals, _ := stringMap(m, "msbuild-sdks")
	for _, k := range keys {
		c.add(layerBuild, k, vals[k], file, k, jsonLine(src, "msbuild-sdks", k))
	}
}
