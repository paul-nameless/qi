package regex

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

type corpusCase struct {
	P    string  `json:"p"`
	F    string  `json:"f"`
	S    string  `json:"s"`
	Err  bool    `json:"err"`
	M    [][]int `json:"m"`
	Goja string  `json:"goja"`
}

func loadCorpus(t testing.TB) []corpusCase {
	return loadCorpusFile(t, "testdata/corpus.json")
}

func loadCorpusFile(t testing.TB, path string) []corpusCase {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cs []corpusCase
	if err := json.Unmarshal(data, &cs); err != nil {
		t.Fatal(err)
	}
	return cs
}

type posMap struct {
	units   []uint16
	toByte  map[int]int
	toUnit  map[int]int
	unicode bool
}

func newPosMap(s string, unicodeMode bool) *posMap {
	pm := &posMap{toByte: map[int]int{}, toUnit: map[int]int{}, unicode: unicodeMode}
	b := 0
	for _, r := range s {
		pm.toByte[len(pm.units)] = b
		pm.toUnit[b] = len(pm.units)
		pm.units = utf16.AppendRune(pm.units, r)
		b += len(string(r))
	}
	pm.toByte[len(pm.units)] = b
	pm.toUnit[b] = len(pm.units)
	return pm
}

func (pm *posMap) advance(i int) int {
	if pm.unicode && i+1 < len(pm.units) && utf16.IsSurrogate(rune(pm.units[i])) && pm.units[i] < 0xDC00 && pm.units[i+1] >= 0xDC00 {
		return i + 2
	}
	return i + 1
}

func iterate(re *Regexp, s string, bytes bool) ([][]int, bool) {
	pm := newPosMap(s, re.Unicode())
	var out [][]int
	pos := 0
	for len(out) < 50 && pos <= len(pm.units) {
		var m []int
		if bytes {
			bp, ok := pm.toByte[pos]
			if !ok {
				return nil, false
			}
			m = re.ExecBytes(s, bp)
			for i, v := range m {
				if v >= 0 {
					m[i] = pm.toUnit[v]
				}
			}
		} else {
			m = re.Exec(s, pos)
		}
		if m == nil {
			break
		}
		out = append(out, m)
		if m[1] == m[0] {
			pos = pm.advance(m[1])
		} else {
			pos = m[1]
		}
	}
	return out, true
}

func bytesComparable(c corpusCase) bool {
	pm := newPosMap(c.S, strings.Contains(c.F, "u"))
	for _, m := range c.M {
		for _, v := range m {
			if _, ok := pm.toByte[v]; v >= 0 && !ok {
				return false
			}
		}
	}
	return true
}

func TestCorpus(t *testing.T) {
	runCorpus(t, loadCorpus(t))
}

func TestRandomCorpus(t *testing.T) {
	runCorpus(t, loadCorpusFile(t, "testdata/random.json"))
}

func TestExtraCorpus(t *testing.T) {
	path := os.Getenv("REGEX_EXTRA_CORPUS")
	if path == "" {
		t.Skip("REGEX_EXTRA_CORPUS not set")
	}
	runCorpus(t, loadCorpusFile(t, path))
}

func runCorpus(t *testing.T, cs []corpusCase) {
	re2Used, failures := 0, 0
	for i, c := range cs {
		for _, eng := range []int{EngineAuto, EngineBacktrack, EngineRE2} {
			name := fmt.Sprintf("%d/%q/%s/eng%d", i, c.P, c.F, eng)
			re, err := CompileEngine(c.P, c.F, eng)
			if c.Err {
				if err == nil {
					t.Errorf("%s: expected syntax error", name)
					failures++
				}
				continue
			}
			if err != nil && strings.Contains(err.Error(), "unsupported") {
				t.Logf("%s: %v", name, err)
				continue
			}
			if err != nil {
				t.Errorf("%s: unexpected error %v", name, err)
				failures++
				continue
			}
			if eng == EngineRE2 && re.re2 != nil {
				re2Used++
			}
			got, _ := iterate(re, c.S, false)
			if !equalMatches(got, c.M) {
				t.Errorf("%s on %q:\n got  %v\n want %v", name, trunc(c.S), got, c.M)
				failures++
				continue
			}
			if !bytesComparable(c) {
				continue
			}
			if got, ok := iterate(re, c.S, true); ok && !equalMatches(got, c.M) {
				t.Errorf("%s bytes on %q:\n got  %v\n want %v", name, trunc(c.S), got, c.M)
				failures++
			}
		}
	}
	t.Logf("%d cases, %d RE2-eligible, %d failures", len(cs), re2Used, failures)
}

func equalMatches(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !slices.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func trunc(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}
