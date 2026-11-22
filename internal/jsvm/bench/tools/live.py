import argparse, concurrent.futures as cf, json, os, re, subprocess, sys, threading, time, urllib.error, urllib.request

ap = argparse.ArgumentParser()
ap.add_argument("--h2h", required=True)
ap.add_argument("--work", required=True)
ap.add_argument("--out", default="corpus/live")
ap.add_argument("--engines", default="jsvm,goja")
ap.add_argument("--plan", default="haiku:3,sonnet:2")
ap.add_argument("--tasks", default="")
ap.add_argument("--cap", type=float, default=8.0)
ap.add_argument("-j", type=int, default=6)
ap.add_argument("--bindir", default="")
ARGS = ap.parse_args()
sys.path.insert(0, ARGS.h2h)
import h2h

BENCH = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MODELS = {"haiku": "claude-haiku-5-5", "sonnet": "claude-sonnet-5-5"}
PRICE = {"haiku": (0.10, 0.50), "sonnet": (2.00, 10.00)}
SYS_C = h2h.SYS + "Tools: one tool, run, which executes JavaScript that can call read, write, edit, bash and mcp. You may call run several times; variables persist between calls. MCP servers: linear (tool list_issues, arguments {team: string}), reachable only through mcp() inside run."
RUN_DESC = """Run JavaScript in a persistent sandboxed VM (no Node APIs, no require, no fs, no fetch). Top-level await works. Variables, functions and classes declared at top level stay defined for later calls, so you can fetch data in one call and work on it in the next. The tool result is everything passed to console.log followed by the value of the last expression (or of a top-level return), JSON-encoded unless it is a string. Output over 8000 chars is truncated; results of the functions below are never truncated inside the code, so filter and summarize in code and output only what you need.
Async functions (await them):
- read(path, offset?, limit?) -> string. offset = 1-based first line, limit = max number of lines. Rejects if the file is missing.
- write(path, content) -> string. Creates or overwrites a file.
- edit(path, old, new) -> string. Replaces old with new; rejects unless old occurs exactly once in the file.
- bash(cmd) -> {output, exit_code}. Runs a bash command in the repo root (60s timeout); output is stdout and stderr combined. Does not reject on a non-zero exit.
- mcp(server, tool, args) -> the parsed JSON result of an MCP tool.
A failed call rejects with an Error. Use Promise.all to run independent calls in parallel."""
TOOLS = [{"name": "run", "description": RUN_DESC, "input_schema": {"type": "object", "properties": {"code": {"type": "string"}}, "required": ["code"]}}]
TASKS = dict(h2h.TASKS)
TASKS["m1_mcp"] = "Use the linear MCP server (team SER). How many issues are in state Todo with priority 3 or higher? Also give the identifiers of the 3 Todo issues with the highest estimate (ties: lower identifier number first). Answer exactly as two lines: `count: N` and `top: ID, ID, ID`."
FOLLOW = {"m1_mcp": "Now, among the issues in state In Progress, which assignee has the most issues? Answer exactly as `assignee: NAME (N)`."}
TASK_IDS = ["t1_todo", "t2_rename", "t3_test", "t4_summary", "t5_import", "t6_count", "m1_mcp"]

lock = threading.Lock()
spend = {"spent": 0.0}
spend_file = os.path.join(ARGS.out, "spend.json")


def key():
    conf = json.load(open(os.path.expanduser("~/.config/qi/config.json")))["providers"]["anthropic"]
    if conf.get("api_key"):
        return conf["api_key"].strip()
    env = dict(os.environ, PATH="/opt/homebrew/bin:" + os.environ.get("PATH", ""))
    k = subprocess.run(["sh", "-c", conf["api_key_shell"]], capture_output=True, text=True, check=True, env=env).stdout.split("\n", 1)[0].strip()
    if not k:
        sys.exit("empty Anthropic API key")
    return k


KEY = key()


def api(body):
    last = None
    for a in range(4):
        try:
            req = urllib.request.Request("https://api.anthropic.com/v1/messages", json.dumps(body).encode(), {"x-api-key": KEY, "anthropic-version": "2026-10-21", "content-type": "application/json"})
            return json.load(urllib.request.urlopen(req, timeout=300))
        except urllib.error.HTTPError as e:
            last = "HTTP %d %s" % (e.code, e.read()[:300])
            if e.code < 500 and e.code != 429:
                break
        except Exception as e:
            last = repr(e)[:200]
        time.sleep(5 * (a + 1))
    raise RuntimeError(last)


def cost(model, u):
    pin, pout = PRICE[model]
    return ((u.get("input_tokens") or 0) * pin + (u.get("cache_creation_input_tokens") or 0) * pin * 1.25 + (u.get("cache_read_input_tokens") or 0) * pin * 0.1 + (u.get("output_tokens") or 0) * pout) / 1e6


class Engine:
    def __init__(self, engine, d):
        self.p = subprocess.Popen(["sandbox-exec", "-f", ARGS.h2h + "/sb.profile", (ARGS.bindir or BENCH + "/bin") + "/" + engine, "serve", d], cwd=d, env=h2h.ENV, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)

    def run(self, code):
        t = threading.Timer(200, self.p.kill)
        t.start()
        try:
            self.p.stdin.write((json.dumps({"code": code}) + "\n").encode())
            self.p.stdin.flush()
            return json.loads(self.p.stdout.readline())
        except Exception as e:
            return {"out": "error: the JavaScript VM crashed: " + repr(e)[:200], "failed": True, "err": "crash", "calls": [], "ms": 0}
        finally:
            t.cancel()

    def close(self):
        try:
            self.p.kill()
        except Exception:
            pass


def text_of(content):
    return "".join(b.get("text", "") for b in content if b.get("type") == "text")


def run_one(model, engine, task, i):
    rid = "%s_%s_%s_%d" % (model, engine, task, i)
    with lock:
        if spend["spent"] >= ARGS.cap:
            return None
    d = os.path.join(ARGS.work, rid)
    h2h.copy_fixture(task, d)
    eng = Engine(engine, d)
    msgs = [{"role": "user", "content": TASKS[task]}]
    m = dict(id=rid, model=model, engine=engine, task=task, i=i, rounds=0, in_tok=0, cache_read=0, cache_write=0, out_tok=0, cost=0.0, cells=0, failed_cells=0, unsupported=0, recovered=0, features=[], err="", capped=False)
    cells, answers, follow = [], [], [FOLLOW[task]] if task in FOLLOW else []
    t0 = time.time()
    try:
        while True:
            if m["rounds"] >= 25:
                m["capped"] = True
                break
            body = {"model": MODELS[model], "max_tokens": 16000, "system": SYS_C, "tools": TOOLS, "messages": msgs, "cache_control": {"type": "ephemeral"}, "output_config": {"effort": "medium"}}
            r = api(body)
            m["rounds"] += 1
            u = r.get("usage") or {}
            c = cost(model, u)
            m["in_tok"] += u.get("input_tokens") or 0
            m["cache_read"] += u.get("cache_read_input_tokens") or 0
            m["cache_write"] += u.get("cache_creation_input_tokens") or 0
            m["out_tok"] += u.get("output_tokens") or 0
            m["cost"] += c
            with lock:
                spend["spent"] += c
                json.dump(spend, open(spend_file, "w"))
            content = r.get("content") or []
            msgs.append({"role": "assistant", "content": content})
            uses = [b for b in content if b.get("type") == "tool_use"]
            if not uses:
                answers.append(text_of(content))
                if follow:
                    msgs.append({"role": "user", "content": follow.pop(0)})
                    continue
                break
            results = []
            for b in uses:
                code = (b.get("input") or {}).get("code")
                if b.get("name") != "run" or not isinstance(code, str):
                    out, res = "error: call run with a code string", {"failed": True, "err": "bad-call"}
                    cells.append({"code": None, "out": out, "failed": True, "err": "bad-call"})
                else:
                    res = eng.run(code)
                    out = res.get("out") or "(no output)"
                    cells.append({"code": code, "out": out, "failed": bool(res.get("failed")), "err": res.get("err", ""), "calls": res.get("calls") or [], "ms": res.get("ms")})
                m["cells"] += 1
                m["failed_cells"] += int(cells[-1]["failed"])
                results.append({"type": "tool_result", "tool_use_id": b["id"], "content": out, "is_error": bool(cells[-1]["failed"])})
            msgs.append({"role": "user", "content": results})
    except Exception as e:
        m["err"] = repr(e)[:300]
    eng.close()
    for k, cell in enumerate(cells):
        hit = re.search(r"unsupported: ([^\n@]+)", cell.get("err") or "") if cell["failed"] else None
        if hit:
            m["unsupported"] += 1
            m["features"].append(hit.group(1).strip())
            if k + 1 < len(cells) and not cells[k + 1]["failed"]:
                m["recovered"] += 1
    m["wall"] = round(time.time() - t0, 1)
    if m["err"]:
        ok, why = False, "error"
    elif task == "m1_mcp":
        a0, a1 = (answers + ["", ""])[:2]
        cnt = re.search(r"count:\s*`?(\d+)", a0)
        top = re.search(r"top:\s*`?([A-Z0-9, -]+)", a0)
        asg = re.search(r"assignee:\s*`?(\w+)\s*\((\d+)\)", a1)
        ok1 = bool(cnt and cnt.group(1) == "24" and top and [x.strip() for x in top.group(1).split(",")][:3] == ["SER-1015", "SER-1055", "SER-1095"])
        ok2 = bool(asg and asg.group(1).lower() == "ana" and asg.group(2) == "30")
        ok, why = ok1 and ok2, "first=%s second=%s" % (ok1, ok2)
    else:
        ok, why = h2h.check(task, d, answers[0] if answers else "")
    m["ok"] = bool(ok) and not m["capped"]
    m["why"] = why
    m["cost"] = round(m["cost"], 6)
    json.dump({"metrics": m, "answers": answers, "cells": cells}, open(os.path.join(ARGS.out, rid + ".json"), "w"), indent=1)
    return m


if __name__ == "__main__":
    os.makedirs(ARGS.out, exist_ok=True)
    os.makedirs(ARGS.work, exist_ok=True)
    if os.path.exists(spend_file):
        spend.update(json.load(open(spend_file)))
    tasks = ARGS.tasks.split(",") if ARGS.tasks else TASK_IDS
    jobs = []
    for spec in ARGS.plan.split(","):
        model, n = spec.split(":")
        for engine in ARGS.engines.split(","):
            for task in tasks:
                for i in range(int(n)):
                    if not os.path.exists(os.path.join(ARGS.out, "%s_%s_%s_%d.json" % (model, engine, task, i))):
                        jobs.append((model, engine, task, i))
    with cf.ThreadPoolExecutor(ARGS.j) as ex, open(os.path.join(ARGS.out, "results.jsonl"), "a") as f:
        for m in ex.map(lambda j: run_one(*j), jobs):
            if m is None:
                continue
            f.write(json.dumps(m) + "\n")
            f.flush()
            print(m["id"], "ok" if m["ok"] else "FAIL", "cells=%d failed=%d unsupported=%d cost=%.4f total=%.4f %s %s" % (m["cells"], m["failed_cells"], m["unsupported"], m["cost"], spend["spent"], m["why"], m["err"]), flush=True)
    print("spent %.4f" % spend["spent"])
