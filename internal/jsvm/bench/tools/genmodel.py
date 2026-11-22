import argparse, json, os, random, re, subprocess, sys, threading, time, urllib.request, urllib.error, concurrent.futures as cf

ap = argparse.ArgumentParser()
ap.add_argument("out")
ap.add_argument("n", type=int, nargs="?", default=60)
ap.add_argument("--provider", choices=["anthropic", "openrouter"], default="anthropic")
ap.add_argument("--mix", default="")
ap.add_argument("--cap", type=float, default=None)
ap.add_argument("--start", type=int, default=0)
ap.add_argument("--tasks", choices=["corpus", "heldout"], default="corpus")
ARGS = ap.parse_args()
OUT, TARGET, PROVIDER = ARGS.out, ARGS.n, ARGS.provider

PROVIDERS = {
    "anthropic": {
        "models": {"haiku": "claude-haiku-5-5", "sonnet": "claude-sonnet-5-5"},
        "mix": ["haiku"] * 9 + ["sonnet"],
        "cap": 5.0,
        "price": {"haiku": (0.10, 0.50), "sonnet": (2.00, 10.00)},
    },
    "openrouter": {
        "models": {"luna": "openai/gpt-6-luna", "glm": "z-ai/glm-5.3-flash", "haiku": "anthropic/claude-haiku-5.5", "sonnet": "anthropic/claude-sonnet-5.5"},
        "mix": ["glm"] * 5 + ["luna"] * 4 + ["haiku"] * 2,
        "cap": 1.35,
    },
}
CONF = PROVIDERS[PROVIDER]
MODELS = CONF["models"]
MIX = ARGS.mix.split(",") if ARGS.mix else CONF["mix"]
CAP = ARGS.cap if ARGS.cap is not None else CONF["cap"]
for m in MIX:
    if m not in MODELS:
        sys.exit("model %r is not available for provider %s" % (m, PROVIDER))

SYS = """You write JavaScript "cells" for a coding agent's code mode. A cell is the body of an async function run in a sandboxed VM (no Node APIs, no require, no fs, no fetch). Top-level await works. The cell's result is everything passed to console.log followed by the value of the last expression statement (or of a top-level return). In real use a cell calls host functions read/bash/mcp; here the cells must be SELF-CONTAINED: paste the tool output the cell works on into the cell as string literals or JSON, or define a small fake async function inside the cell that returns canned data, and then process it the way an agent would.
Rules: deterministic (no Date.now, new Date() without arguments, Math.random, timers longer than a few ms), no real host calls (never call read, write, edit, bash or mcp), finishes in well under 100 ms, prints a compact result."""

TASKS = [
    "parse `git log --pretty=format:'%h|%an|%ad|%s' --date=short` output and summarise commits per author and per week",
    "parse `grep -rn TODO .` output and group hits by directory, then list files with the most hits",
    "parse `ls -l` output, find the largest files and total size per extension",
    "parse `go test -json` event lines and report failed tests with their output and package timings",
    "parse a CSV export (with quoted fields containing commas) and compute per-category totals and averages",
    "work through a JSON API response of issues: filter by state and priority, sort, and print the top N",
    "diff two lists of names or package versions (added, removed, changed) and print a short report",
    "apply a set of regex-based text edits to a source file held in a string and print a unified-looking summary of changed lines",
    "format records as an aligned plain-text table with padded columns and a totals row",
    "page through a fake paginated API (cursor or page number) until exhausted and aggregate the results",
    "fetch details for several ids from a fake async API concurrently and merge them, keeping input order",
    "call a fake async API that fails for some inputs, collect successes and failures separately, and report both",
    "parse `go build` / compiler error output and group errors by file with line numbers",
    "parse `git diff --stat` and `git status --porcelain` output and summarise the change",
    "parse an nginx/access log excerpt: requests per status code, slowest paths, unique IPs",
    "extract function names and line numbers from Go or JS source code held in a string",
    "parse `ps aux` or `df -h` output and report the heaviest entries with human-readable sizes",
    "build a dependency graph from `go list -deps`-like lines or package.json data and print a topological order",
    "rename an identifier across several in-memory files (map of path to content) safely using word boundaries, report counts per file",
    "validate a list of config objects against simple rules and print a list of problems",
    "parse a markdown document held in a string: extract headings into a table of contents and count words per section",
    "parse key=value or INI-style config text into nested objects and print selected settings",
    "compute statistics (min, max, mean, median, p95) over numbers parsed from benchmark output lines",
    "deduplicate and merge records from two sources by key, resolving conflicts by the newest timestamp field (timestamps are fixed strings)",
    "parse a stack trace or panic log and point at the first frame inside the project",
    "parse `docker ps`/`kubectl get pods` style column output and filter unhealthy entries",
    "convert between date strings in fixed formats (ISO 8601 inputs, UTC only) and bucket events by day",
    "word frequency and n-gram counts over a text held in a string, ignoring case and punctuation",
    "simulate a small job queue with retries over fake async workers and report the final status of each job",
    "parse JSON lines logs, group by level and module, and print the latest message per group",
]

HELDOUT = [
    "analyse an application log (timestamps as fixed ISO strings, levels, request ids): error bursts per minute, the slowest request ids, a short incident summary",
    "build a dependency graph from a list of `a -> b` edges: detect cycles, print a topological order and the longest dependency chain",
    "diff two versions of a text held in strings line by line (a simple LCS) and print a unified-style diff with context lines",
    "merge layered configuration objects (defaults, environment, overrides) with deep merge rules, array replacement and a list of overridden keys",
    "render records as a markdown table with column alignment, escaped pipes and a totals row",
    "sort and filter version strings by semver rules (pre-release tags, build metadata), pick the latest compatible with a caret or tilde range",
    "manipulate file paths held as strings: normalise `..` and `.`, compute relative paths, group by extension and directory depth",
    "parse TSV and CSV with quoted fields and embedded newlines, infer column types and print per-column statistics",
    "implement a small state machine (order lifecycle, traffic light or tokenizer) driven by a list of events, rejecting invalid transitions",
    "compute rates, moving averages and percentiles (p50/p90/p99) over request-latency samples parsed from text",
    "bucket calendar events given as ISO date strings by ISO week, month and weekday (UTC only) and find gaps and overlaps",
    "rate-limit simulation: token bucket over a list of timestamped requests, report which were rejected",
    "parse `kubectl get events`-style lines and summarise reasons per object kind",
    "text templating: fill `{{name}}` placeholders from nested data with defaults and report missing keys",
]

DIFF = ["mostly short cells (3-15 lines)", "a mix of short and medium cells (10-40 lines)", "mostly longer, careful cells (30-90 lines) with helper functions", "a mix of all lengths"]

lock = threading.Lock()
state = {"spent": 0.0, "done": 0}
spend_file = os.path.join(OUT, "spend-%s.json" % PROVIDER)
if os.path.exists(spend_file):
    state.update(json.load(open(spend_file)))


def key():
    if PROVIDER == "openrouter":
        cmd = ["pass", "show", "i/openrouter.ai-api-key"]
    else:
        conf = json.load(open(os.path.expanduser("~/.config/qi/config.json")))["providers"]["anthropic"]
        if conf.get("api_key"):
            return conf["api_key"].strip()
        cmd = ["sh", "-c", conf["api_key_shell"]]
    env = dict(os.environ, PATH="/opt/homebrew/bin:" + os.environ.get("PATH", ""))
    p = subprocess.run(cmd, capture_output=True, text=True, check=True, env=env)
    k = p.stdout.split("\n", 1)[0].strip()
    if not k:
        sys.exit("empty API key from the %s key command" % PROVIDER)
    return k


KEY = key()


def request(body):
    if PROVIDER == "openrouter":
        return urllib.request.Request("https://openrouter.ai/api/v1/chat/completions", json.dumps(body).encode(), {"Authorization": "Bearer " + KEY, "Content-Type": "application/json"})
    return urllib.request.Request("https://api.anthropic.com/v1/messages", json.dumps(body).encode(), {"x-api-key": KEY, "anthropic-version": "2026-10-21", "content-type": "application/json"})


def api(body):
    last = None
    for a in range(3):
        try:
            r = json.load(urllib.request.urlopen(request(body), timeout=300))
            if r.get("choices") or r.get("content"):
                return r
            last = json.dumps(r)[:300]
        except urllib.error.HTTPError as e:
            last = "HTTP %d %s" % (e.code, e.read()[:300])
        except Exception as e:
            last = repr(e)[:200]
        time.sleep(3 * (a + 1))
    raise RuntimeError(last)


def prompt(rng, n):
    tasks = rng.sample(HELDOUT if ARGS.tasks == "heldout" else TASKS, 4)
    multi = rng.randint(2, 4)
    lines = "\n".join("- " + t for t in tasks)
    return f"""Write {n} different cells, {DIFF[rng.randrange(len(DIFF))]}. Spread them over these tasks (several cells per task, each with different embedded data and a different question about it):
{lines}
{multi} of the {n} entries should be a short sequence of 2-3 cells separated by a line containing exactly `//// cell`, where later cells reuse variables computed by earlier ones, as an agent does over several calls.
Write the cells the way you would naturally write them as an agent. Output format: for each entry a line `=== script K ===` (K = 1..{n}) followed by the raw code. No markdown fences, no explanations."""


def parse(text):
    parts = re.split(r"(?m)^=== script \d+ ===\s*$", text)
    out = []
    for p in parts[1:]:
        p = re.sub(r"(?m)^```\w*\s*$", "", p).strip("\n")
        p = re.sub(r"(?m)^\s*////\s*(cell)?\s*$", "//// cell", p)
        if p.strip():
            out.append(p.rstrip() + "\n")
    return out


def body_for(model, user):
    if PROVIDER == "openrouter":
        body = {"model": MODELS[model], "messages": [{"role": "system", "content": SYS}, {"role": "user", "content": user}], "max_tokens": 14000, "usage": {"include": True}, "temperature": 0.9}
        if model in ("luna", "glm"):
            body["reasoning"] = {"effort": "low"}
        return body
    return {"model": MODELS[model], "max_tokens": 16000, "system": SYS, "messages": [{"role": "user", "content": user}], "output_config": {"effort": "low"}}


def unpack(model, r):
    if PROVIDER == "openrouter":
        u = r.get("usage") or {}
        return r["choices"][0]["message"].get("content") or "", u.get("cost") or 0, u.get("prompt_tokens"), u.get("completion_tokens")
    u = r.get("usage") or {}
    tin = (u.get("input_tokens") or 0) + (u.get("cache_creation_input_tokens") or 0) + (u.get("cache_read_input_tokens") or 0)
    tout = u.get("output_tokens") or 0
    pin, pout = CONF["price"][model]
    text = "".join(b.get("text", "") for b in r.get("content", []) if b.get("type") == "text")
    return text, (tin * pin + tout * pout) / 1e6, tin, tout


def job(i):
    with lock:
        if state["spent"] >= CAP:
            return None
    rng = random.Random(1000 + i)
    model = MIX[i % len(MIX)]
    n = rng.choice([10, 12, 15])
    try:
        r = api(body_for(model, prompt(rng, n)))
    except Exception as e:
        print("req", i, model, "error", e, flush=True)
        return None
    text, cost, tin, tout = unpack(model, r)
    scripts = parse(text)
    with lock:
        state["spent"] += cost
        state["done"] += 1
        json.dump(state, open(spend_file, "w"))
        rec = {"req": i, "provider": PROVIDER, "model": model, "cost": cost, "in": tin, "out": tout, "n": len(scripts), "text": text}
        with open(os.path.join(OUT, "raw.jsonl"), "a") as f:
            f.write(json.dumps(rec) + "\n")
    print("req", i, model, "scripts", len(scripts), "cost %.4f total %.4f" % (cost, state["spent"]), flush=True)
    return scripts


if __name__ == "__main__":
    os.makedirs(OUT, exist_ok=True)
    with cf.ThreadPoolExecutor(4) as ex:
        list(ex.map(job, range(ARGS.start, ARGS.start + TARGET)))
    print("%s spent %.4f (cap %.2f)" % (PROVIDER, state["spent"], CAP))
