import json, os, re, sys

RAW, OUT = sys.argv[1], sys.argv[2]
os.makedirs(OUT, exist_ok=True)


def parse(text):
    parts = re.split(r"(?m)^=== script \d+ ===\s*$", text)
    out = []
    for p in parts[1:]:
        p = re.sub(r"(?m)^```\w*\s*$", "", p).strip("\n")
        p = re.sub(r"(?m)^\s*////\s*(cell)?\s*$", "//// cell", p)
        if not p.strip():
            continue
        if p.lstrip().startswith("//// cell") and out:
            out[-1] = out[-1] + p.rstrip() + "\n"
            continue
        out.append(p.rstrip() + "\n")
    return out


n = 0
for line in open(RAW):
    r = json.loads(line)
    for k, code in enumerate(parse(r["text"])):
        open(os.path.join(OUT, "%s_r%03d_s%02d.js" % (r["model"], r["req"], k + 1)), "w").write(code)
        n += 1
print(n)
