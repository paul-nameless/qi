package regex

import (
	"regexp"
	"strings"
	"testing"
)

var gateText = strings.Repeat("lorem ipsum dolor ", 50000)

func allMatches(re *Regexp, s string) int {
	n := 0
	var buf []int
	for pos := 0; pos <= len(s); {
		buf = re.ExecBytesTo(buf, s, pos)
		if buf == nil {
			break
		}
		n++
		if buf[1] == buf[0] {
			pos = buf[1] + 1
		} else {
			pos = buf[1]
		}
	}
	return n
}

func replaceAll(re *Regexp, s, repl string) string {
	var b strings.Builder
	last := 0
	for pos := 0; pos <= len(s); {
		m := re.ExecBytes(s, pos)
		if m == nil {
			break
		}
		b.WriteString(s[last:m[0]])
		b.WriteString(repl)
		last = m[1]
		if m[1] == m[0] {
			pos = m[1] + 1
		} else {
			pos = m[1]
		}
	}
	b.WriteString(s[last:])
	return b.String()
}

func benchGate(b *testing.B, eng int) {
	words, err := CompileEngine(`\b\w+\b`, "g", eng)
	if err != nil {
		b.Fatal(err)
	}
	look, err := CompileEngine(`o(?=r)`, "g", eng)
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if n := allMatches(words, gateText); n != 150000 {
			b.Fatalf("words: %d", n)
		}
		if out := replaceAll(look, gateText, "0"); len(out) != len(gateText) {
			b.Fatal("replace")
		}
		if n := len(strings.Split(gateText, " ")); n != 150001 {
			b.Fatalf("split: %d", n)
		}
	}
}

func BenchmarkGateAuto(b *testing.B)      { benchGate(b, EngineAuto) }
func BenchmarkGateBacktrack(b *testing.B) { benchGate(b, EngineBacktrack) }
func BenchmarkGateRE2(b *testing.B)       { benchGate(b, EngineRE2) }

func BenchmarkGateWordsBacktrack(b *testing.B) {
	re, _ := CompileEngine(`\b\w+\b`, "g", EngineBacktrack)
	for b.Loop() {
		allMatches(re, gateText)
	}
}

func BenchmarkGateLookBacktrack(b *testing.B) {
	re, _ := CompileEngine(`o(?=r)`, "g", EngineBacktrack)
	for b.Loop() {
		replaceAll(re, gateText, "0")
	}
}

func BenchmarkGateWordsGoRegexp(b *testing.B) {
	re := regexp.MustCompile(`\b\w+\b`)
	for b.Loop() {
		re.FindAllStringIndex(gateText, -1)
	}
}

var goSource = func() string {
	unit := `package server

import (
	"context"
	"fmt"
	"net/http"
)

// Handler serves requests for the API.
type Handler struct {
	mux  *http.ServeMux
	name string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := h.handle(ctx, w, r); err != nil {
		http.Error(w, fmt.Sprintf("handler %q failed: %v", h.name, err), 500)
		return
	}
}

// TODO: add metrics
func NewHandler(name string) *Handler {
	return &Handler{mux: http.NewServeMux(), name: name}
}

func (h *Handler) handle(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	switch r.Method {
	case "GET":
		_, err := fmt.Fprintf(w, "hello %s\n", "world \"quoted\"")
		return err
	default:
		return fmt.Errorf("unsupported method %s", r.Method)
	}
}
`
	var b strings.Builder
	for b.Len() < 1<<20 {
		b.WriteString(unit)
	}
	return b.String()
}()

type typical struct {
	name, js, flags, goExpr string
}

var typicalPatterns = []typical{
	{"funcdecl", `^\s*func\s+(?:\([^\n)]*\)\s*)?([A-Za-z_]\w*)\s*\(`, "gm", `(?m)^[\t\n\v\f\r ]*func[\t\n\v\f\r ]+(?:\([^\n)]*\)[\t\n\v\f\r ]*)?([A-Za-z_]\w*)[\t\n\v\f\r ]*\(`},
	{"todo", `\b(?:TODO|FIXME)\b.*`, "g", `\b(?:TODO|FIXME)\b[^\n\r\x{2028}\x{2029}]*`},
	{"string", `"(?:[^"\\]|\\.)*"`, "g", `"(?:[^"\\]|\\[^\n\r\x{2028}\x{2029}])*"`},
	{"errcheck", `\berr\s*!=\s*nil\b`, "g", `\berr[\t\n\v\f\r ]*!=[\t\n\v\f\r ]*nil\b`},
	{"ident", `\b[A-Z]\w*\(`, "g", `\b[A-Z]\w*\(`},
}

func BenchmarkTypical(b *testing.B) {
	for _, tp := range typicalPatterns {
		for _, eng := range []struct {
			name string
			id   int
		}{{"backtrack", EngineBacktrack}, {"re2path", EngineRE2}} {
			re, err := CompileEngine(tp.js, tp.flags, eng.id)
			if err != nil {
				b.Fatal(err)
			}
			b.Run(tp.name+"/"+eng.name, func(b *testing.B) {
				for b.Loop() {
					allMatches(re, goSource)
				}
			})
		}
		gre := regexp.MustCompile(tp.goExpr)
		b.Run(tp.name+"/goregexp", func(b *testing.B) {
			for b.Loop() {
				gre.FindAllStringSubmatchIndex(goSource, -1)
			}
		})
	}
}

func TestTypicalAgree(t *testing.T) {
	for _, tp := range typicalPatterns {
		re, err := CompileEngine(tp.js, tp.flags, EngineBacktrack)
		if err != nil {
			t.Fatal(err)
		}
		want := len(regexp.MustCompile(tp.goExpr).FindAllStringIndex(goSource, -1))
		if got := allMatches(re, goSource); got != want {
			t.Errorf("%s: %d matches, go regexp %d", tp.name, got, want)
		}
	}
}

func BenchmarkIsASCII900K(b *testing.B) {
	for b.Loop() {
		if !IsASCII(gateText) {
			b.Fatal("ascii")
		}
	}
}

func BenchmarkCompile(b *testing.B) {
	for b.Loop() {
		if _, err := compile(`^\s*func\s+(?:\([^\n)]*\)\s*)?([A-Za-z_]\w*)\s*\(`, "gm", EngineAuto); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExecUTF16NonASCII(b *testing.B) {
	s := strings.Repeat("Привет мир, hello wörld! ", 40000)
	re, _ := Compile(`\w+`, "g")
	for b.Loop() {
		for pos := 0; ; {
			m := re.Exec(s, pos)
			if m == nil {
				break
			}
			pos = m[1]
		}
	}
}

func BenchmarkGateFreshSubjectEachRun(b *testing.B) {
	words, _ := Compile(`\b\w+\b`, "g")
	look, _ := Compile(`o(?=r)`, "g")
	for b.Loop() {
		b.StopTimer()
		s := strings.Repeat("lorem ipsum dolor ", 50000)
		b.StartTimer()
		allMatches(words, s)
		replaceAll(look, s, "0")
		strings.Split(s, " ")
	}
}

func BenchmarkGateSubjectAPI(b *testing.B) {
	words, _ := Compile(`\b\w+\b`, "g")
	look, _ := Compile(`o(?=r)`, "g")
	for b.Loop() {
		sub := NewSubject(strings.Clone(gateText))
		n := 0
		var buf []int
		for pos := 0; ; {
			if buf = words.ExecSubjectBytesTo(buf, sub, pos); buf == nil {
				break
			}
			n++
			pos = buf[1]
		}
		for pos := 0; ; {
			m := look.ExecSubjectBytes(sub, pos)
			if m == nil {
				break
			}
			pos = m[1]
		}
		strings.Split(sub.String(), " ")
	}
}
