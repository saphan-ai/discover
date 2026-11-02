package report

import (
	"strings"
	"testing"
)

// Hidden runes are built from their code points, so this file holds none of them raw.
var (
	rlo  = string(rune(0x202e)) // RIGHT-TO-LEFT OVERRIDE
	zwsp = string(rune(0x200b)) // ZERO WIDTH SPACE
)

func TestTextEscapingLeavesNoMarkdownSyntax(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"plain words", "plain words"},
		{"# heading", `\# heading`},
		{"[click](https://evil.example)", `\[click\]\(https\://evil.example\)`},
		{"<script>alert(1)</script>", `\<script\>alert\(1\)\</script\>`},
		{"a|b", `a\|b`},
		{"`code`", "\\`code\\`"},
		{`back\slash`, `back\\slash`},
		{"*em* _em_ ~~del~~ $x$ ![i](j) &amp; @user", `\*em\* \_em\_ \~\~del\~\~ \$x\$ \!\[i\]\(j\) \&amp; \@user`},
		{"see www.evil.example and WWW.x", `see www\.evil.example and WWW\.x`},
		{"line\nbreak", `line\<U+000A\>break`},
		{"bidi" + rlo + "right" + zwsp + "zero", `bidi\<U+202E\>right\<U+200B\>zero`},
		{"1.2-beta+3", "1.2-beta+3"},
	}
	for _, c := range cases {
		if got := text(c.in); got != c.want {
			t.Errorf("text(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCodeSpanCannotBeClosedByItsContent(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"go build ./...", "`go build ./...`"},
		{"a`b", "``a`b``"},
		{"`x`", "`` `x` ``"},
		{"echo ``id``", "``` echo ``id`` ```"},
		{"a|b", "`a\\|b`"},
		{"a\nb", "`a<U+000A>b`"},
		{"key" + rlo, "`key<U+202E>`"},
		{"<script>", "`<script>`"},
	}
	for _, c := range cases {
		if got := code(c.in); got != c.want {
			t.Errorf("code(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNoHiddenCharacterSurvivesEscaping(t *testing.T) {
	t.Parallel()
	hidden := []rune{0x00, 0x07, 0x1b, 0x7f, 0x200b, 0x200c, 0x200d, 0x200e, 0x200f, 0x202a, 0x202e, 0x2066, 0x2069, 0x2028, 0x2029, 0xfeff, 0x061c, 0xe0041}
	for _, r := range hidden {
		in := "a" + string(r) + "b"
		for name, f := range map[string]func(string) string{"text": text, "code": code, "yaml": yamlString} {
			if out := f(in); strings.ContainsRune(out, r) {
				t.Errorf("%s(%q) still holds U+%04X: %q", name, in, r, out)
			}
		}
	}
}

func TestYAMLStringIsOneQuotedScalar(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"shop", `"shop"`},
		{`say "hi" \ bye`, `"say \"hi\" \\ bye"`},
		{"two\nlines", `"two\` + `u000Alines"`},
		{"x" + rlo, `"x\` + `u202E"`},
		{"x" + string(rune(0xe0041)), `"x\` + `U000E0041"`},
	}
	for _, c := range cases {
		if got := yamlString(c.in); got != c.want {
			t.Errorf("yamlString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
