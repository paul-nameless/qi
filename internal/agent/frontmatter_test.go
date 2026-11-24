package agent

import (
	"strings"
	"testing"
)

func norm(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func TestParseFrontmatterScalars(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, name, desc string
		hidden         bool
	}{
		{"name: a\ndescription: plain text # comment\n", "a", "plain text", false},
		{"description: C# and F# code\n", "", "C# and F# code", false},
		{"name: 'it''s'\n", "it's", "", false},
		{"description: \"tab\\there \\u00e9 \\\"q\\\" \\\\ end\"\n", "", "tab\there é \"q\" \\ end", false},
		{"name: \"quoted\"   # comment\n", "quoted", "", false},
		{"description: \"Mia\" is the phone # c\n", "", "\"Mia\" is the phone", false},
		{"name: \"\\U0001F528 \\x41\"\n", "\U0001F528 A", "", false},
		{"description: |\n  line one\n  line two\n\n", "", "line one\nline two\n", false},
		{"description: |-\n  line one\n  line two\n\n", "", "line one\nline two", false},
		{"description: |+\n  line one\n  line two\n\n\n", "", "line one\nline two\n\n", false},
		{"description: |\n    indented four\n      deeper\n    back\n", "", "indented four\n  deeper\nback", false},
		{"description: >\n  folded one\n  folded two\n\n  para two\n    more indented\n  back\n", "", "folded one folded two\npara two\n  more indented\nback", false},
		{"description: >-\n  a\n  b\n", "", "a b", false},
		{"description: >+\n  a\n  b\n\n", "", "a b\n", false},
		{"description: >2\n   x\n  y\n", "", " x\ny", false},
		{"description: >\n  # not a comment\n  text\n", "", "# not a comment text", false},
		{"description: multi\n  line plain\n\n  scalar\n", "", "multi line plain\nscalar", false},
		{"description:\n  next line plain\n  continues\n", "", "next line plain continues", false},
		{"description:\n  \"quoted on the\n  next line\"\n", "", "quoted on the next line", false},
		{"description: \"multi\n  line\n\n  quoted\"\n", "", "multi line\nquoted", false},
		{"description: \"escaped \\\n   break\"\n", "", "escaped break", false},
		{"name: x\nmetadata:\n  author: me\n  tags: [a, b]\ndescription: after nested\n", "x", "after nested", false},
		{"name: x\nallowed-tools:\n  - Read\n  - Bash\ndescription: after list\n", "x", "after list", false},
		{"name: x\nmetadata:\n  author: me\n", "x", "", false},
		{"description: ~\n", "", "", false},
		{"description: text\n# comment line\nname: n\n", "n", "text", false},
		{"name: n\ndescription: Use when: something\n", "n", "Use when: something", false},
		{"\"name\": qk\n", "qk", "", false},
		{"disable-model-invocation: true\n", "", "", true},
		{"disable-model-invocation: True\n", "", "", true},
		{"disable-model-invocation: false\n", "", "", false},
		{"name: n\nunknown-key: [1, 2]\nargument-hint: <x>\n", "n", "", false},
	}
	for _, c := range cases {
		m, body := ParseFrontmatter("---\n" + c.in + "---\nbody\n")
		if m.Name != norm(c.name) || m.Description != norm(c.desc) || m.DisableModelInvocation != c.hidden || body != "body\n" {
			t.Errorf("%q: got %q %q %v body %q", c.in, m.Name, m.Description, m.DisableModelInvocation, body)
		}
	}
}

func TestParseFrontmatterWithoutBlock(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"no frontmatter\n", "---\nname: x\nno end\n"} {
		if m, body := ParseFrontmatter(in); m != (skillMeta{}) || body != in {
			t.Errorf("%q: got %+v %q", in, m, body)
		}
	}
	if m, body := ParseFrontmatter("---\r\nname: crlf\r\n---\r\nbody"); m.Name != "crlf" || body != "body" {
		t.Errorf("crlf: %+v %q", m, body)
	}
}
