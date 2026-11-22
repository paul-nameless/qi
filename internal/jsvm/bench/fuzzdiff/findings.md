# fuzzdiff findings (2026-11-22)

500,000 generated programs (25 seeds, three phases) on goja, modernc quickjs and jsvm. goja and quickjs agree on 466,773 (93.4%). jsvm crashes, hangs, internal errors: 0.

## jsvm bugs
All seven are fixed as of commit 6356826; a fresh seed (31, 20,000 programs) shows 0 mismatches and 143 intended deviations.
1. `new Date(0).getTimezoneOffset()` returns `-0` instead of `0` (18,707 hits).
2. `Object.getOwnPropertyNames("ab")` gives `["0","1"]`, expected `["0","1","length"]` (1,921 hits).
3. `"a,b,c".split(/,/y)` gives `["a,b,c"]`, expected `["a","b","c"]`; `"aab abc".split(new RegExp("[ab]+","y"))` gives `[""," abc"]`, expected `[""," ","c"]`. Split must build its own sticky splitter and try every position (2,085 hits).
4. `Math.hypot(1e300,1)` gives `Infinity` (expected `1e+300`); `Math.hypot(5e-324)` gives `0`; `Math.hypot(2.5,0.5)` is off in the last digit. Needs scaling and compensated summation (about 350 hits).
5. `const a=[0]; a.x=1; Object.values(a)` gives `[0]`, expected `[0,1]` (12 hits).
6. Low priority: `(1.5).toString(3)` ends `…112`, both oracles `…111` (25 hits).
7. Low priority: `parseInt("-undefineda0a", 36)` gives `-4034103750424256500`, both oracles `-4034103750424256000` (7 hits).

## Intended deviations hit
- Lone surrogates become U+FFFD (2,645 programs): `JSON.stringify("😀".slice(0,1))`, `charAt`, `substr`, `padStart` with an emoji fill, `Object.entries("😀x")`, `"😀x".replaceAll("", "$$")`.
- Assigning to an undeclared name inside `"use strict"` code creates a global instead of throwing.
- Duplicate named groups across alternatives (ES2025) are a SyntaxError in the regex package.

## Oracle disagreements (excluded, 6.6%)
- quickjs v0.27 gives `{}` for `{..."hi"}`.
- `toString(radix)` on large integers and fraction digits.
- Last-ulp `Math` differences between Go `math` and C libm.
- Duplicate group names inside one alternative.

## Reproducing
- `go run ./fuzzdiff -n 20000 -seed 1 -out DIR` from `internal/jsvm/bench`; `-show SEED`, `-file F.js`, `-avoid tz,ownnames,hypot,splitsticky,values`.
