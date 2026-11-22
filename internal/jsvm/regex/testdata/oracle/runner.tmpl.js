const cases = __CASES__;
const hasD = (() => { try { new RegExp('a', 'd'); return true } catch (e) { return false } })();
function adv(s, i, u) {
  if (!u || i + 1 >= s.length) return i + 1;
  const c = s.charCodeAt(i);
  if (c >= 0xD800 && c <= 0xDBFF) { const d = s.charCodeAt(i + 1); if (d >= 0xDC00 && d <= 0xDFFF) return i + 2 }
  return i + 1;
}
const out = cases.map(([p, f, s]) => {
  let re;
  try { re = new RegExp(p, (f.includes('g') || f.includes('y') ? f : f + 'g') + (hasD && !f.includes('d') ? 'd' : '')) } catch (e) { return { e: String(e.message) } }
  const ms = [];
  let pos = 0;
  while (ms.length < 50 && pos <= s.length) {
    re.lastIndex = pos;
    let m;
    try { m = re.exec(s) } catch (e) { return { e: 'exec:' + e.message } }
    if (!m) break;
    const rec = { i: m.index, g: [...m].map(x => x === undefined ? null : x) };
    if (hasD) rec.d = m.indices.map(x => x === undefined ? null : x);
    ms.push(rec);
    const end = m.index + m[0].length;
    pos = end === m.index ? adv(s, end, f.includes('u')) : end;
  }
  return { m: ms };
});
return JSON.stringify(out);
