import json, os, sys

OUT = sys.argv[1]
os.makedirs(OUT, exist_ok=True)
items = []


def add(group, code):
    items.append((group, code.strip("\n") + "\n"))


def each(group, exprs, form="log"):
    for e in exprs:
        if form == "log":
            add(group, "console.log(%s)" % e)
        elif form == "str":
            add(group, "console.log(String(%s))" % e)
        elif form == "ret":
            add(group, e)
        elif form == "json":
            add(group, "console.log(JSON.stringify(%s))" % e)


nums = ["0.1 + 0.2", "-0", "0 * -1", "1 / 3", "2 / 3", "10 / 3", "1e21", "1e20", "123456789012345680000", "1e-6", "1e-7", "1.5e-7", "123e-20", "2 ** 53", "2 ** 53 + 1", "2 ** 64", "-(2 ** 31)", "2 ** 31 - 1", "Number.MAX_SAFE_INTEGER", "Number.MIN_SAFE_INTEGER", "Number.MAX_VALUE", "Number.MIN_VALUE", "Number.EPSILON", "NaN", "Infinity", "-Infinity", "0.000001", "0.0000001", "100", "1e2", "0x1f", "0o17", "0b1011", "1_000_000", ".5", "5.", "0.1 * 3", "0.3 - 0.1", "9.95", "1.005 * 1000", "4.35 * 100", "1 / 0", "-1 / 0", "0 / 0", "5e-324", "1.7976931348623157e308", "123.456e5", "-1e-7", "3.0", "1.10", "100 / 7", "Math.PI", "Math.E", "Math.SQRT2", "0.1 + 0.7", "1.1 * 1.1", "4 / 2", "7 % -3", "-7 % 3", "5.5 % 2", "2 ** -1", "(-8) ** (1 / 3)", "2 ** 0.5", "10 ** 21", "10 ** -7", "12345678.9", "1234567890123456789", "0.1 ** 2", "Number('12px')", "Number('  42  ')", "Number('')", "Number(' ')", "Number('0x10')", "Number('1e3')", "Number(null)", "Number(undefined)", "Number([])", "Number([7])", "Number([1, 2])", "Number(true)", "Number('Infinity')", "Number('-0')", "Number('.5')", "Number('5.')", "Number('1_000')", "Number('0b101')", "Number('+12')", "Number('--1')"]
each("num-string", nums, "str")
fixed = [("1.005", 2), ("1.45", 1), ("1.55", 1), ("2.5", 0), ("-2.5", 0), ("0.5", 0), ("1.5", 0), ("123.456", 2), ("0", 2), ("-0", 2), ("1e21", 2), ("1e-10", 3), ("0.000001", 7), ("99.995", 2), ("8.345", 2), ("1234.5678", 0), ("-1.005", 2), ("10.235", 2), ("0.1", 20), ("3", 5), ("NaN", 2), ("Infinity", 1), ("2 ** 70", 2), ("0.045", 2), ("1.255", 2)]
for v, d in fixed:
    add("num-toFixed", "console.log((%s).toFixed(%d))" % (v, d))
prec = [("123.456", 4), ("0.00012345", 2), ("123456", 2), ("1.5", 1), ("2.5", 1), ("1e21", 3), ("-0.0001", 1), ("99.99", 3), ("1", 5), ("0", 3)]
for v, d in prec:
    add("num-toPrecision", "console.log((%s).toPrecision(%d))" % (v, d))
expo = [("123456", 2), ("0.00015", 1), ("1", None), ("12345.6789", 3), ("-0.5", 0), ("1e-7", None)]
for v, d in expo:
    add("num-toExponential", "console.log((%s).toExponential(%s))" % (v, "" if d is None else d))
radix = [("255", 16), ("255", 2), ("-255", 36), ("0.5", 2), ("3.75", 16), ("1e21", 36), ("35", 36), ("2 ** 53", 32), ("-0", 2), ("0.1", 3)]
for v, r in radix:
    add("num-radix", "console.log((%s).toString(%d))" % (v, r))
pi = ["'08'", "'0x1A'", "'1e3'", "'  12abc'", "'-0'", "''", "'abc'", "'3.99'", "'-3.99'", "'0b11'", "'  +7 '", "'Infinity'", "'1_000'", "'9007199254740993'", "'123', 8", "'ff', 16", "'z', 36", "'10', 2", "'10', 1", "'10', 37", "null", "'0.0000005'", "0.0000005", "1e21", "'٣'"]
for a in pi:
    add("parseInt", "console.log(String(parseInt(%s)))" % a)
pf = ["'3.14abc'", "'.5'", "'-.5e-3x'", "'1e1000'", "'Infinity1'", "'  2.50  '", "'0x10'", "''", "'1.2.3'", "'-0'"]
for a in pf:
    add("parseFloat", "console.log(String(parseFloat(%s)))" % a)
neg0 = ["Object.is(-0, 0)", "Object.is(0 * -1, -0)", "-0 === 0", "1 / -0", "JSON.stringify(-0)", "JSON.stringify([-0])", "String(-0)", "(-0).toString()", "`${-0}`", "[-0].includes(0)", "[-0].indexOf(0)", "new Set([0, -0]).size", "Math.round(-0.4)", "Object.is(Math.round(-0.4), -0)", "Math.sign(-0)", "Object.is(Math.sign(-0), -0)", "Math.max(-0, 0)", "Object.is(Math.min(0, -0), -0)", "-0 < 0", "(-0).toFixed(1)", "Object.is(parseFloat('-0'), -0)", "Object.is(-0 + 0, 0)"]
each("neg-zero", neg0, "str")
maths = ["Math.round(-0.5)", "Math.round(2.5)", "Math.round(-2.5)", "Math.round(0.49999999999999994)", "Math.max()", "Math.min()", "Math.max(1, NaN)", "Math.max('3', 2)", "Math.trunc(-4.7)", "Math.sign(-3)", "Math.hypot(3, 4)", "Math.cbrt(27)", "Math.log10(1000)", "Math.log2(8)", "Math.floor(-0.5)", "Math.ceil(-0.5)", "Math.abs(-7.25)", "Math.sqrt(-1)", "Math.pow(2, 10)", "Math.atan2(1, 1)", "Math.sin(1)", "Math.exp(1)", "Math.log(10)", "Math.fround(5.5)", "Math.fround(5.05)", "Math.clz32(1)", "Math.imul(3, 4)", "Math.expm1(1e-10)", "Math.cos(Math.PI)", "Math.tan(0.5)"]
each("math", maths, "str")

strs = ["'héllo wörld'.toUpperCase()", "'ß'.toUpperCase()", "'İ'.toLowerCase().length", "'ǅ'.toLowerCase()", "'Σ'.toLowerCase()", "'ÀÉÎ'.toLowerCase()", "'a😀b'.length", "[...'a😀b'].length", "Array.from('a😀b').length", "'a😀b'.split('').length", "'😀'.charCodeAt(0)", "'😀'.charCodeAt(1)", "'😀'.codePointAt(0)", "'😀'.codePointAt(1)", "'a😀b'.slice(1, 2).length", "'a😀b'.at(-1)", "'naïve café'.indexOf('café')", "'日本語テキスト'.slice(2, 4)", "'日本語'.length", "'abc'.padStart(6, '😀').length", "'x'.padEnd(5, 'ab')", "'abc'.padStart(2)", "'résumé'.replaceAll('é', 'e')", "'\\u00a0 trim me \\u2003'.trim()", "'\\ufefftext'.trim().length", "'Ünïcödé'.normalize('NFD').length", "'é' === 'e\\u0301'", "'é'.normalize('NFD') === 'e\\u0301'", "String.fromCharCode(0xD83D, 0xDE00)", "String.fromCodePoint(0x1F600)", "String.fromCharCode(65, 66, 67)", "encodeURIComponent('é ü/?&=')", "encodeURI('http://x.y/a b?q=é&r=1#f')", "decodeURIComponent('%E2%9C%93')", "escape === undefined", "'abc'.substring(2, 0)", "'abc'.substr(-2, 1)", "'abcabc'.lastIndexOf('c', 4)", "'a,b,,c'.split(',')", "'a,b,c'.split(',', 2)", "''.split(',')", "''.split('')", "'abc'.split(undefined)", "'Hello'.at(10)", "'aaa'.replace('a', 'b')", "'aaa'.replaceAll('a', '$&$&')", "'x-y'.replace('-', '$$')", "'abc'.repeat(0)", "'ab'.repeat(3)", "'Straße'.length", "'İstanbul'.toLowerCase().length", "'ﬀ'.toUpperCase()", "'abc'.localeCompare('abd')", "'a'.localeCompare('a')", "'b'.localeCompare('a')", "'abc'.includes('')", "'abc'.startsWith('b', 1)", "'abc'.endsWith('b', 2)", "'abc'.concat(1, null)", "'  x  '.trimStart() + '|'", "'  x  '.trimEnd() + '|'", "'tab\\there'.length", "'a\\u2028b'.length", "'\\x41\\u0042\\u{43}'", "'abc'[1]", "'abc'['length']", "'abc'.charAt(5) === ''", "'abc'.charCodeAt(5)", "'Z'.codePointAt(5)", "[...'👨‍👩‍👧'].length", "'👍🏽'.length", "'abc'.search('c')", "'abc'.match('b').index", "'%'.charCodeAt(0).toString(16)", "'éa'.split('').map(c => c.charCodeAt(0)).join(',')", "'aBc'.toLocaleUpperCase()", "'test'.isWellFormed()", "'\\ud800x'.isWellFormed()", "'\\ud800'.toWellFormed().charCodeAt(0)", "'a\\u0000b'.length", "JSON.stringify('😀')", "JSON.stringify('\\u2028\\u2029')", "JSON.stringify('\\u0007\\b\\f\\n\\r\\t\"\\\\')", "JSON.stringify('\\udc00')", "JSON.stringify('é')", "'😀'.split('')", "'a😀'.slice(-1).charCodeAt(0)"]
each("string", strs, "log")

sorts = [
    "const a = [{k: 1, v: 'a'}, {k: 0, v: 'b'}, {k: 1, v: 'c'}, {k: 0, v: 'd'}, {k: 1, v: 'e'}];\nconsole.log(a.sort((x, y) => x.k - y.k).map(x => x.v).join(''))",
    "const a = [];\nfor (let i = 0; i < 40; i++) a.push({g: i % 3, i});\nconsole.log(a.sort((x, y) => x.g - y.g).map(x => x.i).join(','))",
    "console.log([10, 9, 1, 100, 25].sort().join())",
    "console.log([10, 9, 1, 100, 25].sort((a, b) => a - b).join())",
    "console.log(['b', undefined, 'a', , 'c'].sort())",
    "console.log(JSON.stringify([3, undefined, 1, null, 2].sort()))",
    "console.log(['B', 'a', 'C', 'b'].sort().join())",
    "console.log(['B', 'a', 'C', 'b'].sort((x, y) => x.toLowerCase() < y.toLowerCase() ? -1 : x.toLowerCase() > y.toLowerCase() ? 1 : 0).join())",
    "const a = [3, 1, 2];\nconst b = a.toSorted();\nconsole.log(a.join(), b.join())",
    "console.log([5, 1, 4].sort(() => 0).join())",
    "console.log([1, 2, 3, 4, 5].sort((a, b) => b - a).join())",
    "const rows = [['x', 2, 'b'], ['y', 1, 'a'], ['z', 2, 'a'], ['w', 1, 'b']];\nrows.sort((p, q) => p[1] - q[1] || p[2].localeCompare(q[2]));\nconsole.log(rows.map(r => r[0]).join(''))",
    "console.log([0.1, -0, 0, -1, NaN, 1].sort((a, b) => a - b).map(String).join())",
    "console.log(['10', '9', '1'].sort((a, b) => a - b).join())",
    "console.log([true, false, true].sort().join())",
    "const a = [3, 2, 1];\nconst r = a.sort();\nconsole.log(r === a, a.join())",
    "console.log(['é', 'e', 'f', 'z', 'a'].sort().join())",
    "console.log([[2, 'b'], [1, 'a'], [2, 'a']].sort().join('|'))",
    "const a = Array.from({length: 12}, (_, i) => ({n: i % 4, i}));\nconsole.log(a.toSorted((x, y) => y.n - x.n).map(o => o.i).join(' '))",
    "console.log(['item10', 'item2', 'item1'].sort().join())",
    "const words = 'the quick brown fox jumps over the lazy dog'.split(' ');\nconsole.log(words.sort((a, b) => a.length - b.length).join(' '))",
    "console.log([1, 2, 3].reverse().concat([4]).sort((a, b) => b - a).join())",
]
for s in sorts:
    add("sort", s)
add("sort-impl-defined", "console.log([3, 1, 2].sort((a, b) => a > b).join())")
add("sort-impl-defined", "console.log(['é', 'f', 'e'].sort((a, b) => a.localeCompare(b)).join())")
add("sort-impl-defined", "console.log(['a', 'B', 'c'].sort((a, b) => a.localeCompare(b)).join())")

enum = [
    "const o = {b: 1, 2: 'two', a: 2, 1: 'one', '-1': 'neg', '01': 'lead'};\nconsole.log(Object.keys(o).join())",
    "const o = {};\no.z = 1; o[10] = 1; o.y = 1; o[2] = 1; o['1.5'] = 1;\nconsole.log(Object.keys(o).join())",
    "const o = {a: 1, b: 2, c: 3};\ndelete o.a;\no.a = 4;\nconsole.log(JSON.stringify(o))",
    "const a = [1, 2];\na.x = 'p';\nconsole.log(Object.keys(a).join(), JSON.stringify(a))",
    "const o = {4294967295: 'a', 4294967294: 'b', 1: 'c'};\nconsole.log(Object.keys(o).join())",
    "const p = {inherited: 1};\nconst o = Object.create(p);\no.own = 2;\nconst ks = [];\nfor (const k in o) ks.push(k);\nconsole.log(ks.join(), Object.keys(o).join())",
    "console.log(Object.entries({x: 1, 0: 'a', y: [2]}).map(([k, v]) => k + '=' + v).join(';'))",
    "console.log(JSON.stringify(Object.fromEntries([['b', 1], ['a', 2], ['1', 3]])))",
    "const o = {b: 1, a: 2};\nconsole.log(JSON.stringify({...o, c: 3, b: 9}))",
    "console.log(Object.keys('abc').join(), Object.values('hi').join())",
    "const o = Object.assign({}, {b: 1}, {a: 2}, {b: 3});\nconsole.log(JSON.stringify(o))",
    "const m = {};\n['c', 'a', 'b', '3', '1'].forEach(k => m[k] = k);\nconsole.log(Object.values(m).join(''))",
    "const o = {a: 1};\nObject.defineProperty(o, 'hidden', {value: 2, enumerable: false});\nconsole.log(Object.keys(o).join(), o.hidden, JSON.stringify(o))",
    "console.log(Object.getOwnPropertyNames([1, 2]).join())",
    "const counts = {};\nfor (const w of ['10', 'b', '2', 'a', '10', 'b']) counts[w] = (counts[w] || 0) + 1;\nconsole.log(JSON.stringify(counts))",
]
for s in enum:
    add("enum-order", s)

jsons = ["{a: undefined, b: 1}", "[undefined, function () {}, 1]", "{f() {}, n: null}", "[NaN, Infinity, -Infinity]", "{d: new Map([[1, 2]])}", "{s: new Set([1])}", "[[], {}, [[]]]", "'\\u0000'", "{'k\\n': 'v\\t'}", "Object.create(null)", "[1, [2, [3, [4]]]]", "{toJSON() { return 'custom' }}", "{a: {toJSON(k) { return 'key:' + k }}}", "true", "null", "undefined", "123e-20", "1e21", "[,1]", "new Error('x')", "{a: 1, b: [1, 2], c: {d: 'e'}}", "'😀\\ud83d'", "[Symbol('s')]"]
for j in jsons:
    add("json-stringify", "console.log(String(JSON.stringify(%s)))" % j)
indents = [("{a: [1, {b: 2}], c: 'x'}", "2"), ("{a: [], b: {}}", "2"), ("[1, [2, []]]", "'--'"), ("{a: 1}", "20"), ("{a: 1}", "'abcdefghijklmnop'"), ("{a: [1, 2]}", "0"), ("{a: 1, b: 2}", "-1"), ("[{}, []]", "'\\t'"), ("{a: {b: {c: {}}}}", "1")]
for v, ind in indents:
    add("json-indent", "console.log(JSON.stringify(%s, null, %s))" % (v, ind))
add("json-replacer", "console.log(JSON.stringify({a: 1, b: 2, c: {a: 3, d: 4}}, ['a', 'c']))")
add("json-replacer", "console.log(JSON.stringify({a: 1, b: 'x', c: [1, 'y']}, (k, v) => typeof v === 'number' ? v * 10 : v))")
add("json-replacer", "const seen = [];\nJSON.stringify({x: {y: [1]}}, function (k, v) { seen.push(JSON.stringify(k)); return v });\nconsole.log(seen.join(' '))")
add("json-replacer", "console.log(JSON.stringify({a: 1, b: 2}, (k, v) => k === 'a' ? undefined : v))")
add("json-cycle", "const o = {name: 'x'};\no.self = o;\ntry { JSON.stringify(o) } catch (e) { console.log(e.name, e instanceof TypeError) }")
add("json-cycle", "const a = [1];\na.push(a);\ntry { JSON.stringify(a) } catch (e) { console.log(e.message) }")
add("json-cycle", "const o = {};\no.o = o;\nJSON.stringify(o)")
parses = ["'{\"a\":1,\"a\":2}'", "'[1, 2, 3]'", "' \\n {\"x\" : [true, false, null]} '", "'\"\\\\u00e9\"'", "'1e3'", "'-0'", "'{\"__proto__\": 1}'", "'\"\\\\ud83d\\\\ude00\"'", "'[]'", "'123456789012345678901234567890'"]
for p in parses:
    add("json-parse", "console.log(JSON.stringify(JSON.parse(%s)))" % p)
badparse = ["'{a:1}'", "\"{'a':1}\"", "'[1,]'", "''", "'{\"a\":1'", "'NaN'", "'01'", "'\"\\t\"'", "'undefined'"]
for p in badparse:
    add("json-parse-error", "try { JSON.parse(%s); console.log('parsed') } catch (e) { console.log(e.name) }" % p)
    add("json-parse-error-msg", "try { JSON.parse(%s) } catch (e) { console.log(e.message) }" % p)
add("json-reviver", "console.log(JSON.stringify(JSON.parse('{\"a\":1,\"b\":{\"c\":2}}', (k, v) => typeof v === 'number' ? v + 1 : v)))")
add("json-reviver", "const order = [];\nJSON.parse('{\"a\":[1,2],\"b\":{\"c\":3}}', (k, v) => { order.push(k); return v });\nconsole.log(order.join(','))")
add("json-reviver", "console.log(JSON.stringify(JSON.parse('[1,2,3]', (k, v) => v === 2 ? undefined : v)))")

closures = [
    "function f() {\n  const fs = [];\n  for (let i = 0; i < 3; i++) fs.push(() => i);\n  return fs.map(g => g()).join();\n}\nconsole.log(f())",
    "function f() {\n  const fs = [];\n  for (var i = 0; i < 3; i++) fs.push(() => i);\n  return fs.map(g => g()).join();\n}\nconsole.log(f())",
    "function f() {\n  const fs = [];\n  for (const x of ['a', 'b']) fs.push(() => x);\n  return fs.map(g => g()).join();\n}\nconsole.log(f())",
    "function f() {\n  const fs = [];\n  for (const k in {p: 1, q: 2}) fs.push(() => k);\n  return fs.map(g => g()).join();\n}\nconsole.log(f())",
    "function f() {\n  const fs = [];\n  for (let i = 0; i < 3; i++) { fs.push(() => i); i++; }\n  return fs.map(g => g()).join();\n}\nconsole.log(f())",
    "function counter() { let n = 0; return {inc: () => ++n, get: () => n} }\nconst c = counter();\nc.inc(); c.inc();\nconsole.log(c.get())",
    "function f() {\n  let x = 1;\n  const g = () => x;\n  x = 2;\n  return g();\n}\nconsole.log(f())",
    "const adders = [1, 2, 3].map(n => m => n + m);\nconsole.log(adders.map(a => a(10)).join())",
    "function f() {\n  const fs = [];\n  let i = 0;\n  while (i < 3) { let j = i; fs.push(() => j); i++; }\n  return fs.map(g => g()).join();\n}\nconsole.log(f())",
    "function outer() { var a = 'outer'; function inner() { return a } var a = 'reassigned'; return inner() }\nconsole.log(outer())",
    "function f() { return typeof hoisted; var hoisted = 1 }\nconsole.log(f())",
    "function f() { return g(); function g() { return 'hoisted fn' } }\nconsole.log(f())",
    "const memo = (fn => { const c = new Map(); return n => c.has(n) ? c.get(n) : (c.set(n, fn(n)), c.get(n)) })(n => n * n);\nconsole.log(memo(4), memo(4))",
    "function f() {\n  const out = [];\n  for (let i = 0, n = 3; i < n; i++) setTimeout(() => out.push(i), 0);\n  return new Promise(r => setTimeout(() => r(out.join()), 5));\n}\nreturn await f()",
]
for s in closures:
    add("closure", s)
tdz = [
    "function f() { try { x; let x = 1 } catch (e) { return e.name } }\nconsole.log(f())",
    "function f() { try { return typeof y; let y } catch (e) { return e.name } }\nconsole.log(f())",
    "{\n  try { console.log(z) } catch (e) { console.log(e.name) }\n  const z = 1;\n}",
    "function f() { const c = 1; try { c = 2 } catch (e) { return e.name } }\nconsole.log(f())",
    "function f() { let a = a + 1 }\ntry { f() } catch (e) { console.log(e.name) }",
    "function f(a = b, b = 1) { return a }\ntry { f() } catch (e) { console.log(e.name) }",
    "function f() { x; let x }\nf()",
    "function f() { const c = 1; c = 2 }\nf()",
    "function f() { const c = 1; try { c++ } catch (e) { return e.message } }\nconsole.log(f())",
]
for s in tdz:
    add("tdz", s)
labels = [
    "const out = [];\nouter: for (let i = 0; i < 3; i++) {\n  for (let j = 0; j < 3; j++) {\n    if (j === 1) continue outer;\n    if (i === 2) break outer;\n    out.push(i + '' + j);\n  }\n}\nconsole.log(out.join())",
    "let n = 0;\nblock: {\n  n++;\n  if (n) break block;\n  n = 100;\n}\nconsole.log(n)",
    "const found = [];\nsearch: for (const row of [[1, 2], [3, 4], [5, 6]]) {\n  for (const v of row) {\n    if (v === 4) { found.push(v); break search }\n  }\n}\nconsole.log(found.join())",
    "let i = 0;\nloop: while (true) { i++; do { if (i > 3) break loop; continue loop } while (false) }\nconsole.log(i)",
]
for s in labels:
    add("label", s)
switches = [
    "function f(x) { const r = []; switch (x) { case 1: r.push(1); case 2: r.push(2); break; case 3: r.push(3); default: r.push('d') } return r.join() }\nconsole.log(f(1), f(2), f(3), f(9))",
    "function f(x) { switch (x) { default: return 'd'; case 'a': return 'A' } }\nconsole.log(f('a'), f('b'))",
    "function f(x) { switch (x) { case '1': return 'string'; case 1: return 'number' } }\nconsole.log(f(1), f('1'))",
    "function f(x) { const r = []; switch (x) { case 0: default: r.push('d'); case 1: r.push(1) } return r.join() }\nconsole.log(f(0), f(1), f(5))",
    "let s = '';\nfor (const v of [1, 2, 3]) { switch (v) { case 2: continue; } s += v }\nconsole.log(s)",
    "function f(x) { switch (true) { case x < 0: return 'neg'; case x === 0: return 'zero'; default: return 'pos' } }\nconsole.log(f(-1), f(0), f(2))",
    "function f(x) { switch (x) { case NaN: return 'nan'; default: return 'other' } }\nconsole.log(f(NaN))",
]
for s in switches:
    add("switch", s)
finals = [
    "function f() { try { return 'try' } finally { return 'finally' } }\nconsole.log(f())",
    "function f() { try { throw new Error('x') } catch (e) { return 'catch' } finally { console.log('cleanup') } }\nconsole.log(f())",
    "function f() { let i = 0; try { return i } finally { i = 5 } }\nconsole.log(f())",
    "function f() { for (const x of [1, 2, 3]) { try { if (x === 2) break } finally { console.log('fin', x) } } return 'done' }\nconsole.log(f())",
    "function f() { try { throw 1 } finally { return 'swallowed' } }\nconsole.log(f())",
    "function f() { try { try { throw new Error('inner') } finally { console.log('inner finally') } } catch (e) { return e.message } }\nconsole.log(f())",
    "try { JSON.parse('{') } catch { console.log('no binding') }",
    "function f() { try { throw new Error('a') } catch (e) { throw new Error('b: ' + e.message) } }\ntry { f() } catch (e) { console.log(e.message) }",
    "try { try { throw new TypeError('t') } finally { throw new RangeError('r') } } catch (e) { console.log(e.name, e.message) }",
    "function f() { try { return 1 } finally { console.log('runs before return value is used') } }\nconsole.log(f() + 1)",
    "const r = [];\nfor (let i = 0; i < 3; i++) { try { if (i === 1) continue; r.push(i) } finally { r.push('f' + i) } }\nconsole.log(r.join())",
    "try { throw {code: 42} } catch (e) { console.log(e.code, typeof e) }",
    "try { throw null } catch (e) { console.log(e === null) }",
    "const e = new Error('outer', {cause: new Error('root')});\nconsole.log(e.message, e.cause.message)",
    "class MyErr extends Error { constructor(m) { super(m); this.name = 'MyErr' } }\ntry { throw new MyErr('bad') } catch (e) { console.log(e.name, e.message, e instanceof Error, e instanceof MyErr) }",
    "const e = new TypeError('t');\nconsole.log(String(e), e.toString(), Object.prototype.toString.call(e))",
    "const e = new Error('m');\ne.name = 'Custom';\nconsole.log(String(e))",
    "console.log(String(new Error()), String(new RangeError('')))",
    "throw new RangeError('out of range here')",
    "const x = 1;\n\nthrow new Error('line three')",
    "throw 'a string'",
    "throw {message: 'plain object'}",
    "async function f() { throw new SyntaxError('custom syntax') }\nawait f()",
    "const err = new AggregateError([new Error('a'), new Error('b')], 'many');\nconsole.log(err.name, err.message, err.errors.length)",
]
for s in finals:
    add("try-finally", s)

proms = [
    "const log = [];\nPromise.resolve().then(() => log.push('a')).then(() => log.push('b'));\nPromise.resolve().then(() => log.push('c')).then(() => log.push('d'));\nawait null; await null; await null;\nconsole.log(log.join(''))",
    "const log = [];\nlog.push(1);\nqueueMicrotask(() => log.push(3));\nPromise.resolve().then(() => log.push(4));\nlog.push(2);\nawait new Promise(r => setTimeout(r, 0));\nconsole.log(log.join())",
    "const log = [];\nsetTimeout(() => log.push('timeout'), 0);\nPromise.resolve().then(() => log.push('micro'));\nlog.push('sync');\nawait new Promise(r => setTimeout(r, 1));\nconsole.log(log.join())",
    "const log = [];\nasync function a() { log.push('a1'); await null; log.push('a2') }\nasync function b() { log.push('b1'); await null; log.push('b2') }\na(); b(); log.push('main');\nawait null; await null;\nconsole.log(log.join())",
    "const log = [];\nasync function f() { return 1 }\nasync function g() { return Promise.resolve(1) }\ng().then(() => log.push('g'));\nf().then(() => log.push('f'));\nawait new Promise(r => setTimeout(r, 0));\nconsole.log(log.join())",
    "const log = [];\nconst thenable = {then(r) { log.push('then called'); r(5) }};\nconst v = await thenable;\nconsole.log(log.join(), v)",
    "const log = [];\nnew Promise(r => { log.push('executor'); r() }).then(() => log.push('then'));\nlog.push('after');\nawait null;\nconsole.log(log.join())",
    "const order = [];\nconst ps = [30, 10, 20].map(ms => new Promise(r => setTimeout(() => { order.push(ms); r(ms) }, ms)));\nconst all = await Promise.all(ps);\nconsole.log(order.join(), all.join())",
    "const r = await Promise.race([new Promise(r => setTimeout(() => r('slow'), 20)), new Promise(r => setTimeout(() => r('fast'), 5))]);\nconsole.log(r)",
    "const s = await Promise.allSettled([Promise.resolve(1), Promise.reject(new Error('no')), 3]);\nconsole.log(JSON.stringify(s.map(x => x.status + ':' + (x.value ?? x.reason.message))))",
    "const v = await Promise.any([Promise.reject(new Error('a')), Promise.resolve('b'), Promise.resolve('c')]);\nconsole.log(v)",
    "try { await Promise.any([Promise.reject(new Error('a')), Promise.reject(new Error('b'))]) } catch (e) { console.log(e.name, e.errors.map(x => x.message).join()) }",
    "const log = [];\nPromise.reject(new Error('x')).catch(e => log.push('caught ' + e.message)).finally(() => log.push('finally')).then(v => log.push('then ' + v));\nawait new Promise(r => setTimeout(r, 0));\nconsole.log(log.join(' | '))",
    "const v = await Promise.resolve(1).finally(() => 2);\nconsole.log(v)",
    "try { await Promise.resolve(1).finally(() => { throw new Error('from finally') }) } catch (e) { console.log(e.message) }",
    "const {promise, resolve} = Promise.withResolvers();\nsetTimeout(() => resolve('resolved later'), 3);\nconsole.log(await promise)",
    "const log = [];\nconst p = Promise.resolve();\np.then(() => { log.push(1); Promise.resolve().then(() => log.push(3)) }).then(() => log.push(4));\np.then(() => log.push(2));\nawait new Promise(r => setTimeout(r, 0));\nconsole.log(log.join())",
    "const log = [];\n(async () => { try { await Promise.reject(new Error('inner')) } catch (e) { log.push(e.message) } finally { log.push('fin') } })();\nawait null; await null; await null;\nconsole.log(log.join())",
    "async function fail() { await null; throw new TypeError('async boom') }\ntry { await fail() } catch (e) { console.log(e.name, e.message) }",
    "async function fail() { throw new Error('before await') }\nconst p = fail();\nconsole.log(p instanceof Promise);\ntry { await p } catch (e) { console.log(e.message) }",
    "async function step(n) { await null; if (n === 2) throw new Error('step ' + n); return n }\nconst results = [];\nfor (const n of [1, 2, 3]) { try { results.push(await step(n)) } catch (e) { results.push(e.message) } }\nconsole.log(results.join())",
    "const r = await Promise.all([]);\nconsole.log(JSON.stringify(r))",
    "try { await Promise.all([Promise.resolve(1), Promise.reject(new RangeError('second')), Promise.reject(new Error('third'))]) } catch (e) { console.log(e.name, e.message) }",
    "Promise.reject(new Error('nobody catches me'));\nconsole.log('cell finished')",
    "new Promise((_, rej) => setTimeout(() => rej(new Error('late rejection')), 2));\nconsole.log('done early')",
    "await Promise.reject(new Error('uncaught rejection'))",
    "const p = Promise.reject(new Error('handled later'));\nawait null;\ntry { await p } catch (e) { console.log('caught', e.message) }",
    "const log = [];\nconst p1 = new Promise(r => setTimeout(() => r('a'), 10));\nconst p2 = new Promise(r => setTimeout(() => r('b'), 10));\np2.then(v => log.push(v)); p1.then(v => log.push(v));\nawait Promise.all([p1, p2]);\nconsole.log(log.join())",
    "async function* gen() { yield 1; yield 2 }\nconst out = [];\nfor await (const v of gen()) out.push(v);\nconsole.log(out.join())",
    "const out = [];\nfor await (const v of [Promise.resolve('x'), 'y', new Promise(r => setTimeout(() => r('z'), 1))]) out.push(v);\nconsole.log(out.join())",
    "const sleepy = (ms, v) => new Promise(r => setTimeout(() => r(v), ms));\nconst t = [];\nawait Promise.all([sleepy(5, 'b').then(v => t.push(v)), sleepy(1, 'a').then(v => t.push(v)), sleepy(9, 'c').then(v => t.push(v))]);\nconsole.log(t.join(''))",
    "let calls = 0;\nconst limit = async (tasks, n) => { const out = []; let i = 0; const run = async () => { while (i < tasks.length) { const k = i++; calls++; out[k] = await tasks[k]() } }; await Promise.all(Array.from({length: n}, run)); return out };\nconst res = await limit([1, 2, 3, 4, 5].map(x => () => new Promise(r => setTimeout(() => r(x * x), 6 - x))), 2);\nconsole.log(res.join(), calls)",
    "const log = [];\nconst a = async () => { log.push('a start'); await new Promise(r => setTimeout(r, 2)); log.push('a end') };\nconst b = async () => { log.push('b start'); await null; log.push('b end') };\nawait Promise.all([a(), b()]);\nconsole.log(log.join(', '))",
    "const p = new Promise(r => r(Promise.resolve('nested')));\nconsole.log(await p)",
    "const results = await Promise.all([1, 2, 3].map(async x => { await null; return x * 2 }));\nresults",
    "const x = await 5;\nx + 1",
    "async function f() { return await new Promise(r => setTimeout(() => r('ok'), 1)) }\nconsole.log(await f())",
    "const order = [];\nawait Promise.all([3, 1, 2].map(async (n) => { await new Promise(r => setTimeout(r, n)); order.push(n) }));\nconsole.log(order.join())",
    "let resolveIt;\nconst p = new Promise(r => resolveIt = r);\nresolveIt('first'); resolveIt('second');\nconsole.log(await p)",
    "const p = Promise.resolve(1);\nconsole.log(Promise.resolve(p) === p)",
    "const v = await Promise.reject(new Error('x')).then(() => 'then', e => 'onRejected ' + e.message);\nconsole.log(v)",
]
for s in proms:
    add("promise", s)

maps = [
    "const m = new Map([[1, 'a'], [2, 'b']]);\nconst seen = [];\nfor (const [k] of m) { seen.push(k); if (k === 1) m.set(3, 'c') }\nconsole.log(seen.join())",
    "const m = new Map([[1, 'a'], [2, 'b'], [3, 'c']]);\nconst seen = [];\nm.forEach((v, k) => { seen.push(k); if (k === 1) m.delete(2) });\nconsole.log(seen.join())",
    "const s = new Set([1, 2, 3]);\nconst seen = [];\nfor (const v of s) { seen.push(v); if (v === 1) { s.delete(1); s.add(1) } if (seen.length > 5) break }\nconsole.log(seen.join())",
    "const s = new Set([NaN, NaN, 0, -0, '0']);\nconsole.log(s.size, [...s].map(String).join())",
    "const m = new Map();\nm.set('b', 1).set('a', 2).set('b', 3);\nconsole.log(JSON.stringify([...m]))",
    "const k = {};\nconst m = new Map([[k, 'obj']]);\nconsole.log(m.get(k), m.get({}), m.has(k))",
    "const m = new Map([['x', 1]]);\nconsole.log(JSON.stringify(m), JSON.stringify(Object.fromEntries(m)))",
    "const s = new Set('hello');\nconsole.log([...s].join(''), s.size)",
    "const a = new Set([1, 2, 3]), b = new Set([2, 3, 4]);\nconsole.log([...a].filter(x => b.has(x)).join(), [...new Set([...a, ...b])].join())",
    "const m = new Map([[3, 'c'], [1, 'a'], [2, 'b']]);\nconsole.log([...m.keys()].join(), [...m.values()].join(), [...m.entries()].map(e => e.join(':')).join())",
    "const m = new Map([['a', 1]]);\nm.delete('a');\nm.set('a', 2);\nm.set('b', 3);\nconsole.log([...m.keys()].join())",
    "const grouped = new Map();\nfor (const w of ['apple', 'avocado', 'banana', 'blueberry', 'cherry']) { const k = w[0]; if (!grouped.has(k)) grouped.set(k, []); grouped.get(k).push(w) }\nconsole.log(JSON.stringify([...grouped]))",
    "const s = new Set([1, 2]);\nconsole.log(typeof s.union === 'function' ? [...s.union(new Set([3]))].join() : 'no union')",
    "const m = new Map();\nm.set(NaN, 'nan');\nconsole.log(m.get(NaN), m.size)",
    "const s = new Set([[1], [1]]);\nconsole.log(s.size)",
    "const m = new Map(Object.entries({b: 2, a: 1}));\nconsole.log([...m].sort((x, y) => x[1] - y[1]).map(([k]) => k).join())",
    "const m = new Map([[1, 1]]);\nconsole.log(m.size, m.clear(), m.size)",
    "const wm = new WeakMap();\nconst key = {};\nwm.set(key, 'secret');\nconsole.log(wm.get(key), wm.has({}))",
    "const ws = new WeakSet();\nconst o = {};\nws.add(o);\nconsole.log(ws.has(o))",
    "console.log(JSON.stringify(Object.groupBy([1, 2, 3, 4, 5], n => n % 2 ? 'odd' : 'even')))",
    "console.log(JSON.stringify([...Map.groupBy(['aa', 'b', 'cc'], s => s.length)]))",
]
for s in maps:
    add("map-set", s)

iters = [
    "const range = {from: 1, to: 4, [Symbol.iterator]() { let c = this.from, t = this.to; return {next: () => c <= t ? {value: c++, done: false} : {value: undefined, done: true}} }};\nconsole.log([...range].join(), Array.from(range).length)",
    "class Bag { constructor() { this.items = ['x', 'y'] } *[Symbol.iterator]() { yield* this.items } }\nconsole.log([...new Bag()].join())",
    "function* nat() { let n = 0; while (true) yield n++ }\nconst out = [];\nfor (const v of nat()) { if (v > 4) break; out.push(v) }\nconsole.log(out.join())",
    "function* g() { const x = yield 1; console.log('got', x); yield x * 2 }\nconst it = g();\nconsole.log(it.next().value, it.next(5).value, it.next().done)",
    "function* g() { try { yield 1; yield 2 } finally { console.log('cleanup') } }\nfor (const v of g()) { console.log(v); break }",
    "const it = [1, 2, 3][Symbol.iterator]();\nit.next();\nconsole.log([...it].join())",
    "const s = Symbol('tag');\nconst o = {[s]: 1, a: 2};\nconsole.log(Object.keys(o).join(), JSON.stringify(o), s.description, s.toString())",
    "console.log(Symbol.for('k') === Symbol.for('k'), Symbol('k') === Symbol('k'), typeof Symbol.iterator)",
    "const iterable = {*[Symbol.iterator]() { yield 'a'; yield 'b' }};\nconst [first, ...rest] = iterable;\nconsole.log(first, rest.join())",
    "function* fib() { let [a, b] = [0, 1]; for (;;) { yield a; [a, b] = [b, a + b] } }\nconst f = [];\nfor (const x of fib()) { if (f.length === 10) break; f.push(x) }\nconsole.log(f.join(' '))",
    "const entries = Object.entries({a: 1, b: 2});\nfor (const [k, v] of entries) console.log(k, v)",
    "const str = 'a😀b';\nconst cps = [];\nfor (const ch of str) cps.push(ch.codePointAt(0).toString(16));\nconsole.log(cps.join(' '))",
    "console.log([...'héllo'].reverse().join(''))",
    "console.log(Array.from('😀😁', c => c.length).join())",
    "const m = new Map([['a', 1]]);\nconst it = m[Symbol.iterator]();\nconsole.log(JSON.stringify(it.next()), JSON.stringify(it.next()))",
    "const arr = ['x', 'y'];\nconsole.log([...arr.entries()].join('|'), [...arr.keys()].join())",
]
for s in iters:
    add("iterator", s)

dates = [
    "const d = new Date(0);\nconsole.log(d.toISOString(), d.getTime())",
    "const d = new Date(Date.UTC(2024, 1, 29, 12, 30, 45, 123));\nconsole.log(d.toISOString(), d.getUTCDay(), d.getUTCMonth())",
    "const d = new Date('2024-03-10T08:05:03Z');\nconsole.log(d.getUTCFullYear(), d.getUTCHours(), d.getUTCMinutes(), d.getUTCSeconds())",
    "const a = new Date('2024-01-01T00:00:00Z'), b = new Date('2024-03-01T00:00:00Z');\nconsole.log((b - a) / 86400000)",
    "const d = new Date(Date.UTC(2023, 11, 31));\nd.setUTCDate(d.getUTCDate() + 1);\nconsole.log(d.toISOString().slice(0, 10))",
    "console.log(new Date('not a date').getTime(), String(new Date(NaN).getTime()))",
    "console.log(Date.parse('2020-02-30T00:00:00Z'))",
    "console.log(JSON.stringify({at: new Date(Date.UTC(2020, 0, 2, 3, 4, 5))}))",
    "const d = new Date(1700000000000);\nconsole.log(d.toISOString())",
    "console.log(Date.UTC(1970, 0, 1), Date.UTC(2000, 0))",
    "const days = ['2024-05-01', '2024-04-30', '2024-05-02'].map(s => new Date(s + 'T00:00:00Z')).sort((a, b) => a - b);\nconsole.log(days.map(d => d.toISOString().slice(0, 10)).join())",
    "const d = new Date(Date.UTC(2024, 0, 31));\nd.setUTCMonth(1);\nconsole.log(d.toISOString().slice(0, 10))",
    "try { new Date(NaN).toISOString() } catch (e) { console.log(e.name) }",
    "console.log(new Date('2024-06-01T12:00:00.000Z').valueOf())",
    "console.log(new Date(Date.UTC(-1, 0)).getUTCFullYear(), new Date(8.64e15 + 1).getTime())",
]
for s in dates:
    add("date", s)

regexes = [
    "console.log('a1b22c333'.match(/\\d+/g).join())",
    "const re = /(\\w)(\\d)/g;\nlet m, out = [];\nwhile ((m = re.exec('a1 b2 c3')) !== null) out.push(m[1] + '@' + m.index + ':' + re.lastIndex);\nconsole.log(out.join())",
    "const re = /a/g;\nconsole.log(re.test('aa'), re.lastIndex, re.test('aa'), re.lastIndex, re.test('aa'))",
    "console.log('2024-05-06'.replace(/(\\d+)-(\\d+)-(\\d+)/, '$3/$2/$1'))",
    "console.log('john smith'.replace(/(?<first>\\w+) (?<last>\\w+)/, '$<last>, $<first>'))",
    "const m = '2024-05'.match(/(?<y>\\d{4})-(?<m>\\d{2})/);\nconsole.log(m.groups.y, m.groups.m, JSON.stringify(m.groups))",
    "console.log('aaa'.replace(/a/g, (m, off) => off))",
    "console.log('x1y2'.replace(/(\\d)/g, (m, d, off, s) => `[${d}${off}${s.length}]`))",
    "console.log('a,b;c d'.split(/[,; ]/).join('|'))",
    "console.log('a1b2c'.split(/(\\d)/).join('|'))",
    "console.log('abc'.split(/(?:)/).join('|'), 'abc'.split(/b/, 1).join())",
    "console.log('abc'.replace(/x*/g, '-'))",
    "console.log('price: $5.00'.replace(/\\$/g, '\\\\$'))",
    "console.log('Hello World'.replace(/o/gi, '0'), /HELLO/i.test('hello'))",
    "console.log('line1\\nline2\\nline3'.match(/^line\\d$/gm).length)",
    "console.log(/a.b/.test('a\\nb'), /a.b/s.test('a\\nb'))",
    "console.log('aaa'.match(/a+?/)[0], 'aaa'.match(/a{2,}/)[0], '<b><i>'.match(/<.+?>/)[0])",
    "console.log([...'a1b2c3'.matchAll(/[a-z](\\d)/g)].map(m => m[1] + m.index).join())",
    "const re = /foo/y;\nre.lastIndex = 3;\nconsole.log(re.test('barfoo'), re.lastIndex, re.test('barfoo'))",
    "console.log('price 100 dollars 200'.match(/\\d+(?= dollars)/)[0])",
    "console.log('a1 b2 c3'.replace(/[a-z](?!2)/g, 'X'))",
    "console.log('$10 €20 $30'.match(/(?<=\\$)\\d+/g).join())",
    "console.log('abab xyxy abcd'.match(/(\\w\\w)\\1/g).join())",
    "console.log('<a><b></a>'.match(/<(?<tag>\\w)>.*?<\\/\\k<tag>>/)[0])",
    "console.log(/^[\\w.+-]+@[\\w-]+\\.[\\w.]+$/.test('a.b+c@d-e.co.uk'), /^\\d{3}-\\d{4}$/.test('555-12345'))",
    "console.log('😀x'.match(/./u)[0].length, '😀x'.match(/./)[0].length)",
    "console.log(/\\u{1F600}/u.test('😀'), /^.$/u.test('😀'), /^.$/.test('😀'))",
    "const re = new RegExp('a.c', 'g');\nconsole.log(re.source, re.flags, re.global, 'abc adc'.match(re).length)",
    "const esc = s => s.replace(/[.*+?^${}()|[\\]\\\\]/g, '\\\\$&');\nconsole.log(esc('a.b*c(d)'), new RegExp(esc('1+1')).test('1+1=2'))",
    "console.log(String(/a\\/b/g), /x/gimsuy.flags)",
    "console.log('aBc'.replace(/b/i, m => m.toUpperCase() + m.toLowerCase()))",
    "console.log('  a  b  '.replace(/\\s+/g, ' ').trim())",
    "console.log('CamelCaseString'.replace(/([a-z])([A-Z])/g, '$1_$2').toLowerCase())",
    "console.log('snake_case_here'.replace(/_(\\w)/g, (_, c) => c.toUpperCase()))",
    "console.log('x'.replace('x', '$&$&$$$`$\\''))",
    "console.log('abc'.replace(/(z)?b/, '[$1]'))",
    "const m = /(a)|(b)/.exec('b');\nconsole.log(m[1], m[2], m.length)",
    "console.log('aaaa'.replaceAll(/aa/g, 'b'))",
    "try { 'a'.replaceAll(/a/, 'b') } catch (e) { console.log(e.name) }",
    "try { new RegExp('(') } catch (e) { console.log(e.name) }",
    "console.log(/[^]/.test('\\n'), /[]/.test('a'))",
    "console.log('tab\\there'.split(/\\t/).length, /\\bcat\\b/.test('concat cat'))",
    "console.log('A-B_C d'.split(/[-_\\s]/).map(s => s.toLowerCase()).join(''))",
    "const log = `2024-01-01 ERROR db: timeout\n2024-01-01 INFO web: ok\n2024-01-02 ERROR web: 500`;\nconst errs = [...log.matchAll(/^(\\S+) ERROR (\\w+): (.*)$/gm)].map(m => `${m[2]}=${m[3]}`);\nconsole.log(errs.join('; '))",
    "console.log(/(\\d+)\\s*(?:ms|s)\\b/.exec('took 125 ms')[1])",
    "console.log('a.b.c'.split('.', -1).length, 'aXbXc'.split('X', 0).length)",
    "const r = /(?<word>\\w+)/g;\nconsole.log('hi there'.replace(r, '<$<word>>'))",
    "console.log(/^\\p{Lu}/u.test('Éa'), '日本'.match(/\\p{Script=Han}/gu).length)",
    "console.log('ab'.match(/(?:a|ab)(?:c|b)?/)[0], 'ab'.search(/b/))",
    "const d = /(?<y>\\d+)/d.exec('ab12');\nconsole.log(d.indices[0].join(), d.indices.groups.y.join())",
]
for s in regexes:
    add("regex", s)

typeofs = ["undefined", "null", "0", "'s'", "true", "[]", "{}", "() => 1", "function () {}", "class {}", "Symbol()", "10n", "NaN", "new Number(1)", "Math", "JSON", "Promise.resolve()", "new Map()", "/x/", "new Date(0)", "undeclaredVariable", "globalThis", "async () => {}", "function* () {}", "Object.create(null)"]
for t in typeofs:
    add("typeof", "console.log(typeof (%s))" % t)
pairs = [("null", "undefined"), ("null", "0"), ("undefined", "0"), ("'0'", "false"), ("''", "false"), ("[]", "false"), ("[]", "''"), ("[0]", "false"), ("[1]", "1"), ("[1, 2]", "'1,2'"), ("NaN", "NaN"), ("'1'", "1"), ("' 1 '", "1"), ("'\\n'", "0"), ("true", "1"), ("true", "'1'"), ("true", "'true'"), ("{}", "'[object Object]'"), ("null", "false"), ("0", "-0"), ("'abc'", "new String('abc')"), ("Infinity", "'Infinity'"), ("[null]", "''"), ("[undefined]", "0"), ("'0x10'", "16")]
for a, b in pairs:
    add("equality", "console.log(%s == %s, %s === %s, Object.is(%s, %s))" % (a, b, a, b, a, b))
conv = ["[] + {}", "[] + []", "({}) + []", "'5' * '2'", "'5' + 2", "'5' - 2", "true + 1", "null + 1", "undefined + 1", "[2] * [3]", "`${[1, [2, 3]]}`", "String({})", "+[]", "+{}", "+''", "+' 12 '", "+'0x1f'", "+'1e3'", "'10' < '9'", "'10' < 9", "null >= 0", "null > 0", "null == 0", "undefined == 0", "'b' > 'a'", "'B' > 'a'", "[10] == 10", "1 < 2 < 3", "3 > 2 > 1", "'3' + 4 + 5", "3 + 4 + '5'", "-'3'", "~~'7.9'", "'7' | 0", "2 ** 3 ** 2", "(-2) ** 2", "!!'0'", "!!''", "!!NaN", "!![]", "[] == ![]", "'a' * 1", "0.1 * 3 === 0.3", "9007199254740992 + 1", "1 / '0'", "'' + null", "'' + undefined", "[1, 2] + [3]", "({a: 1}) + ''", "String([null, undefined, 1])", "String(Symbol('x'))", "[1, [2, [3]]].toString()", "Number(new Date(5))", "+true", "'' || 'default'", "0 ?? 'nullish'", "null ?? 'nullish'", "false || null || 0", "1 && 'x' && 0", "typeof (1 + '')", "parseInt(0.0000005)", "(1).constructor.name", "[,].length", "[, ,].length", "[1, , 3].length", "'abc' - 1", "Math.max('10', 9)", "[3, 20, 100].sort()[0]", "10 % 3.5", "5 / 2 | 0", "-7 >> 1", "-7 >>> 28", "1 << 31", "2 ** 32 | 0", "0.1 + 0.2 === 0.3", "(0.1 + 0.2).toFixed(10) === '0.3000000000'", "isNaN('abc')", "Number.isNaN('abc')", "isFinite('12')", "Number.isFinite('12')", "Number.isInteger(5.0)", "Number.isSafeInteger(2 ** 53)", "Number.parseFloat === parseFloat"]
each("conversion", conv, "str")

errs = [
    ("call-undefined", "undefined()"),
    ("call-missing-method", "const o = {};\no.missing()"),
    ("call-non-function", "const n = 5;\nn()"),
    ("read-undefined", "const u = undefined;\nu.x"),
    ("read-null", "const n = null;\nn.x"),
    ("read-deep", "const o = {a: {}};\no.a.b.c"),
    ("write-undefined", "const u = undefined;\nu.x = 1"),
    ("not-defined", "notDefinedAnywhere + 1"),
    ("reduce-empty", "[].reduce((a, b) => a + b)"),
    ("repeat-negative", "'abc'.repeat(-1)"),
    ("array-length", "new Array(-1)"),
    ("toFixed-range", "(1.5).toFixed(101)"),
    ("uri-error", "decodeURIComponent('%')"),
    ("not-iterable", "for (const x of 5) {}"),
    ("not-iterable-obj", "const [a] = {};"),
    ("destructure-undefined", "const {a} = undefined;"),
    ("destructure-null", "function f({x}) { return x }\nf(null)"),
    ("spread-non-iterable", "Math.max(...{})"),
    ("new-non-ctor", "const f = () => 1;\nnew f()"),
    ("in-on-primitive", "'x' in 'string'"),
    ("instanceof-non-callable", "({}) instanceof {}"),
    ("class-no-new", "class A {}\nA()"),
    ("symbol-to-string", "Symbol('s') + ''"),
    ("bigint-mix", "1n + 1"),
    ("const-assign", "function f() { const c = 1; c = 2 }\nf()"),
    ("freeze-strict", "'use strict';\nconst o = Object.freeze({a: 1});\no.a = 2;\nconsole.log(o.a)"),
    ("json-parse", "JSON.parse('{bad json}')"),
    ("invalid-date", "new Date(NaN).toISOString()"),
    ("map-on-undefined", "let items;\nitems.map(x => x)"),
    ("length-of-null", "function len(s) { return s.length }\nlen(null)"),
    ("call-number-method", "(5).toUpperCase()"),
    ("string-method-on-array", "[1, 2].split(',')"),
    ("await-rejected", "await Promise.reject(new TypeError('rejected with type'))"),
    ("stack-overflow", "function r() { return r() }\ntry { r() } catch (e) { console.log(e instanceof RangeError || e.name) }"),
    ("throw-in-callback", "[1, 2, 3].forEach(x => { if (x === 2) throw new Error('at two') })"),
    ("error-in-async-callback", "await Promise.all([1, 2].map(async x => { if (x === 2) null.foo }))"),
    ("throw-in-then", "await Promise.resolve(1).then(v => v.missing.deep)"),
    ("setter-only-getter", "const o = {get x() { return 1 }};\n'use strict';\no.x = 2;\nconsole.log(o.x)"),
    ("delete-nonconfigurable", "const a = [1];\nconsole.log(delete a.length, a.length)"),
    ("property-of-number-undefined", "const data = {items: [{n: 1}]};\nconsole.log(data.items[1].n)"),
]
for name, code in errs:
    lines = code.split("\n")
    body = "\n".join("  " + l for l in lines)
    add("err-uncaught", code)
    add("err-caught-name", "try {\n%s\n} catch (e) {\n  console.log(e.name, e instanceof Error, e.constructor === globalThis[e.name])\n}" % body)
    add("err-caught-message", "try {\n%s\n} catch (e) {\n  console.log(e.message)\n}" % body)
syntax = ["const a = ;", "let x = (1 + 2;", "function f( { }", "const s = 'unterminated;", "const t = `unterminated", "const r = /(/;", "if (x) {", "const 1x = 2;", "return return;", "let y = 1;\nlet y = 2;", "const o = {a: 1,, b: 2};", "x => { await 1 }\nconst f = function () { await sleep(1) }", "class { }", "var await = 1; let let = 2;", "obj?.a = 1;", "for (const i = 0; i < 2; i++) {}\nconst q = )", "a ?? b || c", "new.target", "({a: 1} = 1)", "import fs from 'fs'", "export const x = 1", "const {a, ...b, c} = {};", "1++", "'use strict'; with ({}) {}", "label: label: 1"]
for s in syntax:
    add("syntax-error", "const before = 1;\n" + s)

destr = [
    "const {a = 1, b: {c = 2} = {}, ...rest} = {x: 9, y: 8};\nconsole.log(a, c, JSON.stringify(rest))",
    "const [x, , y = 3, ...z] = [1, 2, undefined, 4, 5];\nconsole.log(x, y, z.join())",
    "let p = 1, q = 2;\n[p, q] = [q, p];\nconsole.log(p, q)",
    "function f({name, tags: [first = 'none'] = []} = {}) { return name + ':' + first }\nconsole.log(f({name: 'a', tags: ['t1']}), f({name: 'b'}), f())",
    "const key = 'dyn';\nconst {[key]: val, [`${key}2`]: v2 = 'dflt'} = {dyn: 42};\nconsole.log(val, v2)",
    "const [a, b] = 'hi';\nconst {length} = 'hello';\nconsole.log(a, b, length)",
    "const users = [{id: 1, profile: {name: 'Ann', langs: ['go', 'js']}}, {id: 2, profile: {name: 'Bo', langs: []}}];\nfor (const {id, profile: {name, langs: [main = '-']}} of users) console.log(id, name, main)",
    "const {a: {b: deep} = {b: 'default'}} = {};\nconsole.log(deep)",
    "const [[a1, a2], [b1]] = [[1, 2], [3]];\nconsole.log(a1 + a2 + b1)",
    "const o = {};\n({x: o.x, y: o['y']} = {x: 1, y: 2});\nconsole.log(JSON.stringify(o))",
    "const arr = [];\n[arr[1], arr[0]] = ['b', 'a'];\nconsole.log(arr.join())",
    "const {length: n, 0: first} = ['p', 'q'];\nconsole.log(n, first)",
    "const f = ([a, b] = [1, 2], {c} = {c: 3}) => a + b + c;\nconsole.log(f(), f([10, 20]), f(undefined, {c: 30}))",
    "const {x = (() => 'computed')()} = {x: undefined};\nconst {y = 'unused'} = {y: null};\nconsole.log(x, y)",
    "const entries = new Map([['a', {v: 1}], ['b', {v: 2}]]);\nconsole.log([...entries].map(([k, {v}]) => k + v).join())",
    "function sum(...nums) { return nums.reduce((a, b) => a + b, 0) }\nconsole.log(sum(), sum(1, 2, 3), sum(...[4, 5]))",
    "const [head, ...tail] = 'abc';\nconsole.log(head, JSON.stringify(tail))",
    "const {0: z, ...others} = ['a', 'b', 'c'];\nconsole.log(z, JSON.stringify(others))",
]
for s in destr:
    add("destructuring", s)

ops = [
    "const o = {a: {b: null}};\nconsole.log(o?.a?.b?.c, o.x?.y.z, o.a?.['b'], typeof o.f?.())",
    "let a = null, b = 0, c = '';\na ??= 'A'; b ||= 'B'; c &&= 'C';\nconsole.log(a, b, JSON.stringify(c))",
    "let n = 1;\nn **= 3; n += 2; n -= 1; n *= 2; n /= 4; n %= 3;\nconsole.log(n)",
    "let x = 5;\nconst r = (x++, x++, x);\nconsole.log(r, x)",
    "const o = {a: 1};\nconsole.log('a' in o, 'toString' in o, delete o.a, 'a' in o, delete o.missing)",
    "const arr = [1, 2, 3];\ndelete arr[1];\nconsole.log(arr.length, 1 in arr, JSON.stringify(arr))",
    "console.log(void 0, typeof void 'x')",
    "const t = (s, ...v) => s.raw.join('|') + v.join(',');\nconsole.log(t`a${1}b${2}c`)",
    "console.log(String.raw`C:\\new\\table ${1 + 1}`)",
    "const nested = `outer ${`inner ${1 + 1}`} end`;\nconsole.log(nested)",
    "const multi = `line1\nline2`;\nconsole.log(multi.split('\\n').length)",
    "const o = {x: 1, y: 2};\nconst copy = {...o, ...null, ...undefined, ...'ab'};\nconsole.log(JSON.stringify(copy))",
    "const merged = [...[1, 2], ...new Set([2, 3]), ...'xy'];\nconsole.log(merged.join())",
    "const fn = (a, b = a * 2, ...rest) => [a, b, rest.length];\nconsole.log(fn(1).join(), fn(1, undefined, 3, 4).join(), fn.length)",
    "const obj = {method() { return this.v }, v: 7, arrow: () => typeof this};\nconsole.log(obj.method(), obj.arrow())",
    "const k = 'dyn';\nconst o = {[k + 1]: 1, [`${k}2`]: 2, 3: 'three', 'quoted-key': 4};\nconsole.log(JSON.stringify(o))",
    "const a = 1, b = 2;\nconsole.log(JSON.stringify({a, b}))",
    "console.log(1_000 + 0.5e1, 0xff ^ 0x0f, 5 & 3, 5 | 3, ~5)",
    "const x = 0;\nconsole.log(x || 'or', x ?? 'nullish', x && 'and')",
    "function f() { return this === undefined ? 'undefined this' : typeof this }\nconsole.log(f())",
    "const bound = function (p) { return this.n + p }.bind({n: 10}, 5);\nconsole.log(bound(), bound.name)",
    "function greet(g, p) { return g + ' ' + this.name + p }\nconsole.log(greet.call({name: 'A'}, 'hi', '!'), greet.apply({name: 'B'}, ['yo', '?']))",
    "const f = function namedExpr() { return typeof namedExpr };\nconsole.log(f(), typeof namedExpr)",
    "console.log((function () { return typeof arguments })(), (function () { return arguments.length })(1, 2, 3))",
    "const getName = (f) => f.name;\nconst arrowFn = () => {};\nconsole.log(getName(arrowFn), getName(function () {}), getName(class K {}), (() => {}).name)",
    "console.log([1, 2, 3].includes(2) ? 'yes' : 'no', 2 > 1 ? 3 > 2 ? 'both' : 'first' : 'none')",
    "let i = 0;\ndo { i++ } while (i < 5);\nconsole.log(i)",
    "let s = '';\nfor (let i = 10; i--;) { if (i % 3) continue; s += i }\nconsole.log(s)",
    "const o = {a: 1, b: 2};\nlet keys = '';\nfor (const k in o) keys += k;\nfor (const k in [7, 8]) keys += k;\nfor (const k in 'xy') keys += k;\nconsole.log(keys)",
    "let total = 0;\nfor (const n of [1, 2, 3, 4, 5]) { if (n === 4) break; total += n }\nconsole.log(total)",
    "console.log(Array.isArray([]), Array.isArray('a'), Array.isArray({length: 0}))",
    "console.log(typeof globalThis, globalThis === this, typeof window)",
]
for s in ops:
    add("operators", s)

arrays = ["[1, [2, [3, [4]]]].flat()", "[1, [2, [3, [4]]]].flat(Infinity)", "[[1, 2], [3]].flatMap(x => x)", "[1, 2, 3].flatMap(x => [x, x * 2])", "[1, 2, 3].at(-1)", "[1, 2, 3].at(5)", "[1, 2, 3, 4].findLast(x => x % 2)", "[1, 2, 3, 4].findLastIndex(x => x > 9)", "[NaN].includes(NaN)", "[NaN].indexOf(NaN)", "new Array(3).fill(0)", "[1, 2, 3, 4, 5].copyWithin(0, 3)", "[1, 2, 3].fill(9, 1, 2)", "[1, 2, 3, 4].slice(-2)", "[1, 2, 3, 4].slice(1, -1)", "[null, undefined, 1].join('-')", "[[1, 2], [3, [4]]].toString()", "[1, 2, 3].reverse()", "[].every(x => false)", "[].some(x => true)", "[1, 2, 3].reduce((a, b) => a + b)", "[1, 2, 3].reduce((a, b) => a + b, 10)", "[[1, 2], [3, 4]].reduceRight((a, b) => a.concat(b))", "Array.from({length: 3}, (_, i) => i * i)", "Array.of(7)", "Array(7).length", "Array(1, 2)", "[...Array(3).keys()]", "[1, , 3].map(x => x * 2)", "Object.keys([1, , 3])", "[1, 2, 3].map(String)", "['1', '2', '3'].map(parseInt)", "['1', '2', '3'].map(Number)", "[1, 2, 3].concat(4, [5, [6]])", "[3, 1, 2].toReversed()", "[1, 2, 3].with(1, 9)", "[1, 2, 3, 4].toSpliced(1, 2, 'x')", "[1, 2, 3].indexOf(3, -1)", "[1, 2, 3].lastIndexOf(1, -3)", "[1, 2, 3].find(x => x > 5)", "[1, 2, 3].findIndex(x => x === 2)", "[0, 1, '', null, 'a'].filter(Boolean)", "[1, 2, 3].entries().next().value", "Array.from(new Map([[1, 2]]))", "Array.from('abc', c => c.toUpperCase())", "[5, 1, 10].sort().reverse()", "['a', 'b'].join()", "[].join()", "[undefined].join()", "[[]].join()", "[1, 2, 3].keys().next()", "Array.prototype.slice.call({0: 'a', 1: 'b', length: 2})", "[1, 2, 3].splice(1, 1)", "[...[1, 2, 3].values()].length", "[3, 2, 1].map((x, i, arr) => arr.length - i)", "Array.from({length: 2.5})", "[1, 2, 3, 4, 5].filter((x, i) => i % 2 === 0)", "[1, [2], [[3]]].flat(0)", "Array.from(new Set([3, 3, 1]))", "[1, 2, 3].includes('2')", "[[1, 'a'], [2, 'b']].map(([n, s]) => s + n)", "Object.entries(['x', 'y'])"]
for e in arrays:
    add("array", "console.log(JSON.stringify(%s))" % e)
arrmut = [
    "const a = [1, 2, 3, 4, 5];\nconst removed = a.splice(1, 2, 'x', 'y', 'z');\nconsole.log(a.join(), removed.join())",
    "const a = [1, 2, 3];\na.length = 1;\nconsole.log(a.join(), a.length)",
    "const a = [1];\na[4] = 5;\nconsole.log(a.length, JSON.stringify(a), a.indexOf(undefined))",
    "const a = [1, 2, 3];\nconst seen = [];\na.forEach((x, i) => { seen.push(x); if (i === 0) a.push(99) });\nconsole.log(seen.join(), a.length)",
    "const a = [1, 2, 3, 4];\nfor (const x of a) { if (x === 2) a.splice(0, 1) }\nconsole.log(a.join())",
    "const a = [1, 2, 3];\nconsole.log(a.push(4, 5), a.pop(), a.shift(), a.unshift(0), a.join())",
    "const a = ['a', 'b', 'c'];\nconst it = a.entries();\nconsole.log(JSON.stringify([...it]))",
    "const matrix = Array.from({length: 3}, (_, r) => Array.from({length: 3}, (_, c) => r * 3 + c));\nconsole.log(matrix.map(row => row.join(' ')).join('\\n'))",
    "const t = matrix => matrix[0].map((_, i) => matrix.map(r => r[i]));\nconsole.log(JSON.stringify(t([[1, 2, 3], [4, 5, 6]])))",
    "const chunk = (a, n) => a.length ? [a.slice(0, n), ...chunk(a.slice(n), n)] : [];\nconsole.log(JSON.stringify(chunk([1, 2, 3, 4, 5], 2)))",
    "const zip = (...as) => as[0].map((_, i) => as.map(a => a[i]));\nconsole.log(JSON.stringify(zip([1, 2], ['a', 'b'], [true, false])))",
    "const uniqBy = (a, f) => [...new Map(a.map(x => [f(x), x])).values()];\nconsole.log(JSON.stringify(uniqBy([{id: 1, v: 'a'}, {id: 2, v: 'b'}, {id: 1, v: 'c'}], x => x.id)))",
    "const a = [5, 3, 8];\nconst [min, max] = [Math.min(...a), Math.max(...a)];\nconsole.log(min, max, a.indexOf(max))",
    "const big = Array.from({length: 100000}, (_, i) => i);\nconsole.log(big.reduce((s, x) => s + x, 0), Math.max(...big.slice(0, 1000)))",
]
for s in arrmut:
    add("array-mut", s)
objs = [
    "const o = Object.freeze({a: 1, nested: {b: 2}});\no.a = 9;\no.nested.b = 3;\nconsole.log(o.a, o.nested.b, Object.isFrozen(o))",
    "const o = Object.seal({a: 1});\no.b = 2;\ndelete o.a;\nconsole.log(JSON.stringify(o), Object.isSealed(o))",
    "const proto = {greet() { return 'hi ' + this.name }};\nconst o = Object.create(proto, {name: {value: 'Zed', enumerable: true}});\nconsole.log(o.greet(), Object.getPrototypeOf(o) === proto, o.hasOwnProperty('greet'))",
    "function Animal(name) { this.name = name }\nAnimal.prototype.speak = function () { return this.name + ' speaks' };\nconst a = new Animal('Rex');\nconsole.log(a.speak(), a instanceof Animal, a.constructor === Animal)",
    "function Base() {}\nfunction Derived() { Base.call(this) }\nDerived.prototype = Object.create(Base.prototype);\nDerived.prototype.constructor = Derived;\nconst d = new Derived();\nconsole.log(d instanceof Base, d instanceof Derived, Object.getPrototypeOf(Derived.prototype) === Base.prototype)",
    "const o = {_v: 1, get v() { return this._v * 10 }, set v(x) { this._v = x }};\no.v = 5;\nconsole.log(o.v, JSON.stringify(o))",
    "const o = {};\nObject.defineProperty(o, 'computed', {get() { return 'from getter' }, enumerable: true});\nconsole.log(o.computed, Object.keys(o).join())",
    "class Temp { #c = 0; static count = 0; constructor(c) { this.#c = c; Temp.count++ } get f() { return this.#c * 9 / 5 + 32 } set f(v) { this.#c = (v - 32) * 5 / 9 } }\nconst t = new Temp(100);\nt.f = 212;\nconsole.log(t.f, Temp.count, Object.keys(t).length)",
    "class A { constructor() { this.x = 1 } m() { return 'A.m' } static s() { return 'static' } }\nclass B extends A { m() { return 'B>' + super.m() } }\nconst b = new B();\nconsole.log(b.m(), B.s(), b.x, b instanceof A, Object.getPrototypeOf(B) === A)",
    "class P { x = 1; y = this.x + 1; static z = 'zz' }\nconsole.log(JSON.stringify(new P()), P.z)",
    "class Stack { #items = []; push(x) { this.#items.push(x); return this } get size() { return this.#items.length } toString() { return `Stack(${this.#items})` } }\nconsole.log(String(new Stack().push(1).push(2)), new Stack().size)",
    "class Shape { area() { return 0 } toString() { return `${this.constructor.name}(${this.area()})` } }\nclass Sq extends Shape { constructor(s) { super(); this.s = s } area() { return this.s ** 2 } }\nconsole.log(`${new Sq(3)}`, [new Sq(2), new Shape()].map(String).join())",
    "class E extends Array { sum() { return this.reduce((a, b) => a + b, 0) } }\nconst e = E.from([1, 2, 3]);\nconsole.log(e.sum(), e instanceof E, e.length)",
    "const o = {a: 1, b: {c: 2}};\nconst clone = structuredClone(o);\nclone.b.c = 3;\nconsole.log(o.b.c, clone.b.c)",
    "const o = {toString() { return 'custom' }, valueOf() { return 42 }};\nconsole.log(`${o}`, o + 1, String(o), o * 2)",
    "const target = {a: 1};\nconst r = Object.assign(target, null, {b: 2});\nconsole.log(r === target, JSON.stringify(target))",
    "console.log(Object.entries({}).length, Object.keys(Object.create({inherited: 1})).length)",
    "const o = {n: 1};\nconst {n, ...copy} = {...o, extra: true};\nconsole.log(n, JSON.stringify(copy))",
    "console.log(Object.prototype.toString.call([]), Object.prototype.toString.call(null), Object.prototype.toString.call(new Date(0)))",
    "const o = Object.fromEntries(new Map([['a', 1]]));\nconsole.log(o.a, o.constructor === Object)",
    "const o = {a: 1};\nconsole.log(Object.hasOwn(o, 'a'), Object.hasOwn(o, 'toString'))",
    "const counter = {count: 0, inc() { this.count++; return this }};\nconsole.log(counter.inc().inc().count)",
    "const o = {f() { return [1, 2].map(function () { return this }, 'ctx').join() }};\nconsole.log(o.f())",
]
for s in objs:
    add("object", s)
fmt = [
    "console.log({a: 1, b: [1, 2]})",
    "console.log([1, 'two', null, true])",
    "console.log('x', 1, {y: 2}, [3])",
    "console.log(undefined)",
    "console.log(null)",
    "console.log()",
    "console.log(function f() {})",
    "console.log([undefined, () => 1])",
    "console.log(new Map([[1, 2]]))",
    "console.log(NaN, -0, 1e21)",
    "console.log('multi\\nline')",
    "console.log({nested: {deep: {deeper: [1, {x: 'y'}]}}})",
    "console.error('to error')",
    "console.warn('warn'); console.info('info'); console.debug('debug')",
    "print('print alias')",
    "({a: 1, b: 'two'})",
    "[1, 2, 3].map(x => x * 2)",
    "'plain string result'",
    "42",
    "undefined",
    "null",
    "const v = 5",
    "let r = 1;\nr + 1",
    "if (true) { 'inside if' }",
    "return {early: true}\nconsole.log('unreachable')",
    "console.log('a');\nreturn",
    "for (let i = 0; i < 2; i++) i * 10",
    "function f() {}",
    "[]",
    "({})",
    "''",
    "true",
    "1n",
    "Symbol('x')",
    "new Set([1])",
    "(() => {})",
    "NaN",
    "-0",
    "'a' + 'b';\n// trailing comment",
    "const x = 2;\nx * 21 // answer",
]
for s in fmt:
    add("output-format", s)

cells = [
    "const items = [3, 1, 2];\n//// cell\nitems.sort((a, b) => a - b);\nitems.join()",
    "const config = {retries: 3};\n//// cell\nconst config = {retries: 5};\nconfig.retries",
    "let counter = 0;\nfunction bump() { return ++counter }\n//// cell\nbump(); bump();\ncounter",
    "const data = await Promise.resolve([{n: 'a', v: 2}, {n: 'b', v: 5}]);\n//// cell\nconst total = data.reduce((s, d) => s + d.v, 0);\nconsole.log(total)\n//// cell\ndata.map(d => `${d.n}=${(d.v / total * 100).toFixed(1)}%`).join(' ')",
    "class Acc { constructor() { this.s = 0 } add(x) { this.s += x; return this } }\n//// cell\nnew Acc().add(2).add(3).s",
    "const x = 1;\nnotDefined.y;\n//// cell\ntypeof x",
    "var v = 1;\n//// cell\nvar v = v + 1;\nv",
    "const fmt = n => n.toFixed(2);\n//// cell\nconst fmt = n => '$' + n;\nfmt(3)",
    "const {a, b: [c]} = {a: 1, b: [2]};\n//// cell\na + c",
    "const m = new Map();\n//// cell\nm.set('k', 1);\n//// cell\n[...m.keys()].join()",
    "function helper() { return 'v1' }\n//// cell\nfunction helper() { return 'v2' }\nhelper()",
    "const r = await new Promise(res => setTimeout(() => res('later'), 2));\n//// cell\nr.toUpperCase()",
    "const list = [];\n//// cell\nfor (const i of [1, 2, 3]) list.push(i * i);\n//// cell\nlist",
    "const s = 'abc';\n//// cell\nconst s = s2 => s2;\ntypeof s",
    "let a = 1;\n//// cell\nlet a = 2;\na",
    "await Promise.reject(new Error('cell one fails'));\n//// cell\n'cell two still runs'",
    "const t = [1, 2];\n//// cell\nreturn t.length\n//// cell\nt.push(3);\nt",
]
for s in cells:
    add("multi-cell", s)

seen = set()
n = 0
for group, code in items:
    if code in seen:
        continue
    seen.add(code)
    n += 1
    open(os.path.join(OUT, "%s_%04d.js" % (group, n)), "w").write(code)
print(n)
