package harness

type VM interface {
	Register(name string, f func(a, b, c, d string)) error
	Eval(src string, async bool) error
	Call(fn string, args ...string) error
	Drain() error
	NativeAsync() bool
	Prepare(code string) (src string, async bool, fallback string)
	Interrupt()
	Reset()
	Close()
}

const prelude = `
globalThis.__p = new Map(); globalThis.__n = 0;
globalThis.__fmt = v => v === undefined ? "" : typeof v === "string" ? v : JSON.stringify(v, null, 1);
globalThis.__err = e => (e && typeof e === "object" && "stack" in e) ? (e.name || "Error") + ": " + e.message + "\n" + e.stack : "Thrown: " + String(e);
globalThis.__settle = (id, ok, v) => { const e = __p.get(id); if (!e) return; __p.delete(id); if (ok === "1") e[0](JSON.parse(v)); else { const err = new Error(v); err.name = "ToolError"; e[1](err); } };
globalThis.__abort = (msg) => { for (const [id, e] of __p) { const err = new Error(msg); err.name = "ToolError"; e[1](err); } __p.clear(); };
for (const name of ["sleep", "read", "bash", "mcp", "fail", "edit", "write"]) globalThis[name] = function __tool(...a) { const at = (new Error("x").stack || "").split("\n").filter(l => /^\s+at /.test(l) && !l.includes("__tool")).join("\n"); return new Promise(function __tool(res, rej) { const id = String(++__n); __p.set(id, [res, e => { e.stack = at; rej(e); }]); __host("call", id, name, JSON.stringify(a)); }); };
globalThis.setTimeout = (f, ms, ...a) => { sleep(ms || 0).then(() => f(...a)); return 0; }; globalThis.clearTimeout = () => {};
globalThis.queueMicrotask = f => { Promise.resolve().then(f); };
globalThis.structuredClone = v => JSON.parse(JSON.stringify(v));
globalThis.console = {}; for (const k of ["log", "error", "warn", "info", "debug"]) console[k] = (...a) => __host("out", a.map(x => typeof x === "string" ? x : __fmt(x)).join(" "), "", "");
globalThis.print = console.log;
`

func Wrap(code, mode string) (string, bool) {
	switch mode {
	case "iife":
		return "void (async () => {\n" + code + "\n})().then(v => __host('done', '1', __fmt(v), ''), e => __host('done', '0', __err(e), ''))", false
	case "native":
		return code, true
	}
	return code, false
}
