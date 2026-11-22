import json, random, sys
random.seed(int(sys.argv[1]))
N = int(sys.argv[2])
CH = ['a', 'b', 'c', 'A', '1', ' ', '\\n', 'é', '.', '\\d', '\\w', '\\s', '\\W', '[ab]', '[^a]', '[a-c]', '[^]', '\\u00e9']
def atom(d, st):
    r = random.random()
    if d > 3 or r < 0.45:
        return random.choice(CH)
    if r < 0.55:
        return random.choice(['^', '$', '\\b', '\\B'])
    if r < 0.70:
        st['g'] += 1
        if random.random() < 0.2:
            st['names'].append('n%d' % st['g'])
            return '(?<n%d>%s)' % (st['g'], alt(d + 1, st))
        return '(' + alt(d + 1, st) + ')'
    if r < 0.78:
        return '(?:' + alt(d + 1, st) + ')'
    if r < 0.88:
        return random.choice(['(?=', '(?!', '(?<=', '(?<!']) + alt(d + 1, st) + ')'
    if st['g'] > 0:
        if st['names'] and random.random() < 0.3:
            return '\\k<%s>' % random.choice(st['names'])
        return '\\%d' % random.randint(1, st['g'])
    return 'a'
def quant(a):
    if a in ('^', '$', '\\b', '\\B') or a.startswith('(?<=') or a.startswith('(?<!') or a.startswith('(?=') or a.startswith('(?!'):
        return a
    r = random.random()
    if r < 0.6:
        return a
    q = random.choice(['*', '+', '?', '{2}', '{1,3}', '{0,2}', '{2,}'])
    if random.random() < 0.3:
        q += '?'
    return a + q
def seq(d, st):
    return ''.join(quant(atom(d, st)) for _ in range(random.randint(1, 4)))
def alt(d, st):
    return '|'.join(seq(d, st) for _ in range(random.choice([1, 1, 1, 2, 3])))
SUB = ['a', 'a', 'b', 'b', 'c', 'A', '1', ' ', '\n', 'é', '😀', 'B']
cases = []
for _ in range(N):
    st = {'g': 0, 'names': []}
    p = alt(0, st)
    f = ''.join(c for c in 'imsuy' if random.random() < 0.25)
    for _ in range(2):
        s = ''.join(random.choice(SUB) for _ in range(random.randint(0, 12)))
        cases.append([p, f, s])
json.dump(cases, sys.stdout, ensure_ascii=False)
