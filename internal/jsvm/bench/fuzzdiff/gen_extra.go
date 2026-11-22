package main

import (
	"fmt"
	"strings"
)

func (g *gen) extraStmts() []weighted {
	return []weighted{
		{2, g.protoStmt},
		{1, g.errorStmt},
		{2, g.builtinsStmt},
		{1, g.spreadStmt},
		{1, g.equalityStmt},
		{1, g.counterStmt},
	}
}

func (g *gen) protoStmt() string {
	k := g.fresh("K")
	parts := []string{
		"function " + k + "(a) { this.a = a; this.b = T(() => " + g.callbackBody("a") + "); }",
		k + ".prototype.get = function (x) { return [this.a, x, T(() => " + g.callbackBody("x") + ")]; };",
		k + ".prototype.c = " + g.expr(0) + ";",
		"const k = new " + k + "(" + g.expr(0) + ");",
		"P(" + g.nextLabel() + ", k, T(() => k.get(" + g.expr(0) + ")), k instanceof " + k + ", Object.keys(k), \"c\" in k, k.hasOwnProperty(\"c\"), typeof " + k + ");",
		"P(" + g.nextLabel() + ", " + k + ".prototype.get.call({a: 9}, 1), " + k + ".prototype.get.apply({a: 8}, [2]), k.get.bind({a: 7}, 3)(), Object.getPrototypeOf(k) === " + k + ".prototype);",
		"const o = {n: " + g.expr(0) + ", m() { return this.n; }, f: () => typeof this};",
		"P(" + g.nextLabel() + ", o.m(), T(() => (0, o.m)()), o.f(), T(() => o.m.call(k)));",
		"const oc = Object.create({inh: 1, sh: 2}); oc.own = " + g.expr(0) + "; oc.sh = 3;",
		"const keys = []; for (const p in oc) keys.push(p);",
		"P(" + g.nextLabel() + ", Object.keys(oc), keys, oc.inh, \"inh\" in oc, JSON.stringify(oc), Object.getPrototypeOf(Object.getPrototypeOf(oc)) === Object.prototype);",
	}
	return g.wrapTry(strings.Join(parts, " "))
}

func (g *gen) errorStmt() string {
	ctor := g.one("Error", "TypeError", "RangeError", "SyntaxError", "ReferenceError", "URIError", "EvalError")
	parts := []string{
		"const e = new " + ctor + "(" + g.one("\"m\"", "undefined", "42", "\"\"", g.expr(0)) + g.opt(", {cause: "+g.expr(0)+"}") + ");",
		"P(" + g.nextLabel() + ", e.name, e.message, e.cause, \"cause\" in e, e instanceof Error, e instanceof " + ctor + ", String(e), Object.keys(e), JSON.stringify(e));",
		"const ae = new AggregateError([1, new Error(\"x\")], \"agg\");",
		"P(" + g.nextLabel() + ", ae.name, ae.message, ae.errors, ae instanceof Error);",
		"P(" + g.nextLabel() + ", T(() => null.x), T(() => undefined()), T(() => ({}).x.y), T(() => " + g.fresh("missing") + "), T(() => new Array(-1)), T(() => (1).toFixed(200)), T(() => JSON.parse(\"{\")), T(() => decodeURIComponent(\"%\")));",
	}
	return g.wrapTry(strings.Join(parts, " "))
}

var uriTexts = []string{"a b&c=d/é?x#y", "%41%zz", "😀", "100%", "~!*()'", "%E6%97%A5", "%C0%AF", "a+b", "[]", ""}

func (g *gen) builtinsStmt() string {
	u := jsq(uriTexts[g.intn(len(uriTexts))])
	l := g.nextLabel()
	switch g.intn(5) {
	case 0:
		return fmt.Sprintf("P(%s, T(() => encodeURIComponent(%s)), T(() => encodeURI(%s)), T(() => decodeURIComponent(%s)), T(() => decodeURI(%s)));", l, u, u, u, u)
	case 1:
		return "P(" + l + ", String.raw`a\\n${" + g.expr(1) + "}b\\u{41}`, `x${" + g.expr(1) + "}\\ty`.length);"
	case 2:
		return "P(" + l + ", T(() => structuredClone(" + g.expr(1) + ")), T(() => Object.groupBy(" + g.arrayLit(1) + ", " + g.callback(1, "x", "i") + ")), T(() => Array.from(" + g.strLit() + ", " + g.callback(1, "x", "i") + ")));"
	case 3:
		return "P(" + l + ", Number.isSafeInteger(" + g.expr(0) + "), Number.MAX_SAFE_INTEGER + 2, Number.EPSILON > 0, Number.MIN_VALUE, Number.isFinite(" + g.expr(0) + "), [NaN].includes(NaN), [NaN].indexOf(NaN), [-0].includes(0), Object.is(" + g.expr(0) + ", " + g.expr(0) + "));"
	}
	return "P(" + l + ", [..." + g.strLit() + "], Array.from(\"a😀b\").length, \"😀\".codePointAt(0), String.fromCodePoint(" + g.one("0x1F600", "65", "0x10FFFF", "1114112", "(-1)", "1.5") + "), T(() => String.fromCharCode(" + g.one("65, 66", "0x1F600", "(-1)", "65.9") + ")), " + g.strLit() + ".normalize === undefined);"
}

func (g *gen) spreadStmt() string {
	a := g.arrayLit(1)
	l := g.nextLabel()
	parts := []string{
		"const arr = " + a + ";",
		"P(" + l + ", Math.max(...arr), Math.min(...arr, 0), [...new Set(arr)], Array.from(new Map([[1, 2]])), [...arr, ...\"ab\"].length, ((...xs) => xs.length)(...arr), Object.keys({...arr}));",
		"const [h, ...t] = arr; const {length: n} = arr;",
		"P(" + g.nextLabel() + ", h, t, n, [...arr].sort(), [...arr].reverse().join(\"|\"), arr.toString(), String(arr), arr + \"\");",
	}
	return g.wrapTry(strings.Join(parts, " "))
}

func (g *gen) equalityStmt() string {
	a, b := g.expr(1), g.expr(1)
	return fmt.Sprintf("P(%s, T(() => [%s == %s, %s === %s, %s < %s, %s >= %s, Object.is(%s, %s), typeof %s, typeof %s]), [10, 9, 1, \"b\", undefined, \"a\", null, \"B\", true].sort(), \"a\" < \"B\", \"10\" < \"9\", [2] == 2, null == 0, undefined == null, null >= 0, NaN != NaN, \"\" == 0, \"0\" == false);",
		g.nextLabel(), a, b, a, b, a, b, a, b, a, b, a, b)
}

func (g *gen) counterStmt() string {
	parts := []string{
		"const counter = (() => { let c = " + g.expr(0) + "; return {inc: () => ++c, add: (n = 1) => (c += n), get: () => c}; })();",
		"counter.inc(); T(() => counter.add(" + g.expr(0) + ")); counter.add();",
		"const fns = {}; for (const name of [\"x\", \"y\", \"z\"]) fns[name] = () => name + counter.get();",
		"P(" + g.nextLabel() + ", counter.get(), fns.x(), fns.z(), Object.keys(fns));",
	}
	return g.wrapTry(strings.Join(parts, " "))
}
