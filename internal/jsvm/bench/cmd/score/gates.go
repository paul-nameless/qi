package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/paul-nameless/qi/internal/jsvm/bench/harness"
)

type gateRule struct {
	name string
	want string
	ok   func(r harness.Rec) bool
}

func outIs(s string) func(harness.Rec) bool {
	return func(r harness.Rec) bool { return r.Err == "" && r.Out == s }
}

func noErr(r harness.Rec) bool { return r.Err == "" }

func errAt(name string, line int) func(harness.Rec) bool {
	return func(r harness.Rec) bool {
		e := harness.ParseErr(r.Err, 0)
		return r.Err != "" && e.Name == name && e.Line == line
	}
}

func errContains(s string, maxMs int64) func(harness.Rec) bool {
	return func(r harness.Rec) bool { return strings.Contains(r.Err, s) && r.Ms <= maxMs }
}

var parRe = regexp.MustCompile(`^(\d+) 8$`)

func interrupted(r harness.Rec) bool {
	e := strings.ToLower(r.Err)
	return (strings.Contains(e, "timed out") || strings.Contains(e, "interrupt")) && r.Ms >= 250 && r.Ms < 600
}

func perfWant(name string) string {
	for _, g := range gateRules {
		if g.name == name {
			return g.want
		}
	}
	return ""
}

func parallel(r harness.Rec) bool {
	m := parRe.FindStringSubmatch(r.Out)
	if r.Err != "" || m == nil {
		return false
	}
	n, _ := strconv.Atoi(m[1])
	return n < 300
}

var gateRules = []gateRule{
	{"parallel8x200", "8 results in < 300 ms", parallel},
	{"iife-state-set", "no error", noErr},
	{"iife-state-get", "number,undefined,undefined,undefined,number", outIs("number,undefined,undefined,undefined,number")},
	{"native-state-set", "no error", noErr},
	{"native-state-get", "number,number,number,function,function", outIs("number,number,number,function,function\n")},
	{"native-redeclare-const", "redeclared const 5", outIs("redeclared const 5\n")},
	{"native-redeclare-let", "redeclared let 6", outIs("redeclared let 6\n")},
	{"native-tla", "tla 20", outIs("tla 20\n")},
	{"native-tla-state", "number number", outIs("number number\n")},
	{"native-tla-par", "[100,100]", outIs("[\n 100,\n 100\n]\n")},
	{"native-throw", "TypeError at line 2", errAt("TypeError", 2)},
	{"native-reject", "ToolError at line 1", errAt("ToolError", 1)},
	{"cancel-busy", "interrupted after 300 ms, within 600 ms", func(r harness.Rec) bool { return interrupted(r) }},
	{"after-cancel-busy", "43", outIs("43")},
	{"cancel-pending", "before + interrupted after 300 ms, within 600 ms", func(r harness.Rec) bool { return r.Out == "before\n" && interrupted(r) }},
	{"after-cancel-pending", "44", outIs("44")},
	{"never-settles", "never-settle error", errContains("never settle", 1000)},
	{"err-throw-line3", "TypeError at line 3", errAt("TypeError", 4)},
	{"err-syntax-line2", "SyntaxError at line 2", errAt("SyntaxError", 3)},
	{"err-tool-uncaught", "ToolError at line 2", errAt("ToolError", 3)},
	{"err-tool-caught", "ToolError|boom: nope", outIs("ToolError|boom: nope")},
	{"err-stack", "ReferenceError at line 1", errAt("ReferenceError", 2)},
	{"unawaited", "ok", outIs("ok")},
	{"recursion", "caught RangeError", outIs("caught RangeError")},
	{"loop1e7", "29999994", outIs("29999994")},
	{"map-sort", "w0,40,w1,40", outIs("w0,40,w1,40")},
	{"regex", "150000 900000 150001", outIs("150000 900000 150001")},
	{"mcp8000", "6218002 1600", outIs("6218002 1600")},
}

type GateResult struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Ms   int64  `json:"ms"`
	Got  string `json:"got,omitempty"`
	Want string `json:"want,omitempty"`
}

func shortRec(r harness.Rec) string {
	s := r.Out
	if r.Err != "" {
		e := harness.ParseErr(r.Err, 0)
		s += fmt.Sprintf("!! %s: %s @%d", e.Name, e.Message, e.Line)
	}
	s = strings.ReplaceAll(s, "\n", "⏎")
	if len(s) > 90 {
		s = s[:90] + "…"
	}
	return s
}

func judgeGates(out runOut) []GateResult {
	var res []GateResult
	for _, g := range gateRules {
		r, ok := out.get(g.name)
		gr := GateResult{Name: g.name, Want: g.want}
		if ok {
			gr.OK, gr.Ms, gr.Got = g.ok(r), r.Ms, shortRec(r)
		} else {
			gr.Got = "missing"
		}
		if gr.OK {
			gr.Want = ""
		}
		res = append(res, gr)
	}
	return res
}
