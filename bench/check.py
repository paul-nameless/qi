#!/usr/bin/env python3
import hashlib, json, os, re, shutil, subprocess, sys

BENCH = os.path.dirname(os.path.abspath(__file__))


def gofiles(d):
    for r, _, fs in os.walk(d):
        for f in fs:
            if f.endswith(".go"):
                yield os.path.relpath(os.path.join(r, f), d)


def go(d, *args):
    try:
        p = subprocess.run(["go", *args], cwd=d, capture_output=True, text=True, timeout=180)
    except subprocess.TimeoutExpired:
        return False
    return p.returncode == 0


def test_hash(d):
    h = hashlib.sha1()
    for f in sorted(gofiles(d)):
        if f.endswith("_test.go"):
            h.update(f.encode())
            h.update(open(os.path.join(d, f), "rb").read())
    return h.hexdigest()


def norm(p):
    return re.sub(r"^\./", "", p.strip("`*'\" "))


def check(task, work, answer):
    meta = json.load(open(os.path.join(BENCH, "tasks", task, "meta.json")))
    kind = meta["kind"]
    pristine = os.path.join(BENCH, "fixtures", meta["fixture"]) if meta.get("fixture") else os.path.join(BENCH, "tasks", task, "workspace")
    if kind == "gotest":
        for f in os.listdir(pristine):
            if f.endswith("_test.go") or f == "go.mod":
                shutil.copy(os.path.join(pristine, f), os.path.join(work, f))
        ok = go(work, "test", "./...")
        return ok, "go test " + ("passes" if ok else "fails")
    if kind == "todo":
        want = set()
        for f in gofiles(pristine):
            for i, l in enumerate(open(os.path.join(pristine, f)).read().split("\n"), 1):
                if "TODO" in l and "auth" in l.split("TODO", 1)[1].lower():
                    want.add((f, i))
        got = {(norm(p), int(n)) for p, n in re.findall(r"([\w./-]+\.go):(\d+)", answer)}
        return got == want, "got %d want %d, extra %s missing %s" % (len(got), len(want), sorted(got - want), sorted(want - got))
    if kind == "rename":
        src = "\n".join(open(os.path.join(work, f)).read() for f in gofiles(work))
        left = len(re.findall(r"(?<!Test)FormatMoney\(", src))
        short = len(re.findall(r"FormatMoneyShort", src))
        defined = "func FormatPrice(" in src
        built = go(work, "build", "./...") and go(work, "test", "./...")
        ok = left == 0 and short == 5 and defined and "FormatPriceShort" not in src and built
        return ok, "left=%d short=%d/5 defined=%s build+test=%s" % (left, short, defined, built)
    if kind == "fixtest":
        ok = go(work, "test", "./...")
        same = test_hash(work) == test_hash(pristine)
        return ok and same, "tests=%s untouched=%s" % (ok, same)
    if kind == "count":
        counts = {}
        for f in gofiles(pristine):
            if not f.endswith("_test.go"):
                counts[f] = sum(1 for l in open(os.path.join(pristine, f)) if re.match(r"^func (\([^)]*\) )?[A-Z]", l))
        top3 = sorted(counts.items(), key=lambda x: -x[1])[:3]
        seen = []
        for p in re.findall(r"([\w./-]+\.go)", answer):
            if norm(p) not in seen:
                seen.append(norm(p))
        files_ok = set(seen[:3]) == {f for f, _ in top3}
        counts_ok = all(re.search(re.escape(f) + r"[^\n]*?\b%d\b" % c, answer) for f, c in top3)
        return files_ok and counts_ok, "files=%s counts=%s want %s" % (files_ok, counts_ok, top3)
    return False, "unknown kind " + kind


if __name__ == "__main__":
    task, work = sys.argv[1], sys.argv[2]
    answer = open(sys.argv[3]).read() if len(sys.argv) > 3 and os.path.exists(sys.argv[3]) else ""
    ok, detail = check(task, work, answer)
    print(("PASS" if ok else "FAIL") + "  " + detail)
    sys.exit(0 if ok else 1)
