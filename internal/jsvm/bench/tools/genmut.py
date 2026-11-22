import glob, json, os, random, re, sys

SRC, OUT = sys.argv[1], sys.argv[2]
os.makedirs(OUT, exist_ok=True)
CAP = 12000

BIG = """const __bigIssues = n => {
  const states = ["Todo", "In Progress", "Done", "Backlog", "Canceled"], names = ["ana", "bo", "cy", "ana"], topics = ["auth", "billing", "search", "ui"];
  const nodes = [];
  for (let i = 0; i < n; i++) nodes.push({assignee: {name: names[i % 4]}, description: "Lorem ipsum dolor sit amet, consectetur adipiscing elit. ".repeat(8 + i % 5), estimate: i % 8, id: "uuid-" + String(i).padStart(6, "0"), identifier: "SER-" + (1000 + i), labels: ["a", "b"].slice(0, i % 3 % 2 + 1), priority: Math.floor(i / 5) % 5, state: {name: states[i % 5], type: "x"}, title: "Issue number " + i + " about " + topics[i % 4]});
  return {nodes, pageInfo: {hasNextPage: false}};
};
"""

FAKE = """let __i = 0;
const __used = new Set();
const __call = async (name, args) => {
  let k = __i < __tape.length && !__used.has(__i) && __tape[__i][0] === name ? __i : __tape.findIndex((e, j) => !__used.has(j) && e[0] === name);
  if (k < 0) { const err = new Error("no recorded result for " + name + " " + JSON.stringify(args)); err.name = "ToolError"; throw err }
  __used.add(k); __i = k + 1;
  const [, ok, v] = __tape[k];
  if (!ok) { const err = new Error(v); err.name = "ToolError"; throw err }
  return typeof v === "object" && v !== null && "$big" in v ? __bigIssues(v.$big) : JSON.parse(JSON.stringify(v));
};
const read = (...a) => __call("read", a), bash = (...a) => __call("bash", a), mcp = (...a) => __call("mcp", a), edit = (...a) => __call("edit", a), write = (...a) => __call("write", a);
"""


def trim(v):
    if isinstance(v, str) and len(v) > CAP:
        cut = v[:CAP]
        return cut[: cut.rfind("\n") + 1] or cut
    if isinstance(v, dict):
        return {k: trim(x) for k, x in v.items()}
    if isinstance(v, list):
        return [trim(x) for x in v]
    return v


def entries(t, cells=None):
    out = []
    for e in t["tape"]:
        if cells is not None and e["c"] not in cells:
            continue
        if e.get("big"):
            v = {"$big": min(e["big"], 60)}
        elif e["ok"]:
            v = trim(json.loads(e["v"]))
        else:
            v = e["v"][:2000]
        out.append([e["n"], e["ok"], v])
    return out


def mutate_data(v, rng, how):
    if isinstance(v, dict):
        return {k: mutate_data(x, rng, how) for k, x in v.items()}
    if isinstance(v, list):
        return [mutate_data(x, rng, how) for x in v]
    if not isinstance(v, str) or "\n" not in v:
        return v
    lines = v.split("\n")
    tail = lines[-1] == ""
    body = lines[:-1] if tail else lines
    if how == "reverse":
        body = body[::-1]
    elif how == "half":
        body = body[: max(1, len(body) // 2)]
    elif how == "dup":
        body = body + body[: max(1, len(body) // 3)]
    elif how == "unicode":
        body = [l + (" é😀" if i % 3 == 0 and l else "") for i, l in enumerate(body)]
    elif how == "empty":
        body = []
    return "\n".join(body) + ("\n" if tail else "")


def prelude(tape):
    return BIG + "const __tape = " + json.dumps(tape, ensure_ascii=False) + ";\n" + FAKE


def code_mutations(code, rng):
    muts = []
    m = re.sub(r"\bslice\(0,\s*(\d+)\)", lambda x: "slice(0, %d)" % max(1, int(x.group(1)) // 7), code)
    if m != code:
        muts.append(("slice", m))
    m = re.sub(r"(?m)^const\b", "let", code)
    if m != code:
        muts.append(("let", m))
    m = re.sub(r"\(a, b\) => b(\S*) - a\1", lambda x: "(a, b) => a%s - b%s" % (x.group(1), x.group(1)), code)
    if m != code:
        muts.append(("sortdir", m))
    m = code.replace("console.log(", "print(")
    if m != code:
        muts.append(("print", m))
    m = re.sub(r"\.forEach\((\w+) => ", r".map(\1 => ", code)
    if m != code:
        muts.append(("map", m))
    m = re.sub(r"\.split\(\"\\n\"\)", '.split(/\\r?\\n/)', code)
    if m != code:
        muts.append(("splitre", m))
    m = re.sub(r"\.includes\(([^()]+)\)", r".indexOf(\1) !== -1", code)
    if m != code:
        muts.append(("indexof", m))
    m = re.sub(r"\btoLowerCase\(\)", "toUpperCase()", code)
    if m != code:
        muts.append(("case", m))
    return muts


n = 0


def emit(name, code):
    global n
    if len(code) > 60000:
        return
    n += 1
    open(os.path.join(OUT, name + ".js"), "w").write(code)


for f in sorted(glob.glob(os.path.join(SRC, "*.json"))):
    t = json.load(open(f))
    rid = t["rid"]
    rng = random.Random(rid)
    cells = t["cells"]
    tape = entries(t)
    joined = "\n//// cell\n".join(cells)
    emit(rid + "_flow", prelude(tape) + "//// cell\n" + joined)
    how = rng.choice(["reverse", "half", "dup", "unicode", "empty"])
    emit(rid + "_data_" + how, prelude(mutate_data(tape, rng, how)) + "//// cell\n" + joined)
    others = []
    declared = set()
    for i, c in enumerate(cells):
        own = entries(t, {i})
        uses_earlier = any(re.search(r"\b%s\b" % re.escape(d), c) for d in declared)
        declared |= set(re.findall(r"(?:const|let|var|function|class)\s+([A-Za-z_$][\w$]*)", c))
        if (not own and i > 0) or uses_earlier:
            continue
        for tag, m in code_mutations(c, rng):
            others.append(("%s_c%d_%s" % (rid, i, tag), prelude(own) + "//// cell\n" + m))
        others.append(("%s_c%d_iife" % (rid, i), prelude(own) + "//// cell\nreturn await (async () => {\n" + c + "\n})()"))
    for name, code in rng.sample(others, min(2, len(others))):
        emit(name, code)
print(n)
