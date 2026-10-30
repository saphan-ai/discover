package stack

import (
	"bytes"
	"encoding/xml"
	"io"
	"regexp"
	"strconv"
	"strings"
)

func itoa(n int) string { return strconv.Itoa(n) }

// xnode is an element with its text and the line it starts on.
type xnode struct {
	name string
	text string
	line int
	kids []*xnode
}

func (n *xnode) child(name string) *xnode {
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
	}
	return nil
}

func (n *xnode) childText(name string) (string, bool) {
	if k := n.child(name); k != nil {
		return strings.TrimSpace(k.text), true
	}
	return "", false
}

func parseXML(b []byte) (*xnode, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	src := string(b)
	var stack []*xnode
	var root *xnode
	for {
		off := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			// The offset before the token is the end of the previous one; the element starts at the next "<".
			at := off + strings.Index(src[off:], "<")
			n := &xnode{name: t.Name.Local, line: lineAt(src, at)}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(t)
			}
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, io.ErrUnexpectedEOF
	}
	return root, nil
}

// pom reads a Maven pom.xml: the Java level its properties set, the parent, the dependencies and the plugins. A
// version written as a property reference is reported as written, and a dependency without a version is not pinned
// here.
func pom(c *ctx, file string) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	root, err := parseXML(b)
	if err != nil || root.name != "project" {
		c.refuse(file, "not a valid Maven project file")
		return
	}
	artifact := func(n *xnode, layer string) {
		a, ok := n.childText("artifactId")
		if !ok || a == "" {
			return
		}
		tech := a
		if g, ok := n.childText("groupId"); ok && g != "" {
			tech = g + ":" + a
		}
		v, _ := n.childText("version")
		l := layer
		if s, _ := n.childText("scope"); s == "test" && layer == layerDependency {
			l = layerDevDependency
		}
		c.add(l, tech, v, file, a, n.child("artifactId").line)
	}
	if props := root.child("properties"); props != nil {
		for _, k := range props.kids {
			switch k.name {
			case "java.version", "maven.compiler.source", "maven.compiler.target", "maven.compiler.release":
				c.add(layerLanguage, "Java", strings.TrimSpace(k.text), file, k.name, k.line)
			}
		}
	}
	if p := root.child("parent"); p != nil {
		artifact(p, layerBuild)
	}
	deps := func(n *xnode) {
		if d := n.child("dependencies"); d != nil {
			for _, k := range d.kids {
				if k.name == "dependency" {
					artifact(k, layerDependency)
				}
			}
		}
	}
	deps(root)
	if dm := root.child("dependencyManagement"); dm != nil {
		deps(dm)
	}
	plugins := func(n *xnode) {
		if d := n.child("plugins"); d != nil {
			for _, k := range d.kids {
				if k.name == "plugin" {
					artifact(k, layerBuild)
				}
			}
		}
	}
	if build := root.child("build"); build != nil {
		plugins(build)
		if pm := build.child("pluginManagement"); pm != nil {
			plugins(pm)
		}
	}
}

var (
	gradleDepString = regexp.MustCompile(`^\s*([A-Za-z][A-Za-z0-9]*)\s*\(?\s*(?:(?:enforcedPlatform|platform)\s*\(\s*)?['"]([^'":\s$]+):([^'":\s$]+)(?::([^'"\s]+))?['"]`)
	gradleDepMap    = regexp.MustCompile(`^\s*([A-Za-z][A-Za-z0-9]*)\s*\(?\s*group\s*[:=]\s*['"]([^'"]+)['"]\s*,\s*name\s*[:=]\s*['"]([^'"]+)['"](?:\s*,\s*version\s*[:=]\s*['"]([^'"]*)['"])?`)
	gradlePluginID  = regexp.MustCompile(`^\s*id\s*\(?\s*['"]([^'"]+)['"]\s*\)?(?:\s*version\s*\(?\s*['"]([^'"]*)['"])?`)
	gradleKotlin    = regexp.MustCompile(`^\s*kotlin\(\s*"([^"]+)"\s*\)(?:\s*version\s*"([^"]*)")?`)
	gradleJava      = regexp.MustCompile(`^\s*(sourceCompatibility|targetCompatibility)\s*=\s*(\S.*?)\s*$`)
	gradleToolchain = regexp.MustCompile(`(jvmToolchain)\s*\(\s*(\d+)\s*\)|(JavaLanguageVersion\.of)\s*\(\s*(\d+)\s*\)`)
)

// gradleConfigs are the dependency configurations whose string notation names a module.
func gradleConfig(name string) (layer string, ok bool) {
	lower := strings.ToLower(name)
	switch {
	case name == "classpath":
		return layerBuild, true
	case strings.HasPrefix(lower, "test") || strings.HasPrefix(lower, "androidtest"):
		return layerDevDependency, true
	case name == "implementation" || name == "api" || name == "compileOnly" || name == "runtimeOnly" ||
		name == "annotationProcessor" || name == "kapt" || name == "ksp" || name == "developmentOnly" ||
		name == "compile" || name == "runtime" || name == "compileOnlyApi" ||
		strings.HasSuffix(name, "Implementation") || strings.HasSuffix(name, "Api") ||
		strings.HasSuffix(name, "CompileOnly") || strings.HasSuffix(name, "RuntimeOnly"):
		return layerDependency, true
	}
	return "", false
}

// gradle reads the plugins, Java level and string- or map-notation dependencies of a build.gradle(.kts). It reads
// lines; a dependency declared any other way is not read.
func gradle(c *ctx, file string) {
	b, ok := c.load(file)
	if !ok {
		return
	}
	inBlock := false
	for i, raw := range strings.Split(string(b), "\n") {
		line := i + 1
		l := raw
		if inBlock {
			j := strings.Index(l, "*/")
			if j < 0 {
				continue
			}
			l, inBlock = l[j+2:], false
		}
		if j := strings.Index(l, "/*"); j >= 0 {
			if k := strings.Index(l[j:], "*/"); k >= 0 {
				l = l[:j] + l[j+k+2:]
			} else {
				l, inBlock = l[:j], true
			}
		}
		l = stripSlashComment(l)
		if strings.TrimSpace(l) == "" {
			continue
		}
		if m := gradleDepString.FindStringSubmatch(l); m != nil {
			if layer, ok := gradleConfig(m[1]); ok {
				c.add(layer, m[2]+":"+m[3], m[4], file, m[2]+":"+m[3], line)
				continue
			}
		}
		if m := gradleDepMap.FindStringSubmatch(l); m != nil {
			if layer, ok := gradleConfig(m[1]); ok {
				c.add(layer, m[2]+":"+m[3], m[4], file, m[3], line)
				continue
			}
		}
		if m := gradleKotlin.FindStringSubmatch(l); m != nil {
			c.add(layerBuild, "org.jetbrains.kotlin."+m[1], m[2], file, m[1], line)
			continue
		}
		if m := gradlePluginID.FindStringSubmatch(l); m != nil {
			c.add(layerBuild, m[1], m[2], file, m[1], line)
			continue
		}
		if m := gradleJava.FindStringSubmatch(l); m != nil {
			c.add(layerLanguage, "Java", strings.Trim(m[2], `'"`), file, m[1], line)
			continue
		}
		if m := gradleToolchain.FindStringSubmatch(l); m != nil {
			key, v := m[1], m[2]
			if key == "" {
				key, v = m[3], m[4]
			}
			c.add(layerLanguage, "Java", v, file, key, line)
		}
	}
}
