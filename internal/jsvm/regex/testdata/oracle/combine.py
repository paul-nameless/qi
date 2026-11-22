import json, sys
cases = json.load(open('cases.json'))
q = json.loads(json.load(open('qjs.json'))['out'])
g = json.loads(json.load(open('goja.json'))['out'])
out = []
diffs = 0
def strs(rec):
    return [rec['i']] + rec['g']
for (p, f, s), qa, ga in zip(cases, q, g):
    e = {'p': p, 'f': f, 's': s}
    if 'e' in qa:
        e['err'] = True
    else:
        ms = []
        for rec in qa['m']:
            flat = []
            for d in rec['d']:
                flat += [-1, -1] if d is None else d
            ms.append(flat)
        e['m'] = ms
    qe, ge = 'e' in qa, 'e' in ga
    agree = qe == ge and (qe or [strs(r) for r in qa['m']] == [strs(r) for r in ga['m']])
    if not agree:
        diffs += 1
        e['goja'] = 'differs'
        print('DIFF', json.dumps([p, f, s[:60]], ensure_ascii=False), '\n   qjs ', json.dumps(qa, ensure_ascii=False)[:300], '\n   goja', json.dumps(ga, ensure_ascii=False)[:300], file=sys.stderr)
    out.append(e)
print(len(out), 'cases,', diffs, 'goja diffs', file=sys.stderr)
json.dump(out, open(sys.argv[1], 'w'), ensure_ascii=False, indent=0)
