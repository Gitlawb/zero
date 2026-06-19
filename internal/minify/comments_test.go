package minify

import (
	"strings"
	"testing"
)

func mustContain(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("expected to CONTAIN %q in:\n%s", sub, s)
		}
	}
}
func mustNotContain(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			t.Errorf("expected NOT to contain %q in:\n%s", sub, s)
		}
	}
}

func TestStripC(t *testing.T) {
	src := "int x = 1; // DROPLINE\n" +
		"/* DROPBLOCK\n   spanning */\n" +
		"int y = 2;\n" +
		"char *u = \"http://e.com/a//b\"; // DROPURLNOTE\n" +
		"char c = '\\''; /* DROPCHARNOTE */\n"
	r := File("f.c", []byte(src))
	if !r.Applied || r.Language != "c" {
		t.Fatalf("got %+v", r)
	}
	mustNotContain(t, r.Content, "DROPLINE", "DROPBLOCK", "spanning", "DROPURLNOTE", "DROPCHARNOTE")
	mustContain(t, r.Content, "int x = 1;", "int y = 2;", `"http://e.com/a//b"`, `'\''`)
}

func TestStripCSSHasNoLineComments(t *testing.T) {
	// CSS: only /* */ are comments. A //-looking value must survive untouched.
	src := "a { color: red; /* DROPBLOCK */ }\n" +
		"b { background: url(http://x//y); }\n" +
		"/* DROPWHOLE */\n" +
		"c { content: \"/* KEEPSTR */\"; }\n"
	r := File("s.css", []byte(src))
	mustNotContain(t, r.Content, "DROPBLOCK", "DROPWHOLE")
	mustContain(t, r.Content, "color: red;", "url(http://x//y)", `"/* KEEPSTR */"`)
}

func TestStripSCSSKeepsInterpolationAndHex(t *testing.T) {
	src := "$c: #fff; // DROPLINE\n.x { color: #{$c}; } /* DROPBLOCK */\n"
	r := File("s.scss", []byte(src))
	mustNotContain(t, r.Content, "DROPLINE", "DROPBLOCK")
	mustContain(t, r.Content, "#fff", "#{$c}", "color:")
}

func TestStripJavaTextBlock(t *testing.T) {
	src := "class A {\n" +
		"  // DROPLINE\n" +
		"  String s = \"\"\"\n" +
		"    KEEP // this and /* this */ and \"\" too\n" +
		"    \"\"\";\n" +
		"  int x = 1; /* DROPBLOCK */\n" +
		"}\n"
	r := File("A.java", []byte(src))
	mustNotContain(t, r.Content, "DROPLINE", "DROPBLOCK")
	mustContain(t, r.Content, "KEEP // this and /* this */ and \"\" too", "int x = 1;")
}

func TestStripPython(t *testing.T) {
	src := "x = 1  # DROPCOMMENT\n" +
		"s = 'has # KEEPHASH inside'\n" +
		"t = \"\"\"\ntriple # KEEPTRIPLE\n\"\"\"\n" +
		"u = f\"{d['k']}# KEEPFSTR\"\n" +
		"r = r'raw\\# KEEPRAW'\n" +
		"# DROPLINE\n" +
		"y = 2\n"
	r := File("m.py", []byte(src))
	if !r.Applied || r.Language != "python" {
		t.Fatalf("got %+v", r)
	}
	mustNotContain(t, r.Content, "DROPCOMMENT", "DROPLINE")
	mustContain(t, r.Content, "x = 1", "KEEPHASH", "KEEPTRIPLE", "KEEPFSTR", "KEEPRAW", "y = 2")
}

func TestStripPythonFStringSameQuoteNesting(t *testing.T) {
	// Python 3.12 same-quote-nested f-string with a # inside must be preserved.
	src := "v = f\"{d[\"k\"]}# KEEPINNER\"\nz = 3  # DROPREAL\n"
	r := File("m.py", []byte(src))
	mustNotContain(t, r.Content, "DROPREAL")
	mustContain(t, r.Content, "# KEEPINNER", "z = 3")
}

func TestUnsupportedLangFallsBackToWhitespaceOnly(t *testing.T) {
	// JS/TS/C++/Rust/shell are intentionally NOT comment-stripped (hazards).
	for _, f := range []string{"a.js", "a.ts", "a.cpp", "a.rs", "a.sh"} {
		r := File(f, []byte("x // KEEPJSCOMMENT\n\n\n y\n"))
		if r.Applied {
			t.Errorf("%s: expected whitespace-only (Applied=false), got %+v", f, r)
		}
		if !strings.Contains(r.Content, "KEEPJSCOMMENT") {
			t.Errorf("%s: comment must be preserved for unsupported langs:\n%s", f, r.Content)
		}
	}
}
