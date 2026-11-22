package main

import (
	"fmt"
	"strings"
)

func (g *gen) program() program {
	if g.chance(0.12) {
		return g.multiCell()
	}
	var units []string
	for i := 0; i < 3+g.intn(3); i++ {
		units = append(units, g.declVar())
	}
	for i := 0; i < 5+g.intn(12); i++ {
		units = append(units, g.stmt(2, true))
	}
	return program{cells: []cell{mainCell(units)}}
}

func (g *gen) declVar() string {
	v := g.fresh("v")
	s := "let " + v + " = T(() => " + g.expr(1) + ");"
	g.vars = append(g.vars, v)
	return s
}

func (g *gen) stmt(d int, top bool) string {
	ws := []weighted{
		{12, func() string { return g.printExpr(2) }},
		{5, g.assign},
		{2, g.memberAssign},
		{3, func() string { return g.ifStmt(d) }},
		{4, func() string { return g.loop(d) }},
		{2, g.switchStmt},
		{2, g.tryFunc},
		{3, g.funcDecl},
		{1, g.closureLoop},
		{3, g.destructure},
		{2, g.objectOps},
		{2, g.evalOrder},
		{2, g.mapSet},
		{2, g.jsonStmt},
		{1, g.dateStmt},
		{2, g.regexStmt},
		{2, g.numFmt},
		{1, g.parseStmt},
		{2, func() string { return g.printExpr(3) }},
	}
	ws = append(ws, g.extraStmts()...)
	if top {
		ws = append(ws, weighted{3, g.asyncBlock})
	}
	return g.choose(ws)
}

var compoundOps = []string{"+=", "-=", "*=", "/=", "%=", "**=", "<<=", ">>=", ">>>=", "&=", "|=", "^=", "&&=", "||=", "??="}

func (g *gen) assign() string {
	if len(g.vars) == 0 {
		return g.printExpr(1)
	}
	v := g.varRef()
	if !strings.HasPrefix(v, "v") {
		return g.printExpr(1)
	}
	switch g.intn(4) {
	case 0:
		return v + " = T(() => " + g.expr(2) + "); P(" + g.nextLabel() + ", " + v + ");"
	case 1:
		return g.wrapTry(v + g.one("++", "--") + "; P(" + g.nextLabel() + ", " + v + ");")
	}
	op := compoundOps[g.intn(len(compoundOps))]
	rhs := g.expr(1)
	if op == "**=" {
		rhs = g.one("2", "0", "0.5", "(-1)")
	}
	return g.wrapTry(v + " " + op + " " + rhs + "; P(" + g.nextLabel() + ", " + v + ");")
}

func (g *gen) assignKey() string {
	return jsq(g.one("a", "b", "c", "x", "é", "1.5", "-1", "foo"))
}

func (g *gen) memberAssign() string {
	v := g.varRef()
	if !strings.HasPrefix(v, "v") {
		v = "void 0, globalThis.__sink"
	}
	return g.wrapTry("const o = " + g.objectLit(1) + "; o[" + g.assignKey() + "] = " + g.expr(1) + "; " + v + " = o; P(" + g.nextLabel() + ", o, Object.keys(o));")
}

func (g *gen) block(d int, n int) string {
	var ss []string
	for i := 0; i < n; i++ {
		if d <= 0 {
			ss = append(ss, g.printExpr(1))
			continue
		}
		ss = append(ss, g.stmt(d-1, false))
	}
	return "{ " + strings.Join(ss, " ") + " }"
}

func (g *gen) ifStmt(d int) string {
	s := "if (T(() => " + g.expr(2) + ")) " + g.block(d, 1+g.intn(2))
	if g.chance(0.6) {
		s += " else " + g.block(d, 1)
	}
	return s
}

func (g *gen) loopBody(d int, idx string) string {
	saved := g.vars
	if idx != "" {
		g.vars = append(append([]string(nil), g.vars...), idx)
	}
	var ss []string
	for i := 0; i < 1+g.intn(3); i++ {
		switch g.intn(5) {
		case 0:
			ss = append(ss, "if (T(() => "+g.expr(1)+")) "+g.one("break;", "continue;"))
		case 1:
			ss = append(ss, "acc.push(T(() => "+g.expr(1)+"));")
		default:
			if d > 1 && g.loopy < 1 {
				g.loopy++
				ss = append(ss, g.stmt(d-1, false))
				g.loopy--
			} else {
				ss = append(ss, g.printExpr(1))
			}
		}
	}
	g.vars = saved
	return "{ " + strings.Join(ss, " ") + " }"
}

func (g *gen) loop(d int) string {
	i := g.fresh("i")
	n := g.one("0", "1", "3", "4")
	var head string
	switch g.intn(7) {
	case 0:
		head = fmt.Sprintf("for (let %s = 0; %s < %s; %s++) %s", i, i, n, i, g.loopBody(d, i))
	case 1:
		head = fmt.Sprintf("for (const %s of %s) %s", i, g.iterable(), g.loopBody(d, i))
	case 2:
		head = fmt.Sprintf("for (const %s in %s) %s", i, g.one(g.objectLit(1), g.arrayLit(0), g.strLit(), "{b: 1, 2: 2, a: 3, 1: 4}"), g.loopBody(d, i))
	case 3:
		head = fmt.Sprintf("let %s = 0; while (%s < %s) { %s++; %s }", i, i, n, i, g.loopBody(d, i))
	case 4:
		head = fmt.Sprintf("let %s = 0; do { %s++; %s } while (%s < %s);", i, i, g.loopBody(d, i), i, n)
	case 5:
		head = fmt.Sprintf("for (const [%s, %s_v] of Object.entries(%s)) %s", i, i, g.objectLit(1), g.loopBody(d, i))
	default:
		head = fmt.Sprintf("for (let %s = %s; %s > 0; %s -= 1) %s", i, n, i, i, g.loopBody(d, i))
	}
	return "{ const acc = []; try { " + head + " } catch (e) { acc.push(E(e)); } P(" + g.nextLabel() + ", acc); }"
}

func (g *gen) iterable() string {
	return g.one(g.arrayLit(1), g.strLit(), "new Set("+g.arrayLit(0)+")", "new Map([[1, \"a\"], [\"k\", 2]])", "[1, 2, 3].entries()", "\"a😀b\"", g.varRef(), "new Map([[1, 2]]).keys()")
}

func (g *gen) switchStmt() string {
	var cases []string
	for i := 0; i < 2+g.intn(3); i++ {
		c := "case " + g.one(g.numLit(), g.strLit(), "null", "undefined", "true") + ": P(" + g.nextLabel() + ", " + g.expr(0) + ");"
		if g.chance(0.6) {
			c += " break;"
		}
		cases = append(cases, c)
	}
	if g.chance(0.7) {
		cases = append(cases, "default: P("+g.nextLabel()+", \"default\");")
	}
	if g.chance(0.3) {
		k := g.intn(len(cases))
		cases[0], cases[k] = cases[k], cases[0]
	}
	return g.wrapTry("switch (" + g.expr(1) + ") { " + strings.Join(cases, " ") + " }")
}

func (g *gen) thrownValue() string {
	return g.one("new Error(\"m\")", "new TypeError(\"t\")", "new RangeError(\"r\")", "42", "\"str\"", "({a: 1})", "undefined", "null")
}

func (g *gen) tryFunc() string {
	f := g.fresh("f")
	saved := g.vars
	g.vars = append(append([]string(nil), g.vars...), "x")
	body := "try { if (T(() => " + g.expr(1) + ")) throw " + g.thrownValue() + "; " + g.one("return "+g.expr(1)+";", "P("+g.nextLabel()+", x);") + " }"
	body += " catch (e) { P(" + g.nextLabel() + ", E(e)); " + g.one("", "return "+g.expr(1)+";", "throw e;", "throw "+g.thrownValue()+";") + " }"
	if g.chance(0.7) {
		body += " finally { P(" + g.nextLabel() + ", \"fin\"); " + g.one("", "", "return "+g.expr(1)+";") + " }"
	}
	g.vars = saved
	s := "function " + f + "(x) { " + body + " return \"end\"; }"
	return s + " P(" + g.nextLabel() + ", T(() => " + f + "(" + g.expr(0) + ")), T(() => " + f + "(" + g.expr(0) + ")));"
}

func (g *gen) funcDecl() string {
	f := g.fresh("f")
	var params []string
	var names []string
	switch g.intn(4) {
	case 0:
		params, names = []string{"a", "b = " + g.expr(0)}, []string{"a", "b"}
	case 1:
		params, names = []string{"a", "...rest"}, []string{"a", "rest"}
	case 2:
		params, names = []string{"{a, b = 2, ...o} = {}", "[c, , d = 4, ...r] = []"}, []string{"a", "b", "o", "c", "d", "r"}
	default:
		params, names = []string{"a", "b", "c = a"}, []string{"a", "b", "c"}
	}
	saved := g.vars
	g.vars = append(append([]string(nil), g.vars...), names...)
	inner := ""
	if g.chance(0.4) {
		inner = g.printExpr(1) + " "
	}
	ret := g.expr(2)
	g.vars = saved
	var decl string
	if g.chance(0.3) {
		decl = "const " + f + " = (" + strings.Join(params, ", ") + ") => { " + inner + "return " + ret + "; };"
	} else {
		decl = "function " + f + "(" + strings.Join(params, ", ") + ") { " + inner + "return " + ret + "; }"
	}
	g.fns = append(g.fns, fnInfo{name: f, arity: len(names)})
	return decl + " P(" + g.nextLabel() + ", T(() => " + f + "(" + g.argList(0, g.intn(4)) + ")));"
}

func (g *gen) closureLoop() string {
	kw := g.one("let", "let", "var")
	i := g.fresh("i")
	saved := g.vars
	g.vars = append(append([]string(nil), g.vars...), i, "j")
	body := g.expr(1)
	g.vars = saved
	return fmt.Sprintf("{ const fs = []; for (%s %s = 0; %s < 3; %s++) { let j = %s * 2; fs.push(() => [%s, j, T(() => %s)]); } P(%s, fs.map(f => f())); }", kw, i, i, i, i, i, body, g.nextLabel())
}

func (g *gen) destructure() string {
	l := g.nextLabel()
	switch g.intn(5) {
	case 0:
		return g.wrapTry("const [a = " + g.expr(0) + ", , b, ...rest] = " + g.iterable() + "; P(" + l + ", a, b, rest);")
	case 1:
		return g.wrapTry("const {a, b: {c = " + g.expr(0) + "} = {}, [" + g.key() + "]: d = 2, ...rest} = " + g.objectLit(1) + "; P(" + l + ", a, c, d, rest);")
	case 2:
		if len(g.vars) >= 2 {
			a, b := g.varRef(), g.varRef()
			if a != b && strings.HasPrefix(a, "v") && strings.HasPrefix(b, "v") {
				return g.wrapTry("[" + a + ", " + b + "] = [" + b + ", " + a + "]; P(" + l + ", " + a + ", " + b + ");")
			}
		}
		return g.wrapTry("let x, y; ({x, y = " + g.expr(0) + "} = " + g.objectLit(1) + "); P(" + l + ", x, y);")
	case 3:
		return g.wrapTry("const [[p, q = 1] = [], {r} = {r: " + g.expr(0) + "}] = " + g.arrayLit(1) + "; P(" + l + ", p, q, r);")
	}
	return g.wrapTry("const {length, 0: first, ...more} = " + g.one(g.strLit(), g.arrayLit(1)) + "; P(" + l + ", length, first, more);")
}

func (g *gen) objectOps() string {
	l := g.nextLabel()
	o := g.objectLit(1)
	switch g.intn(5) {
	case 0:
		vals := "Object.values(o)"
		if avoid["values"] {
			vals = "Object.keys(o).length"
		}
		return g.wrapTry("const o = " + o + "; o[" + g.assignKey() + "] = " + g.expr(0) + "; delete o[" + g.assignKey() + "]; P(" + l + ", " + g.assignKey() + " in o, Object.keys(o), " + vals + ", Object.entries(o));")
	case 1:
		return g.wrapTry("const o = Object.assign({}, " + o + ", " + g.objectLit(1) + "); P(" + l + ", o, {..." + g.objectLit(0) + ", ...o});")
	case 2:
		return g.wrapTry("P(" + l + ", Object.fromEntries([[" + g.key() + ", " + g.expr(0) + "], [\"b\", 1], [2, 2], [\"1\", 3]]));")
	case 3:
		return g.wrapTry("const o = Object.freeze(" + o + "); P(" + l + ", Object.isFrozen(o)); o.a = 9;")
	}
	return g.wrapTry("const o = {b: 1, 2: 1, a: 1, 1: 1, \"-1\": 1, \"01\": 1, [" + g.key() + "]: 0}; P(" + l + ", Object.keys(o), JSON.stringify(o));")
}

func (g *gen) evalOrder() string {
	l := g.nextLabel()
	op1, op2 := binOps[g.intn(len(binOps)-4)], binOps[g.intn(len(binOps)-4)]
	parts := []string{
		"const L = []; const t = (v) => (L.push(R(v)), v);",
		"P(" + l + ", T(() => t(" + g.expr(0) + ") " + op1 + " t(" + g.expr(0) + ") " + op2 + " t(" + g.expr(0) + ")));",
		"const o = {a: 1, b: [1, 2]}; try { o[t(\"a\")] " + compoundOps[g.intn(12)] + " t(" + g.expr(0) + "); } catch (e) { L.push(E(e)); }",
		"try { t(o)[t(\"b\")][t(0)] += t(2); } catch (e) { L.push(E(e)); }",
		"let z = 1; z += (z = 5, t(z));",
		"P(" + g.nextLabel() + ", L, o, z);",
	}
	return "{ " + strings.Join(parts, " ") + " }"
}

func (g *gen) mapSet() string {
	parts := []string{
		"const m = new Map([[" + g.expr(0) + ", 1], [\"k\", " + g.expr(0) + "], [NaN, 2]]); m.set(-0, \"z\").set(" + g.expr(0) + ", 3);",
		"P(" + g.nextLabel() + ", m.get(0), m.has(NaN), m.size, m.get(" + g.expr(0) + "));",
		"m.delete(" + g.expr(0) + ");",
		"let c = 0; for (const [k, v] of m) { if (++c > 8) break; if (T(() => " + g.callbackBody("k", "v") + ")) m.delete(k); else if (c < 3) m.set(String(k) + \"x\", v); }",
		"P(" + g.nextLabel() + ", m, [...m.keys()], [...m.values()]);",
		"const s = new Set(" + g.arrayRecv(0) + "); s.add(" + g.expr(0) + ").add(-0).add(NaN);",
		"for (const x of s) { if (++c > 14) break; if (c % 2) s.delete(x); else s.add(c); }",
		"const fe = []; m.forEach((v, k) => fe.push(R(k) + \":\" + R(v))); s.forEach(v => fe.push(R(v)));",
		"P(" + g.nextLabel() + ", s, s.size, s.has(0), fe);",
	}
	return g.wrapTry(strings.Join(parts, " "))
}

func (g *gen) callbackBody(params ...string) string {
	saved := g.vars
	g.vars = append(append([]string(nil), g.vars...), params...)
	b := g.expr(1)
	g.vars = saved
	return b
}

var jsonTexts = []string{`{"a":[1,2,{"b":null}],"c":"x"}`, `[1,2,3]`, `"\u00e9\ud83d\ude00"`, ` 1 `, `1e5`, `-0`, `{"__proto__":1}`, `{"a":1,"a":2}`, `[1,]`,
	`{'a':1}`, `NaN`, `[[[]]]`, `{"1":1,"b":2,"0":3}`, `true`, `nul`, `"\x"`, `{"a":{"b":{"c":[1,{"d":2}]}}}`, `[1e400, -1e-400]`, `"line\nbreak"`, ``, `0123`, `{"a" : 1 , "b":[ ]}`}

func (g *gen) jsonStmt() string {
	l := g.nextLabel()
	replacer := g.one("null", "undefined", "[\"a\", \"b\", \"0\", 1]", "(k, v) => typeof v === \"number\" ? v * 2 : v", "(k, v) => k === \"a\" ? undefined : v", "(k, v) => v === null ? \"N\" : v")
	indent := g.one("undefined", "2", "0", "\"\\t\"", "\"--\"", "11", "\"0123456789ABC\"", "(-1)", "1.9")
	reviver := g.one("undefined", "(k, v) => typeof v === \"number\" ? v + 1 : v", "(k, v) => Array.isArray(v) ? v.length : v", "(k, v) => k === \"a\" ? undefined : v", "(k, v) => k === \"\" ? [v] : v")
	text := jsq(jsonTexts[g.intn(len(jsonTexts))])
	if g.chance(0.3) {
		text = "JSON.stringify(" + g.expr(1) + ")"
	}
	return "P(" + l + ", T(() => JSON.stringify(" + g.expr(2) + ", " + replacer + ", " + indent + ")), T(() => JSON.parse(" + text + ", " + reviver + ")));"
}

var timestamps = []string{"0", "1e12", "(-1)", "1700000000123", "8.64e15", "8.64e15 + 1", "NaN", "951782400000", "(-62198755200000)", "253402300800000", "1.5", "(-8.64e15)"}
var isoTexts = []string{"2024-02-29", "2024-02-29T12:34:56Z", "2024-02-29T12:34:56.789+02:00", "2024-13-01", "1970-01-01T00:00:00", "+275760-09-13T00:00:00.000Z", "20240229", "2024-02-29T24:00:00Z", "-000001-01-01T00:00:00Z", "2024-02-29T12:34Z", "2024"}

func (g *gen) dateStmt() string {
	ts := timestamps[g.intn(len(timestamps))]
	parts := []string{
		"const d = new Date(" + ts + ");",
		"P(" + g.nextLabel() + ", T(() => d.toISOString()), d.getTime(), d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate(), d.getUTCDay(), d.getUTCHours(), d.getUTCMinutes(), d.getUTCSeconds(), d.getUTCMilliseconds(), T(() => JSON.stringify(d)));",
		"P(" + g.nextLabel() + ", d." + g.one("setUTCMonth", "setUTCDate", "setUTCHours", "setUTCFullYear", "setUTCMinutes", "setTime") + "(" + g.one("0", "11", "12", "40", "(-1)", "25", "NaN", "2000", "61") + g.opt(", "+g.one("5", "60", "(-3)")) + "), T(() => d.toISOString()));",
		"P(" + g.nextLabel() + ", Date.UTC(" + g.one("2024", "1970", "99", "0", "275760") + ", " + g.one("0", "1", "11", "12", "(-1)") + g.opt(", "+g.one("1", "29", "31", "0")) + "), Date.parse(" + jsq(isoTexts[g.intn(len(isoTexts))]) + "), new Date(2024, 1, 29, 12).getTime(), " + tzExpr() + ");",
	}
	return g.wrapTry(strings.Join(parts, " "))
}

func tzExpr() string {
	if avoid["tz"] {
		return "d.getTimezoneOffset() + 0"
	}
	return "d.getTimezoneOffset()"
}

func (g *gen) numFmt() string {
	n := g.one(g.numLit(), "(0.1 * 3)", "(1 / 3)", "(2 ** 53)", "(-1.005)", "1.45", "123.456e10", "5e-7", "(2 ** 70)", "0.5", "(-0.000001)", "1e-10", "999.995")
	r := g.one("2", "8", "16", "36", "10", "3", "1", "37", "undefined", "2.5")
	dg := g.one("0", "1", "2", "5", "10", "20", "100", "101", "(-1)", "undefined", "3.7")
	pr := g.one("1", "2", "5", "16", "21", "100", "101", "0", "undefined")
	l := g.nextLabel()
	return fmt.Sprintf("P(%s, T(() => (%s).toString(%s)), T(() => (%s).toFixed(%s)), T(() => (%s).toPrecision(%s)), T(() => (%s).toExponential(%s)), String(%s), %s + \"\");", l, n, r, n, dg, n, pr, n, dg, n, n)
}

var numericTexts = []string{" 12px", "0x1F", "1e3", "-0", ".5", "Infinity", "-Infinity", "1_000", "0b101", "  \n42\t", "", "12abc", "0o17", "+-1", "١٢", "3.14.15", "0.0000001", "1e-7", "  ", "-0x10", "9007199254740993", "1e1000", "00012", "5.", "+.5e+2", "NaN", "0X1g", "\u00a07"}

func (g *gen) parseStmt() string {
	s := jsq(numericTexts[g.intn(len(numericTexts))])
	r := g.one("10", "16", "2", "36", "0", "1", "37", "undefined", "8")
	return fmt.Sprintf("P(%s, %s, parseInt(%s), parseInt(%s, %s), parseFloat(%s), Number(%s), +%s, %s * 1, Number.parseFloat(%s) === parseFloat(%s));", g.nextLabel(), s, s, s, r, s, s, s, s, s, s)
}

func (g *gen) regexStmt() string {
	pat, flags := g.regexPattern()
	s := jsq(g.one("aab abc ABC a1b2", "foo bar foo", "line1\nline2\nline3", "é😀aé", "", "aaa", "x=1, y=22; z=333", "The Cat sat", "a.b*c", "abcabc"))
	l := g.nextLabel()
	calls := []string{
		"re.source", "re.flags", "T(() => re.test(" + s + "))", "T(() => re.exec(" + s + "))", "T(() => re.exec(" + s + ")?.index)",
		"T(() => " + s + ".match(re))", "T(() => " + s + ".replace(re, \"<$&>\"))", "T(() => " + s + ".split(re))", "T(() => " + s + ".search(re))",
		"T(() => [..." + s + ".matchAll(re)].map(m => [m.index, ...m]))", "T(() => " + s + ".replace(re, (m, ...a) => \"[\" + m + \"]\"))", "re.lastIndex",
	}
	return "{ const re = T(() => new RegExp(" + jsq(pat) + ", " + jsq(flags) + ")); if (typeof re === \"string\") P(" + l + ", re); else P(" + l + ", " + strings.Join(calls, ", ") + "); }"
}

var reChars = []string{"a", "b", "c", "A", "1", " ", "\\n", "é", ".", "\\d", "\\w", "\\s", "\\W", "[ab]", "[^a]", "[a-c]", "\\.", "x", "o", "\\b"}

func (g *gen) regexPattern() (string, string) {
	st := &reState{}
	p := g.reAlt(0, st)
	var f strings.Builder
	for _, c := range "gimsuy" {
		if c == 'y' && avoid["splitsticky"] {
			continue
		}
		if g.chance(0.25) {
			f.WriteRune(c)
		}
	}
	return p, f.String()
}

type reState struct{ groups int }

func (g *gen) reAlt(d int, st *reState) string {
	n := 1
	if g.chance(0.25) {
		n = 2
	}
	var alts []string
	for i := 0; i < n; i++ {
		alts = append(alts, g.reSeq(d, st))
	}
	return strings.Join(alts, "|")
}

func (g *gen) reSeq(d int, st *reState) string {
	var b strings.Builder
	for i := 0; i < 1+g.intn(3); i++ {
		b.WriteString(g.reQuant(g.reAtom(d, st)))
	}
	return b.String()
}

func (g *gen) reAtom(d int, st *reState) string {
	k := g.r.Float64()
	switch {
	case d > 2 || k < 0.5:
		return reChars[g.intn(len(reChars))]
	case k < 0.58:
		return g.one("^", "$")
	case k < 0.75:
		st.groups++
		if g.chance(0.2) {
			return fmt.Sprintf("(?<n%d>%s)", st.groups, g.reAlt(d+1, st))
		}
		return "(" + g.reAlt(d+1, st) + ")"
	case k < 0.83:
		return "(?:" + g.reAlt(d+1, st) + ")"
	case k < 0.92:
		return g.one("(?=", "(?!", "(?<=", "(?<!") + g.reAlt(d+1, st) + ")"
	}
	if st.groups > 0 {
		return fmt.Sprintf("\\%d", 1+g.intn(st.groups))
	}
	return "a"
}

func (g *gen) reQuant(a string) string {
	if a == "^" || a == "$" || a == "\\b" || strings.HasPrefix(a, "(?=") || strings.HasPrefix(a, "(?!") || strings.HasPrefix(a, "(?<=") || strings.HasPrefix(a, "(?<!") || g.chance(0.6) {
		return a
	}
	q := g.one("*", "+", "?", "{2}", "{1,3}", "{0,2}", "{2,}")
	if g.chance(0.3) {
		q += "?"
	}
	return a + q
}

func (g *gen) asyncStep() string {
	return g.one("null", "1", "Promise.resolve(2)", "new Promise(res => res(3))", "new Promise(res => Promise.resolve().then(() => res(4)))",
		"sleep("+fmt.Sprint(g.intn(4))+")", "Promise.reject(new Error(\"x\")).catch(e => e.name)", "(async () => 5)()", "({then: r => r(6)})", "Promise.resolve().then(() => L.push(\"inner\"))")
}

func (g *gen) asyncBlock() string {
	var parts []string
	parts = append(parts, "const L = [];")
	n := 2 + g.intn(3)
	var names []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("a%d", i)
		body := "L.push(\"" + name + ":\" + x);"
		for k := 0; k < 1+g.intn(3); k++ {
			body += " await " + g.asyncStep() + "; L.push(\"" + name + "." + fmt.Sprint(k) + "\");"
		}
		if i > 0 && g.chance(0.5) {
			prev := names[g.intn(len(names))]
			body += " const v = await " + prev + "(x + 1).catch(e => (L.push(\"c:" + name + ":\" + E(e)), -1)); L.push(\"" + name + "v\" + v);"
		}
		if g.chance(0.35) {
			body += " if (x > " + fmt.Sprint(g.intn(3)) + ") throw new " + g.one("RangeError", "TypeError", "Error") + "(\"" + name + "\");"
		}
		body += " return x * 10;"
		parts = append(parts, "async function "+name+"(x) { "+body+" }")
		names = append(names, name)
	}
	var ps []string
	for i, nm := range names {
		p := nm + "(" + fmt.Sprint(i) + ")"
		switch g.intn(4) {
		case 0:
			p += ".then(v => (L.push(\"t:\" + v), v))"
		case 1:
			p += ".finally(() => L.push(\"f" + fmt.Sprint(i) + "\"))"
		case 2:
			p += ".catch(e => (L.push(\"k:\" + E(e)), \"caught\"))"
		}
		ps = append(ps, p)
	}
	parts = append(parts, "const ps = ["+strings.Join(ps, ", ")+"];")
	parts = append(parts, "Promise.resolve().then(() => L.push(\"m1\")).then(() => L.push(\"m2\")); queueMicrotask(() => L.push(\"q\"));")
	comb := g.one(
		"await Promise.allSettled(ps).then(rs => rs.map(r => r.status + \":\" + R(r.status === \"fulfilled\" ? r.value : r.reason)))",
		"await Promise.all(ps).catch(e => E(e))",
		"await Promise.race(ps).catch(e => E(e))",
		"await Promise.any(ps).catch(e => E(e) + R(e.errors))",
		"await ps[0].catch(e => E(e))",
	)
	parts = append(parts, "const r = "+comb+";", "L.push(\"end\");", "await sleep(10);", "P("+g.nextLabel()+", L, r);")
	return g.wrapTry(strings.Join(parts, " "))
}

func (g *gen) multiCell() program {
	g.vars = nil
	var cells []cell
	names := []string{"ga", "gb", "gc"}
	kw := []string{"let", "const", "var"}
	var c1 []string
	for i, n := range names {
		c1 = append(c1, kw[i]+" "+n+" = T(() => "+g.expr(1)+");")
	}
	c1 = append(c1, "function gf(x) { return T(() => "+g.callbackBody("x")+"); }", "P(\"c1\", ga, gb, gc, gf(1));")
	cells = append(cells, cell{units: c1})
	g.vars = append([]string(nil), names...)
	for c := 0; c < 1+g.intn(3); c++ {
		var cl []string
		for k, n := range names {
			if g.chance(0.3) {
				cl = append(cl, kw[k]+" "+n+" = T(() => "+g.expr(1)+");")
			}
		}
		if g.chance(0.3) {
			cl = append(cl, "function gf(x) { return T(() => "+g.callbackBody("x")+"); }")
		}
		cl = append(cl, "P(\"c"+fmt.Sprint(c+2)+"\", T(() => ga), T(() => gb), T(() => gc), T(() => gf(2)));")
		for k := 0; k < 1+g.intn(3); k++ {
			switch g.intn(4) {
			case 0:
				cl = append(cl, g.one("ga", "gc")+" = T(() => "+g.expr(1)+");")
			case 1:
				t := g.fresh("gt")
				cl = append(cl, "const "+t+" = await Promise.resolve(T(() => "+g.expr(1)+")); P(\"aw\", "+t+");")
			case 2:
				cl = append(cl, g.printExpr(2))
			default:
				cl = append(cl, "P(\"gx\", typeof gnew, T(() => gf(ga)));")
			}
		}
		if g.chance(0.4) {
			cl = append(cl, "[ga, typeof gf]")
		}
		cells = append(cells, cell{units: cl})
	}
	return program{cells: cells}
}
