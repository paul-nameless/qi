package main

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
)

const prelude = `const R = (v, d = 0) => {
  if (d > 4) return "~";
  const t = typeof v;
  if (t === "number") return Object.is(v, -0) ? "-0" : String(v);
  if (t === "string") return JSON.stringify(v);
  if (t === "undefined" || t === "boolean") return String(v);
  if (t === "function") return "fn";
  if (v === null) return "null";
  if (Array.isArray(v)) return "[" + v.map(x => R(x, d + 1)).join(",") + "]";
  if (v instanceof Map) return "Map{" + [...v].map(([k, x]) => R(k, d + 1) + "=>" + R(x, d + 1)).join(",") + "}";
  if (v instanceof Set) return "Set{" + [...v].map(x => R(x, d + 1)).join(",") + "}";
  if (v instanceof Error) return "<" + v.name + ">";
  if (v instanceof Date) return "Date(" + v.getTime() + ")";
  if (v instanceof RegExp) return String(v);
  if (v instanceof Promise) return "Promise";
  return "{" + Object.keys(v).map(k => JSON.stringify(k) + ":" + R(v[k], d + 1)).join(",") + "}";
};
const P = (...a) => console.log(a.map(x => R(x)).join(" "));
const E = (e) => e instanceof Error ? "!" + e.name : "!thrown:" + R(e);
const T = (f) => { try { return f(); } catch (e) { return E(e); } };`

type cell struct {
	head  []string
	units []string
	tail  []string
}

type program struct {
	cells []cell
}

func (p program) source() string {
	var b strings.Builder
	for i, c := range p.cells {
		if i > 0 {
			b.WriteString("\n//// cell\n")
		} else {
			b.WriteString(prelude + "\n")
		}
		var lines []string
		lines = append(lines, c.head...)
		lines = append(lines, c.units...)
		lines = append(lines, c.tail...)
		b.WriteString(strings.Join(lines, "\n"))
	}
	return b.String()
}

func (p program) clone() program {
	q := program{cells: make([]cell, len(p.cells))}
	for i, c := range p.cells {
		q.cells[i] = cell{head: c.head, units: append([]string(nil), c.units...), tail: c.tail}
	}
	return q
}

func mainCell(units []string) cell {
	return cell{head: []string{"async function __main() {", "\"use strict\";"}, units: units, tail: []string{"}", "await __main();"}}
}

var avoid = map[string]bool{}

type gen struct {
	r     *rand.Rand
	vars  []string
	fns   []fnInfo
	n     int
	label int
	loopy int
}

type fnInfo struct {
	name  string
	arity int
	async bool
}

func newGen(seed uint64) *gen {
	return &gen{r: rand.New(rand.NewPCG(seed, seed*0x9E3779B97F4A7C15+1))}
}

func (g *gen) intn(n int) int { return g.r.IntN(n) }

func (g *gen) chance(p float64) bool { return g.r.Float64() < p }

func (g *gen) one(xs ...string) string { return xs[g.intn(len(xs))] }

func (g *gen) fresh(prefix string) string {
	g.n++
	return fmt.Sprintf("%s%d", prefix, g.n)
}

func (g *gen) nextLabel() string {
	g.label++
	return fmt.Sprintf("\"s%d\"", g.label)
}

func jsq(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSpace(b.String())
}

type weighted struct {
	w int
	f func() string
}

func (g *gen) choose(ws []weighted) string {
	total := 0
	for _, w := range ws {
		total += w.w
	}
	k := g.intn(total)
	for _, w := range ws {
		if k < w.w {
			return w.f()
		}
		k -= w.w
	}
	return ws[0].f()
}

var numLits = []string{"0", "(-0)", "1", "2", "3", "7", "(-1)", "(-7)", "0.5", "(-2.5)", "1.5", "2.5", "0.1", "0.2", "1e21", "1e-7", "123.456",
	"9007199254740993", "4294967295", "2147483648", "(-2147483649)", "255", "0x10", "NaN", "Infinity", "(-Infinity)", "1e300", "5e-324", "100", "10", "0.000001", "1234.5678"}

var strLits = []string{"", "a", "abc", "Hello, World", "10", " 12 ", "1e3", "0x1f", "-0", "true", "null", "a,b,c", "ABC", "é", "日本語", "😀x", "Straße",
	"  padded  ", "x-y_z", "3.14", "Infinity", "0b101", "line1\nline2", "tab\there", "ǅ", "İ", "aAbB", "12px", "+5", "-", "07", "  1"}

func (g *gen) numLit() string { return numLits[g.intn(len(numLits))] }
func (g *gen) strLit() string { return jsq(strLits[g.intn(len(strLits))]) }

func (g *gen) smallInt() string { return fmt.Sprint(g.intn(7) - 2) }

func (g *gen) index() string {
	return g.one("0", "1", "2", "3", "(-1)", "(-2)", "(-10)", "10", "NaN", "Infinity", "(-Infinity)", "1.5", "\"1\"", "undefined", "null", "1e10", "(-0)", "true")
}

func (g *gen) size() string {
	return g.one("0", "1", "2", "3", "5", "8", "(-1)", "NaN", "1.7", "\"3\"", "undefined", "null")
}

func (g *gen) atom() string {
	return g.choose([]weighted{
		{6, g.numLit},
		{5, g.strLit},
		{2, func() string { return g.one("true", "false", "null", "undefined") }},
		{8, g.varRef},
		{2, func() string { return g.arrayLit(1) }},
		{2, func() string { return g.objectLit(1) }},
	})
}

func (g *gen) varRef() string {
	if len(g.vars) == 0 {
		return g.numLit()
	}
	return g.vars[g.intn(len(g.vars))]
}

var binOps = []string{"+", "+", "-", "*", "/", "%", "**", "&", "|", "^", "<<", ">>", ">>>", "<", ">", "<=", ">=", "==", "!=", "===", "!==", "&&", "||", "??", "in"}

func (g *gen) expr(d int) string {
	if d <= 0 {
		return g.atom()
	}
	return g.choose([]weighted{
		{6, g.atom},
		{10, func() string {
			op := binOps[g.intn(len(binOps))]
			if op == "**" {
				return "(" + g.expr(d-1) + " ** " + g.one("0", "1", "2", "3", "0.5", "(-1)", "NaN", "\"2\"") + ")"
			}
			return "(" + g.expr(d-1) + " " + op + " " + g.expr(d-1) + ")"
		}},
		{4, func() string {
			return "(" + g.one("typeof ", "void ", "!", "-", "+", "~", "- -") + g.expr(d-1) + ")"
		}},
		{2, func() string { return "(" + g.expr(d-1) + " ? " + g.expr(d-1) + " : " + g.expr(d-1) + ")" }},
		{1, func() string { return "(" + g.expr(d-1) + ", " + g.expr(d-1) + ")" }},
		{2, func() string { return g.template(d - 1) }},
		{2, func() string { return g.optional(d - 1) }},
		{3, func() string { return g.member(d - 1) }},
		{3, func() string { return g.conversion(d - 1) }},
		{4, func() string { return g.stringCall(d - 1) }},
		{4, func() string { return g.arrayCall(d - 1) }},
		{2, func() string { return g.arrayLit(d - 1) }},
		{2, func() string { return g.objectLit(d - 1) }},
		{2, func() string { return g.mathCall(d - 1) }},
		{2, func() string { return g.fnCall(d - 1) }},
		{1, func() string { return g.objectCall(d - 1) }},
	})
}

var tmplText = []string{"", "a", " ", "x=", "-", "é", "[", "]", ": "}

func (g *gen) template(d int) string {
	var b strings.Builder
	b.WriteString("`")
	n := 1 + g.intn(3)
	for i := 0; i < n; i++ {
		b.WriteString(tmplText[g.intn(len(tmplText))])
		b.WriteString("${" + g.expr(d) + "}")
	}
	b.WriteString(tmplText[g.intn(len(tmplText))])
	b.WriteString("`")
	return b.String()
}

func (g *gen) key() string {
	return g.choose([]weighted{
		{4, func() string {
			return jsq(g.one("a", "b", "c", "length", "x", "0", "1", "2", "10", "-1", "1.5", "__proto__x", "constructorx", "toStringx", "é"))
		}},
		{2, g.index},
		{1, func() string { return g.expr(0) }},
	})
}

func (g *gen) optional(d int) string {
	e := g.expr(d)
	switch g.intn(4) {
	case 0:
		return "(" + e + ")?.length"
	case 1:
		return "(" + e + ")?.[" + g.key() + "]"
	case 2:
		return "(" + e + ")?.a?.b"
	}
	return "(" + e + ")?.toString?.()"
}

func (g *gen) member(d int) string {
	e := g.expr(d)
	if g.chance(0.4) {
		return "(" + e + ").length"
	}
	return "(" + e + ")[" + g.key() + "]"
}

func (g *gen) conversion(d int) string {
	e := g.expr(d)
	switch g.intn(8) {
	case 0:
		return "String(" + e + ")"
	case 1:
		return "Number(" + e + ")"
	case 2:
		return "Boolean(" + e + ")"
	case 3:
		return "parseInt(" + e + g.one("", ", 16", ", 2", ", 36", ", 0", ", 1", ", 10") + ")"
	case 4:
		return "parseFloat(" + e + ")"
	case 5:
		return "isNaN(" + e + ")"
	case 6:
		return "Number.isInteger(" + e + ")"
	}
	return "JSON.stringify(" + e + ")"
}

func (g *gen) argList(d, n int) string {
	var as []string
	for i := 0; i < n; i++ {
		as = append(as, g.expr(d))
	}
	return strings.Join(as, ", ")
}

func (g *gen) stringCall(d int) string {
	recv := "String(" + g.expr(d) + ")"
	if g.chance(0.5) {
		recv = g.strLit()
	}
	m := g.choose([]weighted{
		{3, func() string {
			return g.one("slice", "substring", "substr") + "(" + g.index() + g.opt(", "+g.index()) + ")"
		}},
		{2, func() string { return g.one("charAt", "charCodeAt", "codePointAt", "at") + "(" + g.index() + ")" }},
		{3, func() string {
			return g.one("indexOf", "lastIndexOf", "includes", "startsWith", "endsWith") + "(" + g.strArg() + g.opt(", "+g.index()) + ")"
		}},
		{2, func() string { return g.one("padStart", "padEnd") + "(" + g.size() + g.opt(", "+g.strLit()) + ")" }},
		{1, func() string { return "repeat(" + g.size() + ")" }},
		{3, func() string { return "split(" + g.strArg() + g.opt(", "+g.size()) + ")" }},
		{2, func() string { return g.one("trim", "trimStart", "trimEnd", "toUpperCase", "toLowerCase") + "()" }},
		{2, func() string {
			return g.one("replace", "replaceAll") + "(" + g.strArg() + ", " + g.one(jsq("$&$&"), jsq("[$`|$']"), jsq("$$"), jsq("-"), jsq(""), jsq("$1"), "(m, i) => m + i") + ")"
		}},
		{1, func() string { return "concat(" + g.argList(0, 1+g.intn(2)) + ")" }},
	})
	return "(" + recv + ")." + m
}

func (g *gen) strArg() string {
	return g.one(jsq(""), jsq("a"), jsq("b"), jsq(","), jsq(" "), jsq("l"), jsq("ab"), jsq("é"), jsq("😀"), "undefined", "1", jsq("1"), jsq("x"), jsq("ll"))
}

func (g *gen) opt(s string) string {
	if g.chance(0.5) {
		return s
	}
	return ""
}

func (g *gen) arrayLit(d int) string {
	n := g.intn(5)
	var es []string
	for i := 0; i < n; i++ {
		if d > 0 && g.chance(0.15) {
			es = append(es, "..."+g.one("[1, 2]", "\"ab\"", g.arrayLit(0), "[]"))
			continue
		}
		es = append(es, g.expr(d))
	}
	return "[" + strings.Join(es, ", ") + "]"
}

func (g *gen) objectLit(d int) string {
	n := g.intn(4)
	var es []string
	for i := 0; i < n; i++ {
		switch g.intn(6) {
		case 0:
			es = append(es, "["+g.expr(0)+"]: "+g.expr(d))
		case 1:
			if d > 0 {
				es = append(es, "..."+g.one("{a: 1, b: 2}", "[7, 8]", "\"hi\"", "null", g.objectLit(0)))
				continue
			}
			fallthrough
		case 2:
			es = append(es, jsq(g.one("2", "1", "10", "b", "a", "z", "-1", "1.5", "01"))+": "+g.expr(d))
		default:
			es = append(es, g.one("a", "b", "c", "x", "y")+": "+g.expr(d))
		}
	}
	return "({" + strings.Join(es, ", ") + "})"
}

func (g *gen) mathCall(d int) string {
	f := g.one("abs", "floor", "ceil", "round", "trunc", "sign", "sqrt", "cbrt", "max", "min", "pow", "hypot", "fround", "clz32", "imul", "atan2", "log2", "log10")
	if f == "hypot" && avoid["hypot"] {
		f = "max"
	}
	n := 1
	switch f {
	case "max", "min", "hypot":
		n = g.intn(4)
	case "pow", "imul", "atan2":
		n = 2
	}
	return "Math." + f + "(" + g.argList(d, n) + ")"
}

func (g *gen) objectCall(d int) string {
	e := g.expr(d)
	f := g.one("Object.keys", "Object.values", "Object.entries", "Array.isArray", "Object.getOwnPropertyNames")
	if f == "Object.getOwnPropertyNames" && avoid["ownnames"] || f == "Object.values" && avoid["values"] {
		f = "Object.keys"
	}
	return f + "(" + e + ")"
}

func (g *gen) fnCall(d int) string {
	var sync []fnInfo
	for _, f := range g.fns {
		if !f.async {
			sync = append(sync, f)
		}
	}
	if len(sync) == 0 {
		return g.mathCall(d)
	}
	f := sync[g.intn(len(sync))]
	return f.name + "(" + g.argList(d, g.intn(f.arity+2)) + ")"
}

func (g *gen) callback(d int, params ...string) string {
	saved := g.vars
	g.vars = append(append([]string(nil), g.vars...), params...)
	body := g.expr(d)
	g.vars = saved
	return "(" + strings.Join(params, ", ") + ") => " + body
}

var keyFns = []string{"String(x)", "typeof x", "String(x).length", "JSON.stringify(x) ?? \"u\"", "String(typeof x === \"number\" ? x : 0).padStart(30, \"0\")"}

func (g *gen) comparator() string {
	k := keyFns[g.intn(len(keyFns))]
	ka, kb := strings.ReplaceAll(k, "x", "a"), strings.ReplaceAll(k, "x", "b")
	if g.chance(0.5) {
		return fmt.Sprintf("(a, b) => (%s) < (%s) ? -1 : (%s) > (%s) ? 1 : 0", ka, kb, ka, kb)
	}
	return fmt.Sprintf("(a, b) => (%s) < (%s) ? 1 : (%s) > (%s) ? -1 : 0", ka, kb, ka, kb)
}

func (g *gen) arrayRecv(d int) string {
	return g.choose([]weighted{
		{4, func() string { return g.arrayLit(d) }},
		{2, func() string { return "[" + g.varRef() + ", " + g.expr(0) + ", " + g.expr(0) + "]" }},
		{1, func() string { return "[[1, [2, [3]]], [4], 5]" }},
		{1, func() string { return "Array.from(" + g.strLit() + ")" }},
		{1, func() string { return "Array.of(" + g.argList(0, g.intn(4)) + ")" }},
		{1, func() string {
			return "Array.from({length: " + g.one("0", "1", "3", "4") + "}, " + g.callback(1, "x", "i") + ")"
		}},
	})
}

func (g *gen) arrayCall(d int) string {
	recv := g.arrayRecv(d)
	m := g.choose([]weighted{
		{3, func() string {
			return g.one("map", "filter", "some", "every", "find", "findIndex", "findLast", "findLastIndex", "flatMap") + "(" + g.callback(1, "x", "i") + ")"
		}},
		{2, func() string {
			return g.one("reduce", "reduceRight") + "(" + g.callback(1, "acc", "x") + g.opt(", "+g.expr(0)) + ")"
		}},
		{2, func() string { return "sort(" + g.opt(g.comparator()) + ")" }},
		{2, func() string { return "splice(" + g.index() + g.opt(", "+g.index()+g.opt(", "+g.expr(0))) + ")" }},
		{2, func() string { return "slice(" + g.index() + g.opt(", "+g.index()) + ")" }},
		{1, func() string { return "flat(" + g.opt(g.one("0", "1", "2", "Infinity", "(-1)")) + ")" }},
		{2, func() string {
			return g.one("indexOf", "lastIndexOf", "includes") + "(" + g.expr(0) + g.opt(", "+g.index()) + ")"
		}},
		{1, func() string { return "at(" + g.index() + ")" }},
		{1, func() string { return "concat(" + g.argList(0, 1+g.intn(2)) + ")" }},
		{2, func() string { return "join(" + g.opt(g.strArg()) + ")" }},
		{1, func() string { return "reverse()" }},
		{1, func() string { return "fill(" + g.expr(0) + g.opt(", "+g.index()+g.opt(", "+g.index())) + ")" }},
		{1, func() string { return "copyWithin(" + g.index() + ", " + g.index() + g.opt(", "+g.index()) + ")" }},
		{1, func() string { return g.one("keys", "values", "entries") + "().toArrayX" }},
		{1, func() string { return g.one("toSorted()", "toReversed()", "with("+g.index()+", 9)", "toSpliced(1, 1)") }},
	})
	if strings.HasSuffix(m, ".toArrayX") {
		return "[...(" + recv + ")." + strings.TrimSuffix(m, ".toArrayX") + "]"
	}
	return "(" + recv + ")." + m
}

func (g *gen) wrapTry(body string) string {
	return "try { " + body + " } catch (e) { P(" + g.nextLabel() + ", E(e)); }"
}

func (g *gen) printExpr(d int) string {
	return "P(" + g.nextLabel() + ", T(() => " + g.expr(d) + "));"
}
